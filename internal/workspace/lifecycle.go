package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	Error     string
}

type AttachResultMsg struct {
	Err error
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
		statuses = append(statuses, status)
	}

	return statuses
}

func (l Lifecycle) EnsureStarted(ctx context.Context, summary Summary) error {
	status, spec, err := l.provision(ctx, summary)
	if err != nil {
		return err
	}

	if status != runtime.StatusRunning {
		if err := l.driver.StartContainer(ctx, summary.Manifest.ContainerName); err != nil {
			if recreateErr := l.recreateAndStart(ctx, summary, spec); recreateErr != nil {
				return fmt.Errorf("start failed: %w; recreate failed: %v", err, recreateErr)
			}
		}
	}

	return nil
}

func (l Lifecycle) Provision(ctx context.Context, summary Summary) (string, error) {
	status, _, err := l.provision(ctx, summary)
	return status, err
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

	mounts, err := globalTemplateMounts()
	if err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	spec := runtime.ContainerSpec{
		Name:      manifest.ContainerName,
		ImageName: manifest.ImageName,
		HomeDir:   manifest.HomeDir,
		UID:       uid,
		GID:       gid,
		Env:       manifest.Env,
		Mounts:    mounts,
		Command:   interactiveOpenCodeCommand(),
	}

	status, err := l.driver.ContainerStatus(ctx, manifest.ContainerName)
	if err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	if status == runtime.StatusMissing {
		if err := l.driver.CreateContainer(ctx, spec); err != nil {
			return runtime.StatusUnknown, runtime.ContainerSpec{}, err
		}
		status = runtime.StatusCreated
	}

	return status, spec, nil
}

// openCodeConfigDir is where OpenCode reads its global configuration inside the
// container. The workspace home directory is mounted at /home/debian.
const openCodeConfigDir = "/home/debian/.config/opencode"

// globalTemplateMounts returns the read-only bind mounts that expose the global
// OpenCode templates (~/.config/opencode-manager) inside the workspace at
// /home/debian/.config/opencode. Editing a host template propagates live to
// every workspace; adding or removing a template takes effect on the next
// container (re)creation.
func globalTemplateMounts() ([]runtime.Mount, error) {
	dir, err := config.GlobalDir()
	if err != nil {
		return nil, err
	}

	names := append([]string{"AGENTS.md", "opencode.json"}, config.GlobalTemplateDirs...)
	mounts := make([]runtime.Mount, 0, len(names))
	for _, name := range names {
		mounts = append(mounts, runtime.Mount{
			Source:   filepath.Join(dir, name),
			Target:   openCodeConfigDir + "/" + name,
			ReadOnly: true,
		})
	}

	return mounts, nil
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
	status, err := l.driver.ContainerStatus(ctx, summary.Manifest.ContainerName)
	if err != nil {
		return err
	}
	if status == runtime.StatusMissing {
		return fmt.Errorf("container %s does not exist", summary.Manifest.ContainerName)
	}
	if status != runtime.StatusRunning {
		return nil
	}

	return l.driver.StopContainer(ctx, summary.Manifest.ContainerName)
}

func (l Lifecycle) Delete(ctx context.Context, summary Summary) error {
	if err := l.driver.RemoveContainer(ctx, summary.Manifest.ContainerName); err != nil {
		return err
	}
	if err := l.driver.RemoveImage(ctx, summary.Manifest.ImageName); err != nil {
		return err
	}
	if err := l.registry.Delete(summary); err != nil {
		return err
	}

	return nil
}

func (l Lifecycle) AttachCommand(ctx context.Context, summary Summary) (*exec.Cmd, error) {
	status, err := l.driver.ContainerStatus(ctx, summary.Manifest.ContainerName)
	if err != nil {
		return nil, err
	}
	if shouldStartForAttach(status) {
		if err := l.EnsureStarted(ctx, summary); err != nil {
			return nil, err
		}
	}

	return l.driver.AttachCommand(summary.Manifest.ContainerName), nil
}

func (l Lifecycle) Attach(ctx context.Context, summary Summary) (tea.Cmd, error) {
	cmd, err := l.AttachCommand(ctx, summary)
	if err != nil {
		return nil, err
	}

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return AttachResultMsg{Err: err}
	}), nil
}

// ShellCommand ensures the workspace container is running and returns a command
// that opens an interactive shell inside it.
func (l Lifecycle) ShellCommand(ctx context.Context, summary Summary) (*exec.Cmd, error) {
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

func shouldStartForAttach(status string) bool {
	return status != runtime.StatusRunning
}

func interactiveOpenCodeCommand() []string {
	return []string{"/usr/local/bin/opencode-manager-entrypoint"}
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

func managedBaseImageName(image ImageConfig) (string, error) {
	data, err := json.Marshal(image)
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
