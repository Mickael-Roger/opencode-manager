package runtime

import (
	"bytes"
	"context"
	"embed"
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

// buildContextFS holds the image build context — the Dockerfiles and the manager
// scripts — as real files under buildcontext/. They are the single source of
// truth for how images are built: the CI pipeline publishes buildcontext/Dockerfile
// directly, and the runtime materializes this directory to a temp dir and runs the
// container builder against it (see writeBuildContext).
//
//go:embed buildcontext
var buildContextFS embed.FS

// Build context file names.
const (
	baseDockerfile      = "Dockerfile"
	overlayDockerfile   = "Dockerfile.overlay"
	workspaceDockerfile = "Dockerfile.workspace"

	// attachScriptName / entrypointScriptName are the manager scripts, named the
	// same in the build context and at /usr/local/bin inside the image.
	attachScriptName     = "opencode-manager-attach"
	entrypointScriptName = "opencode-manager-entrypoint"
)

// EntrypointPath is the supervisor entrypoint inside the image; it is the
// container's main process.
const EntrypointPath = "/usr/local/bin/" + entrypointScriptName

// ContainerWorkspaceDir is the project working directory inside every workspace
// container (the home of the unprivileged workspace user). It is where exec'd
// commands and headless OpenCode runs operate, mirroring the WORKDIR baked into
// Dockerfile.workspace and the --workdir used for container creation.
const ContainerWorkspaceDir = "/home/debian/workspace"

// writeBuildContext materializes the embedded build context (Dockerfiles +
// scripts) into dir so the container builder can run against it. The binary ships
// these files embedded, so they must be written to disk before a build.
func writeBuildContext(dir string) error {
	entries, err := buildContextFS.ReadDir("buildcontext")
	if err != nil {
		return fmt.Errorf("read embedded build context: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := buildContextFS.ReadFile("buildcontext/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read embedded %q: %w", entry.Name(), err)
		}
		mode := os.FileMode(0o644)
		if entry.Name() == attachScriptName || entry.Name() == entrypointScriptName {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, mode); err != nil {
			return fmt.Errorf("write build context file %q: %w", entry.Name(), err)
		}
	}
	return nil
}

// baseBuildArgs returns the Dockerfile to use and the --build-arg pairs for a
// base-image build. A prebuilt base (extras layered on the published ocm-base)
// uses the thin overlay Dockerfile; any other base uses the full recipe. The
// FROM image and the user's extra packages/commands are passed as build args so
// the Dockerfiles stay static and directly buildable.
func baseBuildArgs(spec BaseBuildSpec) (string, []string) {
	dockerfile := baseDockerfile
	if spec.Prebuilt {
		dockerfile = overlayDockerfile
	}
	args := []string{"--build-arg", "BASE_IMAGE=" + spec.FromImage}
	if len(spec.Packages) > 0 {
		args = append(args, "--build-arg", "EXTRA_PACKAGES="+strings.Join(spec.Packages, " "))
	}
	if len(spec.Commands) > 0 {
		args = append(args, "--build-arg", "EXTRA_COMMANDS="+strings.Join(spec.Commands, " && "))
	}
	return dockerfile, args
}

// workspaceBuildArgs returns the --build-arg pairs for a per-workspace image
// build (the resolved base image plus the host UID/GID).
func workspaceBuildArgs(spec BuildSpec) []string {
	return []string{
		"--build-arg", "BASE_IMAGE=" + spec.BaseImage,
		"--build-arg", "UID=" + strconv.Itoa(spec.UID),
		"--build-arg", "GID=" + strconv.Itoa(spec.GID),
	}
}

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
	// system packages, uv, OpenCode, tokscale, and the manager scripts.
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
	// HostNetwork runs the container in the host's network namespace
	// (`--network host`) instead of an isolated one.
	HostNetwork bool
	// ExtraArgs are extra flags passed verbatim to `create`, inserted just before
	// the image name. They come from config.RuntimeArgs and are an escape hatch for
	// options the manager does not model natively.
	ExtraArgs []string
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

	if err := writeBuildContext(dir); err != nil {
		return err
	}

	dockerfile, buildArgs := baseBuildArgs(spec)
	args := []string{"build", "-t", spec.ImageName, "-f", filepath.Join(dir, dockerfile)}
	args = append(args, buildArgs...)
	args = append(args, dir)
	if err := d.run(ctx, args...); err != nil {
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

	if err := writeBuildContext(dir); err != nil {
		return err
	}

	args := []string{"build", "-t", spec.ImageName, "-f", filepath.Join(dir, workspaceDockerfile)}
	args = append(args, workspaceBuildArgs(spec)...)
	args = append(args, dir)
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
		"--workdir", ContainerWorkspaceDir,
		"--env", "HOME=/home/debian",
		"--env", "TERM=xterm-256color",
		"--volume", spec.HomeDir + ":/home/debian",
	}

	if binary == "podman" {
		args = append(args, "--userns", "keep-id")
	}

	if spec.HostNetwork {
		args = append(args, "--network", "host")
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

	// User-supplied extras last in the options section (just before the image), so
	// they can extend or override anything the manager set above.
	args = append(args, spec.ExtraArgs...)

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
