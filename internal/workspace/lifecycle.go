package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

type Lifecycle struct {
	cfg      config.Config
	registry Registry
	driver   runtime.Driver
}

type Status struct {
	Workspace Summary
	Container string
	Activity  Activity
	Pending   int // sessions currently blocked on a permission prompt
	Error     string
}

type AttachResultMsg struct {
	Err error
	// StillRunning reports whether the container is still up after the attach
	// command returned. Detaching (Ctrl-C) exits the attach client non-zero but
	// leaves opencode running, so this distinguishes a detach from a real
	// failure or a clean exit that stopped the container.
	StillRunning bool
}

type ShellResultMsg struct {
	Err error
}

func NewLifecycle(cfg config.Config) (Lifecycle, error) {
	driver, err := runtime.NewDriver(cfg.Runtime)
	if err != nil {
		return Lifecycle{}, err
	}

	return Lifecycle{cfg: cfg, registry: NewRegistry(cfg), driver: driver}, nil
}

func (l Lifecycle) EnsureBaseImage(ctx context.Context) error {
	slog.Debug("ensuring base image is available")
	if err := l.driver.Available(ctx); err != nil {
		return err
	}

	image := imageConfigFromConfig(l.cfg)
	baseImageName, err := managedBaseImageName(image)
	if err != nil {
		return err
	}

	return l.driver.BuildBaseImage(ctx, runtime.BaseBuildSpec{
		ImageName: baseImageName,
		FromImage: image.BaseImage,
		Packages:  image.Packages,
		Commands:  image.Commands,
	})
}

func (l Lifecycle) Statuses(ctx context.Context, workspaces []Summary) []Status {
	statuses := make([]Status, 0, len(workspaces))
	for _, ws := range workspaces {
		containerStatus, err := l.driver.ContainerStatus(ctx, ws.Manifest.ContainerName)
		status := Status{Workspace: ws, Container: containerStatus}
		if err != nil {
			status.Error = err.Error()
		}
		status.Activity, status.Pending = readActivity(ws.Manifest.HomeDir, containerStatus == runtime.StatusRunning)
		statuses = append(statuses, status)
	}

	return statuses
}

func (l Lifecycle) EnsureStarted(ctx context.Context, summary Summary) error {
	name := summary.Manifest.ContainerName
	slog.Info("ensuring workspace is started", "workspace", summary.Manifest.Name, "container", name)

	status, spec, err := l.provision(ctx, summary)
	if err != nil {
		return err
	}

	if status != runtime.StatusRunning {
		if err := l.driver.StartContainer(ctx, name); err != nil {
			slog.Warn("starting container failed, recreating", "workspace", summary.Manifest.Name, "container", name, "error", err)
			if recreateErr := l.recreateAndStart(ctx, summary, spec); recreateErr != nil {
				return fmt.Errorf("start failed: %w; recreate failed: %v", err, recreateErr)
			}
		}
		slog.Info("workspace container started", "workspace", summary.Manifest.Name, "container", name)
	} else {
		slog.Debug("workspace container already running", "workspace", summary.Manifest.Name, "container", name)
	}

	// Converge module state: a freshly (re)created container has lost its
	// writable layer, so reinstall any selected modules. This is a cheap no-op
	// (one marker read) when nothing changed. A failure here is logged but does
	// not prevent the container from being usable.
	if err := l.reconcile(ctx, summary); err != nil {
		slog.Warn("module reconcile failed", "workspace", summary.Manifest.Name, "container", name, "error", err)
	}

	return nil
}

