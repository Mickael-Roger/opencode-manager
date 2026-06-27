package runtime

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mickael-menu/opencode-manager/internal/config"
)

const (
	StatusMissing = "missing"
	StatusCreated = "created"
	StatusRunning = "running"
	StatusExited  = "exited"
	StatusUnknown = "unknown"
)

// attachScriptName is the file name of the attach wrapper, both within the build
// context and at /usr/local/bin inside the image.
const attachScriptName = "opencode-manager-attach"

// EntrypointPath is the supervisor entrypoint inside the image; it is the
// container's main process (see entrypointScript).
const EntrypointPath = "/usr/local/bin/" + entrypointScriptName

// attachScript is the attach wrapper copied into the base image. The container's
// main process is a persistent `opencode serve`, and attaching runs a TUI client
// (`opencode attach`) against it over HTTP. Ctrl-C then exits only the client;
// the server keeps running in the background. The wrapper waits for the server to
// be listening, then attaches to the last session if one exists (asking the
// server over HTTP, so no second opencode process is spawned), otherwise starts a
// fresh session.
//
// It is shipped as a build-context file copied with COPY rather than written from
// a heredoc: the buildah builder used by Podman does not support heredocs
// (`<<'EOF'`) in Containerfiles (a BuildKit-only feature), and parses the script
// body as further Containerfile instructions.
const attachScript = `#!/bin/sh
url="http://127.0.0.1:4096"
dir="/home/debian/workspace"
i=0
while [ "$i" -lt 150 ]; do
  curl -sf -o /dev/null "$url/session" && break
  i=$((i+1)); sleep 0.2
done
count=$(curl -sf "$url/session" | jq 'length' 2>/dev/null || echo 0)
if [ "${count:-0}" -gt 0 ] 2>/dev/null; then
  exec opencode attach "$url" --dir "$dir" -c
fi
exec opencode attach "$url" --dir "$dir"
`

// entrypointScriptName is the file name of the supervisor entrypoint, both in
// the build context and at /usr/local/bin inside the image.
const entrypointScriptName = "opencode-manager-entrypoint"

// entrypointScript is the container's main process (PID 1). It sources the
// per-workspace ~/.env before launching the OpenCode server so module-provided
// environment variables reach the server process (and every tool it spawns).
//
// The server runs as a background child; killing only that child (for example
// `pkill -f 'opencode serve'`, which the manager does after a module edits
// ~/.env) makes the loop re-source ~/.env and relaunch the server, so an env
// change needs only a cheap in-place server bounce, not a container recreate.
// A SIGTERM from `docker stop` is trapped and forwarded to the child so the
// container still stops cleanly.
//
// Like the attach wrapper it is shipped as a COPYed build-context file rather
// than a heredoc, which the buildah builder used by Podman does not support.
const entrypointScript = `#!/bin/sh
child=""
shutdown() {
  [ -n "$child" ] && kill -TERM "$child" 2>/dev/null
  exit 0
}
trap shutdown TERM INT
while true; do
  set -a
  [ -f "$HOME/.env" ] && . "$HOME/.env"
  set +a
  opencode serve --hostname 127.0.0.1 --port 4096 &
  child=$!
  wait "$child"
  child=""
  sleep 0.5
done
`

type Driver interface {
	Name() string
	Available(context.Context) error
	PullImage(context.Context, string) error
	BuildBaseImage(context.Context, BaseBuildSpec) error
	BuildImage(context.Context, BuildSpec) error
	ContainerStatus(context.Context, string) (string, error)
	ContainerImageID(context.Context, string) (string, error)
	ImageID(context.Context, string) (string, error)
	CreateContainer(context.Context, ContainerSpec) error
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string) error
	RemoveContainer(context.Context, string) error
	RemoveImage(context.Context, string) error
	ExecCommand(string, []string) *exec.Cmd
	ExecOutput(context.Context, string, []string) ([]byte, error)
	ExecOutputAs(context.Context, string, string, []string) ([]byte, error)
	Exec(context.Context, ExecSpec) ([]byte, error)
}

// ExecSpec runs a command inside a running container as a specific user and
// with extra environment variables. It is used to run module install/uninstall
// scripts (as the workspace user, which has passwordless sudo) with their
// prompt values passed as OCM_* variables.
type ExecSpec struct {
	Container string
	// User is the container user to run as ("" keeps the container's default
	// user, "0" runs as root). Module scripts run as the default workspace user.
	User string
	Env  map[string]string
	Args []string
}