func (l Lifecycle) provision(ctx context.Context, summary Summary) (string, runtime.ContainerSpec, error) {
	if err := l.driver.Available(ctx); err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	uid, gid, err := currentUserIDs()
	if err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	manifest := summary.Manifest
	baseImageName, err := managedBaseImageName(manifest.Image)
	if err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}
	if err := l.driver.BuildBaseImage(ctx, runtime.BaseBuildSpec{
		ImageName: baseImageName,
		FromImage: manifest.Image.BaseImage,
		Packages:  manifest.Image.Packages,
		Commands:  manifest.Image.Commands,
	}); err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	if err := l.driver.BuildImage(ctx, runtime.BuildSpec{
		ImageName: manifest.ImageName,
		BaseImage: baseImageName,
		UID:       uid,
		GID:       gid,
	}); err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	// Seed the workspace's OpenCode asset directories (and the manager status
	// plugin) into the bind-mounted home so they are writable by module scripts.
	if err := ensureWorkspaceOpenCodeAssets(manifest.HomeDir); err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	mounts, err := openCodeMounts(l.cfg.UseLocalOpenCodeAuth)
	if err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}
	// Mount the host module directory read-only so module install/uninstall
	// scripts are runnable inside the container.
	mounts = append(mounts, moduleMounts(l.cfg)...)
	if l.cfg.UseLocalOpenCodeAuth {
		if err := os.MkdirAll(filepath.Join(manifest.HomeDir, ".local", "share", "opencode"), 0o700); err != nil {
			return runtime.StatusUnknown, runtime.ContainerSpec{}, fmt.Errorf("create workspace OpenCode data directory: %w", err)
		}
	}

	spec := runtime.ContainerSpec{
		Name:      manifest.ContainerName,
		ImageName: manifest.ImageName,
		HomeDir:   manifest.HomeDir,
		UID:       uid,
		GID:       gid,
		Env:       manifest.Env,
		Mounts:    mounts,
		Command:   openCodeServeCommand(),
	}

	status, err := l.driver.ContainerStatus(ctx, manifest.ContainerName)
	if err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	// Recreate a container that was built from a now-outdated image (e.g. after
	// a base-image revision bump or an OpenCode update) so it picks up the new
	// image and run command.
	if status != runtime.StatusMissing {
		if stale, serr := l.containerImageStale(ctx, manifest); serr == nil && stale {
			slog.Warn("container image is stale, recreating", "workspace", manifest.Name, "container", manifest.ContainerName)
			if err := l.driver.RemoveContainer(ctx, manifest.ContainerName); err != nil {
				return runtime.StatusUnknown, runtime.ContainerSpec{}, err
			}
			status = runtime.StatusMissing
		}
	}

	if status == runtime.StatusMissing {
		slog.Info("creating workspace container", "workspace", manifest.Name, "container", manifest.ContainerName, "image", manifest.ImageName)
		if err := l.driver.CreateContainer(ctx, spec); err != nil {
			return runtime.StatusUnknown, runtime.ContainerSpec{}, err
		}
		status = runtime.StatusCreated
	}

	return status, spec, nil
}

// openCodeHomeDir is the workspace user's home directory inside the container.
const openCodeHomeDir = "/home/debian"

// openCodeConfigDir is where OpenCode reads its global configuration inside the
// container.
const openCodeConfigDir = openCodeHomeDir + "/.config/opencode"

const openCodeAuthRelPath = ".local/share/opencode/auth.json"

// openCodeMounts returns the read-only bind mounts that expose the global
// OpenCode templates (~/.config/opencode-manager) inside the workspace at
// /home/debian/.config/opencode. Only the single-file templates (AGENTS.md and
// opencode.json) are mounted live; editing one propagates to every workspace.
//
// The asset directories (agents/, commands/, plugins/, skills/) are NOT mounted:
// they are seeded into the workspace home so module install scripts can write
// OpenCode commands/skills/agents/plugins into them (see
// ensureWorkspaceOpenCodeAssets). They are then workspace-owned, writable, and
// persist across container recreation via the bind-mounted home.
func openCodeMounts(useLocalAuth bool) ([]runtime.Mount, error) {
	dir, err := config.GlobalDir()
	if err != nil {
		return nil, err
	}

	names := []string{"AGENTS.md", "opencode.json"}
	mounts := make([]runtime.Mount, 0, len(names)+1)
	for _, name := range names {
		mounts = append(mounts, runtime.Mount{
			Source:   filepath.Join(dir, name),
			Target:   openCodeConfigDir + "/" + name,
			ReadOnly: true,
		})
	}
	if useLocalAuth {
		source, err := localOpenCodeAuthPath()
		if err != nil {
			return nil, err
		}
		if err := validateOpenCodeAuthFile(source); err != nil {
			return nil, err
		}
		mounts = append(mounts, runtime.Mount{
			Source:   source,
			Target:   openCodeHomeDir + "/" + openCodeAuthRelPath,
			ReadOnly: false,
		})
	}

	return mounts, nil
}

// ensureWorkspaceOpenCodeAssets seeds the OpenCode asset directories (agents/,
// commands/, plugins/, skills/) into the workspace home from the global
// templates the first time, and always refreshes the manager-owned status
// plugin. Existing directories are left intact so workspace-level edits and
// module-written assets are preserved.
func ensureWorkspaceOpenCodeAssets(homeDir string) error {
	globalDir, err := config.GlobalDir()
	if err != nil {
		return err
	}

	base := filepath.Join(homeDir, ".config", "opencode")
	for _, name := range config.GlobalTemplateDirs {
		dst := filepath.Join(base, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("check workspace OpenCode asset directory %q: %w", dst, err)
		}
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return fmt.Errorf("create workspace OpenCode asset directory %q: %w", dst, err)
		}
		if err := copyDirContents(filepath.Join(globalDir, name), dst); err != nil {
			return fmt.Errorf("seed workspace OpenCode asset directory %q: %w", dst, err)
		}
	}

	return EnsureWorkspaceStatusPlugin(base)
}

// copyDirContents recursively copies the contents of src into dst. A missing
// src is treated as empty.
func copyDirContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read directory %q: %w", src, err)
	}

	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(d, 0o700); err != nil {
				return err
			}
			if err := copyDirContents(s, d); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		if err := os.WriteFile(d, data, 0o600); err != nil {
			return err
		}
	}

	return nil
}

func localOpenCodeAuthPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home directory: %w", err)
	}

	return filepath.Join(homeDir, openCodeAuthRelPath), nil
}

func validateOpenCodeAuthFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("local OpenCode auth file %q is required when useLocalOpenCodeAuth is true", path)
		}
		return fmt.Errorf("check local OpenCode auth file %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("local OpenCode auth path %q is a directory", path)
	}

	return nil
}

func (l Lifecycle) recreateAndStart(ctx context.Context, summary Summary, spec runtime.ContainerSpec) error {
	if err := l.driver.RemoveContainer(ctx, summary.Manifest.ContainerName); err != nil {
		return err
	}
	if err := l.driver.CreateContainer(ctx, spec); err != nil {
		return err
	}
	return l.driver.StartContainer(ctx, summary.Manifest.ContainerName)
}

func (l Lifecycle) Stop(ctx context.Context, summary Summary) error {
	name := summary.Manifest.ContainerName
	slog.Info("stopping workspace", "workspace", summary.Manifest.Name, "container", name)

	status, err := l.driver.ContainerStatus(ctx, name)
	if err != nil {
		return err
	}
	if status == runtime.StatusMissing {
		return fmt.Errorf("container %s does not exist", name)
	}
	if status != runtime.StatusRunning {
		slog.Debug("workspace container not running, nothing to stop", "workspace", summary.Manifest.Name, "container", name, "status", status)
		return nil
	}

	return l.driver.StopContainer(ctx, name)
}