type BaseBuildSpec struct {
	ImageName string
	FromImage string
	Packages  []string
	Commands  []string
	// Prebuilt selects the build recipe. When false (the default), FromImage is a
	// plain distro (e.g. debian:stable-slim) and the full base recipe is rendered:
	// system packages, uv, linuxbrew, OpenCode, tokscale, and the manager scripts.
	// This is what publishes docker.io/mroger78/ocm-base. When true, FromImage is
	// already a built ocm-base, so only a thin overlay is rendered that adds the
	// user's extra Packages/Commands on top.
	Prebuilt bool
}

type BuildSpec struct {
	ImageName string
	BaseImage string
	UID       int
	GID       int
}

type ContainerSpec struct {
	Name      string
	ImageName string
	HomeDir   string
	UID       int
	GID       int
	Env       map[string]string
	Mounts    []Mount
	Command   []string
}

// Mount is an additional bind mount layered on top of the workspace home
// directory, e.g. the read-only global OpenCode templates.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

type CLIDriver struct {
	binary string
}

func NewDriver(runtimeName string) (Driver, error) {
	switch runtimeName {
	case config.RuntimeDocker, config.RuntimePodman:
		return CLIDriver{binary: runtimeName}, nil
	default:
		return nil, fmt.Errorf("unsupported runtime %q", runtimeName)
	}
}

func (d CLIDriver) Name() string {
	return d.binary
}

func (d CLIDriver) Available(ctx context.Context) error {
	if _, err := exec.LookPath(d.binary); err != nil {
		slog.Debug("runtime executable not found", "runtime", d.binary, "error", err)
		return fmt.Errorf("%s executable not found: %w", d.binary, err)
	}

	cmd := exec.CommandContext(ctx, d.binary, "version")
	if output, err := cmd.CombinedOutput(); err != nil {
		slog.Debug("runtime not available", "runtime", d.binary, "error", err, "output", strings.TrimSpace(string(output)))
		return fmt.Errorf("%s is not available: %w: %s", d.binary, err, string(output))
	}

	slog.Debug("runtime available", "runtime", d.binary)
	return nil
}

func (d CLIDriver) BuildBaseImage(ctx context.Context, spec BaseBuildSpec) error {
	if spec.ImageName == "" {
		return fmt.Errorf("base image name is required")
	}
	if spec.FromImage == "" {
		return fmt.Errorf("base image source is required")
	}

	exists, err := d.imageExists(ctx, spec.ImageName)
	if err != nil {
		return err
	}
	if exists {
		slog.Debug("base image already exists, skipping build", "image", spec.ImageName)
		return nil
	}

	slog.Info("building base image", "image", spec.ImageName, "from", spec.FromImage, "prebuilt", spec.Prebuilt, "packages", spec.Packages)

	dir, err := os.MkdirTemp("", "opencode-manager-base-image-*")
	if err != nil {
		return fmt.Errorf("create temporary base build context: %w", err)
	}
	defer os.RemoveAll(dir)

	containerfile, err := WriteBaseBuildContext(dir, spec)
	if err != nil {
		return err
	}

	if err := d.run(ctx, "build", "-t", spec.ImageName, "-f", containerfile, dir); err != nil {
		return err
	}

	slog.Info("base image built", "image", spec.ImageName)
	return nil
}

// PullImage fetches an image from a registry. It is used to obtain the published
// base image (docker.io/mroger78/ocm-base) instead of building it locally.
func (d CLIDriver) PullImage(ctx context.Context, ref string) error {
	if ref == "" {
		return fmt.Errorf("image reference is required")
	}
	slog.Info("pulling image", "image", ref)
	return d.run(ctx, "pull", ref)
}

// WriteBaseBuildContext writes the Containerfile and the manager scripts that
// make up a base-image build context into dir and returns the Containerfile
// path. It is the single source of truth for the base recipe, shared by the
// runtime (BuildBaseImage) and the CI helper (cmd/ocm-base-context) that
// publishes docker.io/mroger78/ocm-base, so the two never drift.
func WriteBaseBuildContext(dir string, spec BaseBuildSpec) (string, error) {
	var contents string
	if spec.Prebuilt {
		contents = renderOverlayContainerfile(spec)
	} else {
		contents = renderBaseContainerfile(spec)
	}

	containerfile := filepath.Join(dir, "Containerfile")
	if err := os.WriteFile(containerfile, []byte(contents), 0o600); err != nil {
		return "", fmt.Errorf("write base Containerfile: %w", err)
	}

	// The thin overlay inherits the manager scripts from the prebuilt base, so it
	// does not COPY them and the context needs only the Containerfile.
	if spec.Prebuilt {
		return containerfile, nil
	}

	attachPath := filepath.Join(dir, attachScriptName)
	if err := os.WriteFile(attachPath, []byte(attachScript), 0o755); err != nil {
		return "", fmt.Errorf("write attach script: %w", err)
	}

	entrypointPath := filepath.Join(dir, entrypointScriptName)
	if err := os.WriteFile(entrypointPath, []byte(entrypointScript), 0o755); err != nil {
		return "", fmt.Errorf("write entrypoint script: %w", err)
	}

	return containerfile, nil
}