// UpdateOpenCode upgrades OpenCode to the latest npm release inside the
// workspace container and restarts it so the new binary becomes the running
// server. It returns the resulting OpenCode version.
//
// OpenCode must be idle: the TUI only invokes this when no task is running, so a
// restart cannot interrupt active work. OpenCode is installed globally under
// /usr/local (owned by root), so the upgrade runs as root even though the
// container's main process is the unprivileged workspace user. A stop/start
// restart preserves the container's writable layer, so the freshly installed
// package survives and the persistent `opencode serve` process reloads it.
func (l Lifecycle) UpdateOpenCode(ctx context.Context, summary Summary) (string, error) {
	name := summary.Manifest.ContainerName
	slog.Info("updating OpenCode in workspace", "workspace", summary.Manifest.Name, "container", name)

	// The container must be running to exec the upgrade into it.
	if err := l.EnsureStarted(ctx, summary); err != nil {
		return "", err
	}

	if _, err := l.driver.ExecOutputAs(ctx, name, "0", []string{"npm", "install", "-g", "opencode-ai@latest"}); err != nil {
		return "", fmt.Errorf("update OpenCode: %w", err)
	}

	version, err := l.openCodeVersion(ctx, name)
	if err != nil {
		return "", err
	}

	slog.Debug("restarting container after OpenCode update", "workspace", summary.Manifest.Name, "container", name, "version", version)
	if err := l.driver.StopContainer(ctx, name); err != nil {
		return "", fmt.Errorf("restart after update: stop container: %w", err)
	}
	if err := l.driver.StartContainer(ctx, name); err != nil {
		return "", fmt.Errorf("restart after update: start container: %w", err)
	}

	slog.Info("OpenCode updated in workspace", "workspace", summary.Manifest.Name, "container", name, "version", version)
	return version, nil
}

// OpenCodeVersion returns the OpenCode version installed in the workspace's
// running container, as reported by `opencode --version`.
func (l Lifecycle) OpenCodeVersion(ctx context.Context, summary Summary) (string, error) {
	return l.openCodeVersion(ctx, summary.Manifest.ContainerName)
}

// openCodeVersion returns the OpenCode version installed in the running
// container, as reported by `opencode --version`.
func (l Lifecycle) openCodeVersion(ctx context.Context, containerName string) (string, error) {
	output, err := l.driver.ExecOutput(ctx, containerName, []string{"opencode", "--version"})
	if err != nil {
		return "", fmt.Errorf("read OpenCode version: %w", err)
	}

	version := strings.TrimSpace(string(output))
	if i := strings.IndexByte(version, '\n'); i >= 0 {
		version = strings.TrimSpace(version[:i])
	}
	if version == "" {
		return "unknown", nil
	}

	return version, nil
}

func (l Lifecycle) Delete(ctx context.Context, summary Summary) error {
	slog.Info("deleting workspace", "workspace", summary.Manifest.Name, "container", summary.Manifest.ContainerName, "image", summary.Manifest.ImageName)

	if err := l.driver.RemoveContainer(ctx, summary.Manifest.ContainerName); err != nil {
		return err
	}
	if err := l.driver.RemoveImage(ctx, summary.Manifest.ImageName); err != nil {
		return err
	}
	if err := l.registry.Delete(summary); err != nil {
		return err
	}

	slog.Info("workspace deleted", "workspace", summary.Manifest.Name)
	return nil
}

func (l Lifecycle) AttachCommand(ctx context.Context, summary Summary) (*exec.Cmd, error) {
	slog.Info("attaching to workspace", "workspace", summary.Manifest.Name, "container", summary.Manifest.ContainerName)
	if err := l.ensureCurrentRunning(ctx, summary); err != nil {
		return nil, err
	}

	return l.driver.ExecCommand(summary.Manifest.ContainerName, openCodeSessionCommand()), nil
}

// ensureCurrentRunning makes sure the workspace container is running and built
// from the current image before we exec into it. The common hot path (already
// running and current) avoids the image build that EnsureStarted performs.
func (l Lifecycle) ensureCurrentRunning(ctx context.Context, summary Summary) error {
	status, err := l.driver.ContainerStatus(ctx, summary.Manifest.ContainerName)
	if err != nil {
		return err
	}
	if status == runtime.StatusRunning {
		if stale, serr := l.containerImageStale(ctx, summary.Manifest); serr == nil && !stale {
			return nil
		}
	}

	return l.EnsureStarted(ctx, summary)
}