func (d CLIDriver) BuildImage(ctx context.Context, spec BuildSpec) error {
	if spec.ImageName == "" {
		return fmt.Errorf("image name is required")
	}
	if spec.BaseImage == "" {
		return fmt.Errorf("image base image is required")
	}
	if spec.UID <= 0 || spec.GID <= 0 {
		return fmt.Errorf("valid uid and gid are required")
	}

	slog.Info("building workspace image", "image", spec.ImageName, "base", spec.BaseImage, "uid", spec.UID, "gid", spec.GID)

	dir, err := os.MkdirTemp("", "opencode-manager-image-*")
	if err != nil {
		return fmt.Errorf("create temporary build context: %w", err)
	}
	defer os.RemoveAll(dir)

	containerfile := filepath.Join(dir, "Containerfile")
	if err := os.WriteFile(containerfile, []byte(renderWorkspaceContainerfile(spec)), 0o600); err != nil {
		return fmt.Errorf("write Containerfile: %w", err)
	}

	args := []string{
		"build",
		"-t", spec.ImageName,
		"--build-arg", "UID=" + strconv.Itoa(spec.UID),
		"--build-arg", "GID=" + strconv.Itoa(spec.GID),
		"-f", containerfile,
		dir,
	}
	if err := d.run(ctx, args...); err != nil {
		return err
	}

	slog.Info("workspace image built", "image", spec.ImageName)
	return nil
}

func (d CLIDriver) ContainerStatus(ctx context.Context, name string) (string, error) {
	if name == "" {
		return StatusMissing, fmt.Errorf("container name is required")
	}

	cmd := exec.CommandContext(ctx, d.binary, "inspect", "-f", "{{.State.Status}}", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if isMissingResourceOutput(output) {
			slog.Debug("container status", "container", name, "status", StatusMissing)
			return StatusMissing, nil
		}

		return StatusUnknown, fmt.Errorf("inspect container %q: %w: %s", name, err, strings.TrimSpace(string(output)))
	}

	status := strings.TrimSpace(string(output))
	if status == "" {
		return StatusUnknown, nil
	}

	slog.Debug("container status", "container", name, "status", status)
	return status, nil
}

func (d CLIDriver) CreateContainer(ctx context.Context, spec ContainerSpec) error {
	if spec.Name == "" || spec.ImageName == "" || spec.HomeDir == "" {
		return fmt.Errorf("container name, image name, and home directory are required")
	}
	if spec.UID <= 0 || spec.GID <= 0 {
		return fmt.Errorf("valid uid and gid are required")
	}
	if len(spec.Command) == 0 {
		return fmt.Errorf("container command is required")
	}

	return d.run(ctx, createArgs(d.binary, spec)...)
}

// createArgs builds the runtime CLI arguments for creating a workspace
// container. The workspace home directory is mounted read-write, and any extra
// spec.Mounts (e.g. the global OpenCode templates) are layered on top.
//
// The binary selects the user-namespace strategy. Under rootless Podman the
// invoking host user maps to container root, so a `--user UID:GID` process
// (a sub-uid) cannot write to the bind-mounted home, which is owned by the host
// UID. `--userns=keep-id` maps the host UID/GID 1:1 into the container so the
// process owns its home. Docker's default namespace already preserves the
// numeric bind-mount ownership for `--user`, and it does not support keep-id,
// so the flag is Podman-only.
func createArgs(binary string, spec ContainerSpec) []string {
	args := []string{
		"create",
		"--interactive",
		"--tty",
		"--name", spec.Name,
		"--user", fmt.Sprintf("%d:%d", spec.UID, spec.GID),
		"--workdir", "/home/debian/workspace",
		"--env", "HOME=/home/debian",
		"--env", "TERM=xterm-256color",
		"--volume", spec.HomeDir + ":/home/debian",
	}

	if binary == "podman" {
		args = append(args, "--userns", "keep-id")
	}

	for _, mount := range spec.Mounts {
		volume := mount.Source + ":" + mount.Target
		if mount.ReadOnly {
			volume += ":ro"
		}
		args = append(args, "--volume", volume)
	}

	for key, value := range spec.Env {
		args = append(args, "--env", key+"="+value)
	}

	args = append(args, spec.ImageName)
	args = append(args, spec.Command...)

	return args
}

func (d CLIDriver) StartContainer(ctx context.Context, name string) error {
	return d.run(ctx, "start", name)
}

func (d CLIDriver) StopContainer(ctx context.Context, name string) error {
	return d.run(ctx, "stop", name)
}

func (d CLIDriver) RemoveContainer(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("container name is required")
	}

	return d.runAllowMissing(ctx, []string{"rm", "-f", name}, "container")
}

func (d CLIDriver) RemoveImage(ctx context.Context, imageName string) error {
	if imageName == "" {
		return fmt.Errorf("image name is required")
	}

	return d.runAllowMissing(ctx, []string{"rmi", imageName}, "image")
}

// ContainerImageID returns the image ID a container was created from, or an
// empty string if the container does not exist. It is used to detect when the
// workspace image has been rebuilt and the container needs recreating.
func (d CLIDriver) ContainerImageID(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("container name is required")
	}

	cmd := exec.CommandContext(ctx, d.binary, "inspect", "-f", "{{.Image}}", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if isMissingResourceOutput(output) {
			return "", nil
		}

		return "", fmt.Errorf("inspect container image %q: %w: %s", name, err, strings.TrimSpace(string(output)))
	}

	return strings.TrimSpace(string(output)), nil
}

// ImageID returns the current ID of an image, or an empty string if it does not
// exist.
func (d CLIDriver) ImageID(ctx context.Context, imageName string) (string, error) {
	if imageName == "" {
		return "", fmt.Errorf("image name is required")
	}

	cmd := exec.CommandContext(ctx, d.binary, "image", "inspect", "-f", "{{.Id}}", imageName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if isMissingResourceOutput(output) {
			return "", nil
		}

		return "", fmt.Errorf("inspect image %q: %w: %s", imageName, err, strings.TrimSpace(string(output)))
	}

	return strings.TrimSpace(string(output)), nil
}

// ExecCommand runs a command inside an already-running container with an
// interactive TTY, e.g. an interactive shell.
func (d CLIDriver) ExecCommand(name string, command []string) *exec.Cmd {
	args := append([]string{"exec", "--interactive", "--tty", name}, command...)
	return exec.Command(d.binary, args...)
}

// ExecOutput runs a command inside a running container without a TTY and
// returns its standard output. Used to collect machine-readable output such as
// `tokscale --json`. It runs as the container's default user.
func (d CLIDriver) ExecOutput(ctx context.Context, name string, command []string) ([]byte, error) {
	return d.ExecOutputAs(ctx, name, "", command)
}