// containerImageStale reports whether the container was created from a different
// image than the one currently tagged for the workspace, meaning it should be
// recreated. Missing IDs are treated as not stale to avoid spurious recreation.
func (l Lifecycle) containerImageStale(ctx context.Context, manifest Manifest) (bool, error) {
	containerImage, err := l.driver.ContainerImageID(ctx, manifest.ContainerName)
	if err != nil {
		return false, err
	}
	currentImage, err := l.driver.ImageID(ctx, manifest.ImageName)
	if err != nil {
		return false, err
	}
	if containerImage == "" || currentImage == "" {
		return false, nil
	}

	stale := containerImage != currentImage
	if stale {
		slog.Debug("container image differs from current workspace image", "workspace", manifest.Name, "containerImage", containerImage, "currentImage", currentImage)
	}
	return stale, nil
}

func (l Lifecycle) Attach(ctx context.Context, summary Summary) (tea.Cmd, error) {
	cmd, err := l.AttachCommand(ctx, summary)
	if err != nil {
		return nil, err
	}

	return tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		// Use a fresh context: the caller's ctx is already cancelled by the time
		// the attached process exits.
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		status, _ := l.driver.ContainerStatus(bg, summary.Manifest.ContainerName)
		return AttachResultMsg{Err: execErr, StillRunning: status == runtime.StatusRunning}
	}), nil
}

// ShellCommand ensures the workspace container is running and returns a command
// that opens an interactive shell inside it.
func (l Lifecycle) ShellCommand(ctx context.Context, summary Summary) (*exec.Cmd, error) {
	slog.Info("opening shell in workspace", "workspace", summary.Manifest.Name, "container", summary.Manifest.ContainerName)
	if err := l.EnsureStarted(ctx, summary); err != nil {
		return nil, err
	}

	return l.driver.ExecCommand(summary.Manifest.ContainerName, []string{"/bin/bash"}), nil
}

func (l Lifecycle) Shell(ctx context.Context, summary Summary) (tea.Cmd, error) {
	cmd, err := l.ShellCommand(ctx, summary)
	if err != nil {
		return nil, err
	}

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return ShellResultMsg{Err: err}
	}), nil
}

// openCodeServeCommand is the container's main process: the supervisor
// entrypoint, which sources the per-workspace ~/.env and then runs a persistent,
// headless OpenCode server. The server keeps the container alive, runs the agent
// (so work continues while no client is attached), and loads the status-reporter
// plugin so the dashboard reflects activity server-side. Each workspace container
// is network-isolated, so binding 127.0.0.1:4096 is private to that container.
// Sourcing ~/.env lets module-provided environment variables reach the server
// and the tools it spawns; bouncing only the server child reloads them without
// recreating the container.
func openCodeServeCommand() []string {
	return []string{runtime.EntrypointPath}
}

// openCodeSessionCommand runs a TUI client that attaches to the container's
// OpenCode server over HTTP. Because the client is a separate process from the
// server, Ctrl-C exits only the client and the server keeps running in the
// background; re-attaching reconnects to the same live session. The client draws
// directly to the real terminal, so there is no multiplexer or nested-terminal
// issue. The wrapper script waits for the server and picks --continue when a
// session already exists.
func openCodeSessionCommand() []string {
	return []string{"/usr/local/bin/opencode-manager-attach"}
}

// TokenUsage is a synthesis of OpenCode token usage for a workspace, as
// reported by tokscale running inside the workspace container.
type TokenUsage struct {
	TotalTokens int64
	TotalCost   float64
	TotalMsgs   int
	TodayTokens int64
	TodayCost   float64
	TodayMsgs   int
}