// ExecOutputAs is like ExecOutput but runs the command as the given container
// user (e.g. "0" for root). An empty user keeps the container's default user.
// Updating the globally installed OpenCode needs root because the container's
// main process runs as the unprivileged workspace user.
func (d CLIDriver) ExecOutputAs(ctx context.Context, name, user string, command []string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("container name is required")
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("exec command is required")
	}

	args := []string{"exec"}
	if user != "" {
		args = append(args, "--user", user)
	}
	args = append(args, name)
	args = append(args, command...)
	cmd := exec.CommandContext(ctx, d.binary, args...)

	slog.Debug("running container exec", "runtime", d.binary, "container", name, "user", user, "command", command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		slog.Debug("container exec failed", "runtime", d.binary, "container", name, "command", command, "error", err, "stderr", strings.TrimSpace(stderr.String()))
		return nil, fmt.Errorf("%s exec %s: %w: %s", d.binary, name, err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

// Exec runs a command inside a running container as the requested user with
// extra environment variables and returns its combined output. Module scripts
// run through here; their output is surfaced to the user on failure.
func (d CLIDriver) Exec(ctx context.Context, spec ExecSpec) ([]byte, error) {
	if spec.Container == "" {
		return nil, fmt.Errorf("container name is required")
	}
	if len(spec.Args) == 0 {
		return nil, fmt.Errorf("exec command is required")
	}

	args := []string{"exec"}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	// Sort env keys so the command line is deterministic (eases logging/testing).
	keys := make([]string, 0, len(spec.Env))
	for key := range spec.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--env", key+"="+spec.Env[key])
	}
	args = append(args, spec.Container)
	args = append(args, spec.Args...)

	slog.Debug("running container exec", "runtime", d.binary, "container", spec.Container, "user", spec.User, "args", spec.Args)

	cmd := exec.CommandContext(ctx, d.binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Debug("container exec failed", "runtime", d.binary, "container", spec.Container, "args", spec.Args, "error", err, "output", strings.TrimSpace(string(output)))
		return output, fmt.Errorf("%s exec %s: %w: %s", d.binary, spec.Container, err, strings.TrimSpace(string(output)))
	}

	return output, nil
}

func (d CLIDriver) run(ctx context.Context, args ...string) error {
	slog.Debug("running runtime command", "runtime", d.binary, "args", args)
	cmd := exec.CommandContext(ctx, d.binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Debug("runtime command failed", "runtime", d.binary, "args", args, "error", err, "output", strings.TrimSpace(string(output)))
		return fmt.Errorf("%s %s: %w: %s", d.binary, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}

	slog.Debug("runtime command succeeded", "runtime", d.binary, "args", args)
	return nil
}

func (d CLIDriver) runAllowMissing(ctx context.Context, args []string, resource string) error {
	slog.Debug("running runtime command", "runtime", d.binary, "args", args)
	cmd := exec.CommandContext(ctx, d.binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if isMissingResourceOutput(output) {
			slog.Debug("runtime resource already missing, ignoring", "runtime", d.binary, "resource", resource, "args", args)
			return nil
		}

		slog.Debug("runtime command failed", "runtime", d.binary, "args", args, "error", err, "output", strings.TrimSpace(string(output)))
		return fmt.Errorf("remove %s: %s %s: %w: %s", resource, d.binary, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}

	slog.Debug("runtime command succeeded", "runtime", d.binary, "args", args)
	return nil
}

func (d CLIDriver) imageExists(ctx context.Context, imageName string) (bool, error) {
	cmd := exec.CommandContext(ctx, d.binary, "image", "inspect", imageName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if isMissingResourceOutput(output) {
			return false, nil
		}

		return false, fmt.Errorf("inspect image %q: %w: %s", imageName, err, strings.TrimSpace(string(output)))
	}

	return true, nil
}

func isMissingResourceOutput(output []byte) bool {
	text := strings.ToLower(string(output))
	return strings.Contains(text, "no such") ||
		strings.Contains(text, "not found") ||
		strings.Contains(text, "does not exist") ||
		strings.Contains(text, "image not known")
}

func renderBaseContainerfile(spec BaseBuildSpec) string {
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(spec.FromImage)
	b.WriteString("\n\n")
	b.WriteString("COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /usr/local/bin/\n\n")

	packages := append([]string{"bash", "build-essential", "ca-certificates", "curl", "file", "git", "jq", "nodejs", "npm", "openssh-client", "passwd", "procps", "ripgrep", "sudo"}, spec.Packages...)
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends ")
	b.WriteString(strings.Join(packages, " "))
	b.WriteString(" && rm -rf /var/lib/apt/lists/*\n")
	for _, command := range spec.Commands {
		b.WriteString("RUN ")
		b.WriteString(command)
		b.WriteString("\n")
	}
	// Create linuxbrew as a system account (UID/GID < 1000). Without an explicit
	// id, useradd grabs UID 1000, which collides with the host user the workspace
	// image installs at UID 1000: getent then resolves UID 1000 to linuxbrew, the
	// `debian` user is never created, and the pod runs as linuxbrew instead.
	//
	// Every install step below is idempotent (install only what is missing) so the
	// recipe can also be layered on a base that already provides some of these
	// tools without failing — e.g. re-creating the linuxbrew user would otherwise
	// crash with "user already exists".
	b.WriteString("RUN set -eux; getent group linuxbrew >/dev/null 2>&1 || groupadd -r linuxbrew; id -u linuxbrew >/dev/null 2>&1 || useradd -r -g linuxbrew -m -s /bin/bash linuxbrew; mkdir -p /home/linuxbrew/.linuxbrew/Homebrew /home/linuxbrew/.linuxbrew/bin; chown -R linuxbrew:linuxbrew /home/linuxbrew\n")
	b.WriteString("RUN set -eux; if [ ! -x /home/linuxbrew/.linuxbrew/bin/brew ]; then su linuxbrew -c 'git clone --depth=1 https://github.com/Homebrew/brew /home/linuxbrew/.linuxbrew/Homebrew && ln -s ../Homebrew/bin/brew /home/linuxbrew/.linuxbrew/bin/brew'; fi; su linuxbrew -c '/home/linuxbrew/.linuxbrew/bin/brew --version'\n")
	b.WriteString("RUN git --version && rg --version && jq --version && npx --version && uvx --version && su linuxbrew -c '/home/linuxbrew/.linuxbrew/bin/brew --version'\n")
	// Install OpenCode from npm rather than the curl|bash installer: the
	// installer resolves the version via the GitHub API, which is rate-limited
	// and flaky (504s), whereas the npm registry is reliable and already used
	// for tokscale below. Installed only when absent so the step is a no-op on a
	// base that already ships it.
	b.WriteString("RUN set -eux; command -v opencode >/dev/null 2>&1 || npm install -g opencode-ai; opencode --version\n")
	b.WriteString("RUN set -eux; command -v tokscale >/dev/null 2>&1 || npm install -g tokscale@latest; command -v tokscale\n")
	// Attach wrapper and supervisor entrypoint: copied from the build context
	// (see attachScript/entrypointScript) rather than written via a heredoc,
	// which the buildah builder used by Podman does not support in Containerfiles.
	b.WriteString("COPY " + attachScriptName + " /usr/local/bin/" + attachScriptName + "\n")
	b.WriteString("COPY " + entrypointScriptName + " /usr/local/bin/" + entrypointScriptName + "\n")
	b.WriteString("RUN chmod 0755 /usr/local/bin/" + attachScriptName + " /usr/local/bin/" + entrypointScriptName + "\n")
	// Passwordless sudo for the sudo group: module install scripts run as the
	// unprivileged workspace user (so files they write into the bind-mounted home
	// keep host ownership) but need root to install system packages. The
	// workspace user is added to the sudo group in the workspace image layer.
	b.WriteString("RUN echo '%sudo ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/opencode-manager && chmod 0440 /etc/sudoers.d/opencode-manager\n")
	// Source the per-workspace ~/.env from interactive shells (the `s`/shell
	// action) so they see module-provided variables, mirroring the server. Guarded
	// so re-layering the recipe does not append the line twice.
	b.WriteString("RUN grep -qF '[ -f \"$HOME/.env\" ]' /etc/bash.bashrc || echo '[ -f \"$HOME/.env\" ] && . \"$HOME/.env\"' >> /etc/bash.bashrc\n")
	b.WriteString("ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin\n")

	return b.String()
}

// renderOverlayContainerfile renders the thin overlay built on top of a prebuilt
// ocm-base when the user adds extra baseImage.packages/commands. The heavy layers
// (uv, linuxbrew, OpenCode, tokscale, scripts, sudo, PATH) are already in the base
// and are inherited, so only the extras are applied here.
func renderOverlayContainerfile(spec BaseBuildSpec) string {
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(spec.FromImage)
	b.WriteString("\n\n")
	if len(spec.Packages) > 0 {
		b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends ")
		b.WriteString(strings.Join(spec.Packages, " "))
		b.WriteString(" && rm -rf /var/lib/apt/lists/*\n")
	}
	for _, command := range spec.Commands {
		b.WriteString("RUN ")
		b.WriteString(command)
		b.WriteString("\n")
	}
	return b.String()
}

func renderWorkspaceContainerfile(spec BuildSpec) string {
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(spec.BaseImage)
	b.WriteString("\n\n")
	b.WriteString("ARG UID\n")
	b.WriteString("ARG GID\n\n")
	b.WriteString("RUN set -eux; ")
	b.WriteString("if getent group ${GID}; then group_name=$(getent group ${GID} | cut -d: -f1); else groupadd -g ${GID} debian && group_name=debian; fi; ")
	b.WriteString("if getent passwd ${UID}; then user_name=$(getent passwd ${UID} | cut -d: -f1); usermod -d /home/debian -s /bin/bash ${user_name}; else useradd -m -u ${UID} -g ${GID} -s /bin/bash debian; user_name=debian; fi; ")
	// Add the workspace user to the sudo group so module install scripts can use
	// passwordless sudo (configured in the base image) for system packages.
	b.WriteString("usermod -aG sudo ${user_name}; ")
	b.WriteString("mkdir -p /home/debian/workspace && chown -R ${UID}:${GID} /home/debian /home/linuxbrew/.linuxbrew\n")
	b.WriteString("ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin\n")
	b.WriteString("WORKDIR /home/debian/workspace\n")

	return b.String()
}