type tokscaleEntry struct {
	Input        int64   `json:"input"`
	Output       int64   `json:"output"`
	CacheRead    int64   `json:"cacheRead"`
	CacheWrite   int64   `json:"cacheWrite"`
	Reasoning    int64   `json:"reasoning"`
	MessageCount int     `json:"messageCount"`
	Cost         float64 `json:"cost"`
}

type tokscaleReport struct {
	Entries []tokscaleEntry `json:"entries"`
}

type tokscaleAggregate struct {
	tokens int64
	cost   float64
	msgs   int
}

// TokenUsage runs tokscale inside the workspace container to summarize OpenCode
// token usage, both all-time and for the current day. The container must be
// running.
func (l Lifecycle) TokenUsage(ctx context.Context, summary Summary) (TokenUsage, error) {
	containerName := summary.Manifest.ContainerName

	total, err := l.runTokscale(ctx, containerName, nil)
	if err != nil {
		return TokenUsage{}, err
	}

	today, err := l.runTokscale(ctx, containerName, []string{"--today"})
	if err != nil {
		return TokenUsage{}, err
	}

	return TokenUsage{
		TotalTokens: total.tokens,
		TotalCost:   total.cost,
		TotalMsgs:   total.msgs,
		TodayTokens: today.tokens,
		TodayCost:   today.cost,
		TodayMsgs:   today.msgs,
	}, nil
}

func (l Lifecycle) runTokscale(ctx context.Context, containerName string, extra []string) (tokscaleAggregate, error) {
	args := append([]string{"tokscale", "--json", "--client", "opencode"}, extra...)
	output, err := l.driver.ExecOutput(ctx, containerName, args)
	if err != nil {
		return tokscaleAggregate{}, err
	}

	var report tokscaleReport
	if err := json.Unmarshal(output, &report); err != nil {
		return tokscaleAggregate{}, fmt.Errorf("parse tokscale output: %w", err)
	}

	var agg tokscaleAggregate
	for _, entry := range report.Entries {
		agg.tokens += entry.Input + entry.Output + entry.CacheRead + entry.CacheWrite + entry.Reasoning
		agg.cost += entry.Cost
		agg.msgs += entry.MessageCount
	}

	return agg, nil
}

func imageConfigFromConfig(cfg config.Config) ImageConfig {
	return ImageConfig{
		BaseImage: cfg.BaseImage.Name,
		Packages:  append([]string(nil), cfg.BaseImage.Packages...),
		Commands:  append([]string(nil), cfg.BaseImage.Commands...),
	}
}

// baseImageRevision is bumped whenever the managed base image's build recipe
// changes (independently of the user's baseImage config) so existing cached base
// images are invalidated and rebuilt. Bump this when editing the base
// Containerfile (e.g. adding packages, the attach wrapper, or the OpenCode
// install method).
//
// Revision 7: added the supervisor entrypoint (sources ~/.env, reloadable
// server), passwordless sudo for the sudo group, and the interactive-shell env
// hook, for the module system.
//
// Revision 8: added the openssh-client package so SSH remotes (e.g. ssh-style
// git clone URLs) work without a per-module install.
const baseImageRevision = 8

func managedBaseImageName(image ImageConfig) (string, error) {
	payload := struct {
		Rev   int         `json:"rev"`
		Image ImageConfig `json:"image"`
	}{Rev: baseImageRevision, Image: image}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode base image definition: %w", err)
	}

	sum := sha256.Sum256(data)
	return "opencode-manager/base:" + hex.EncodeToString(sum[:])[:16], nil
}

func currentUserIDs() (int, int, error) {
	uid := os.Getuid()
	gid := os.Getgid()
	if uid <= 0 || gid <= 0 {
		return 0, 0, fmt.Errorf("current uid/gid must be positive, got %d:%d", uid, gid)
	}

	return uid, gid, nil
}

func lifecycleContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}
