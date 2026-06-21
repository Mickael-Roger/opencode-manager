package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

type Driver interface {
	Name() string
	Available(context.Context) error
	BuildBaseImage(context.Context, BaseBuildSpec) error
	BuildImage(context.Context, BuildSpec) error
	ContainerStatus(context.Context, string) (string, error)
	CreateContainer(context.Context, ContainerSpec) error
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string) error
	RemoveContainer(context.Context, string) error
	RemoveImage(context.Context, string) error
	AttachCommand(string) *exec.Cmd
	ExecCommand(string, []string) *exec.Cmd
	ExecOutput(context.Context, string, []string) ([]byte, error)
}

type BaseBuildSpec struct {
	ImageName string
	FromImage string
	Packages  []string
	Commands  []string
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
	Command   []string
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
		return fmt.Errorf("%s executable not found: %w", d.binary, err)
	}

	cmd := exec.CommandContext(ctx, d.binary, "version")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s is not available: %w: %s", d.binary, err, string(output))
	}

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
		return nil
	}

	dir, err := os.MkdirTemp("", "opencode-manager-base-image-*")
	if err != nil {
		return fmt.Errorf("create temporary base build context: %w", err)
	}
	defer os.RemoveAll(dir)

	containerfile := filepath.Join(dir, "Containerfile")
	if err := os.WriteFile(containerfile, []byte(renderBaseContainerfile(spec)), 0o600); err != nil {
		return fmt.Errorf("write base Containerfile: %w", err)
	}

	return d.run(ctx, "build", "-t", spec.ImageName, "-f", containerfile, dir)
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
	return d.run(ctx, args...)
}

func (d CLIDriver) ContainerStatus(ctx context.Context, name string) (string, error) {
	if name == "" {
		return StatusMissing, fmt.Errorf("container name is required")
	}

	cmd := exec.CommandContext(ctx, d.binary, "inspect", "-f", "{{.State.Status}}", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.ToLower(string(output))
		if strings.Contains(text, "no such") || strings.Contains(text, "not found") || strings.Contains(text, "does not exist") {
			return StatusMissing, nil
		}

		return StatusUnknown, fmt.Errorf("inspect container %q: %w: %s", name, err, strings.TrimSpace(string(output)))
	}

	status := strings.TrimSpace(string(output))
	if status == "" {
		return StatusUnknown, nil
	}

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

	for key, value := range spec.Env {
		args = append(args, "--env", key+"="+value)
	}

	args = append(args, spec.ImageName)
	args = append(args, spec.Command...)

	return d.run(ctx, args...)
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

func (d CLIDriver) AttachCommand(name string) *exec.Cmd {
	return exec.Command(d.binary, "attach", name)
}

// ExecCommand runs a command inside an already-running container with an
// interactive TTY, e.g. an interactive shell.
func (d CLIDriver) ExecCommand(name string, command []string) *exec.Cmd {
	args := append([]string{"exec", "--interactive", "--tty", name}, command...)
	return exec.Command(d.binary, args...)
}

// ExecOutput runs a command inside a running container without a TTY and
// returns its standard output. Used to collect machine-readable output such as
// `tokscale --json`.
func (d CLIDriver) ExecOutput(ctx context.Context, name string, command []string) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("container name is required")
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("exec command is required")
	}

	args := append([]string{"exec", name}, command...)
	cmd := exec.CommandContext(ctx, d.binary, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s exec %s: %w: %s", d.binary, name, err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

func (d CLIDriver) run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, d.binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", d.binary, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}

	return nil
}

func (d CLIDriver) runAllowMissing(ctx context.Context, args []string, resource string) error {
	cmd := exec.CommandContext(ctx, d.binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.ToLower(string(output))
		if strings.Contains(text, "no such") || strings.Contains(text, "not found") || strings.Contains(text, "does not exist") {
			return nil
		}

		return fmt.Errorf("remove %s: %s %s: %w: %s", resource, d.binary, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}

	return nil
}

func (d CLIDriver) imageExists(ctx context.Context, imageName string) (bool, error) {
	cmd := exec.CommandContext(ctx, d.binary, "image", "inspect", imageName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.ToLower(string(output))
		if strings.Contains(text, "no such") || strings.Contains(text, "not found") || strings.Contains(text, "does not exist") {
			return false, nil
		}

		return false, fmt.Errorf("inspect image %q: %w: %s", imageName, err, strings.TrimSpace(string(output)))
	}

	return true, nil
}

func renderBaseContainerfile(spec BaseBuildSpec) string {
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(spec.FromImage)
	b.WriteString("\n\n")
	b.WriteString("COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /usr/local/bin/\n\n")

	packages := append([]string{"bash", "build-essential", "ca-certificates", "curl", "file", "git", "jq", "nodejs", "npm", "passwd", "procps", "ripgrep", "sudo"}, spec.Packages...)
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends ")
	b.WriteString(strings.Join(packages, " "))
	b.WriteString(" && rm -rf /var/lib/apt/lists/*\n")
	for _, command := range spec.Commands {
		b.WriteString("RUN ")
		b.WriteString(command)
		b.WriteString("\n")
	}
	b.WriteString("RUN useradd -m -s /bin/bash linuxbrew && mkdir -p /home/linuxbrew/.linuxbrew/Homebrew /home/linuxbrew/.linuxbrew/bin && chown -R linuxbrew:linuxbrew /home/linuxbrew\n")
	b.WriteString("RUN su linuxbrew -c 'git clone --depth=1 https://github.com/Homebrew/brew /home/linuxbrew/.linuxbrew/Homebrew && ln -s ../Homebrew/bin/brew /home/linuxbrew/.linuxbrew/bin/brew && /home/linuxbrew/.linuxbrew/bin/brew --version'\n")
	b.WriteString("RUN git --version && rg --version && jq --version && npx --version && uvx --version && su linuxbrew -c '/home/linuxbrew/.linuxbrew/bin/brew --version'\n")
	b.WriteString("RUN curl -fsSL https://opencode.ai/install | bash && cp /root/.opencode/bin/opencode /usr/local/bin/opencode && chmod 0755 /usr/local/bin/opencode && /usr/local/bin/opencode --version\n")
	b.WriteString("RUN npm install -g tokscale@latest && which tokscale\n")
	b.WriteString("RUN cat > /usr/local/bin/opencode-manager-entrypoint <<'EOF'\n")
	b.WriteString("#!/bin/sh\n")
	b.WriteString("sessions=$(opencode session list 2>/dev/null || true)\n")
	b.WriteString("if [ -z \"$sessions\" ]; then\n")
	b.WriteString("  exec opencode\n")
	b.WriteString("fi\n")
	b.WriteString("exec opencode -c\n")
	b.WriteString("EOF\n")
	b.WriteString("RUN chmod 0755 /usr/local/bin/opencode-manager-entrypoint\n")
	b.WriteString("ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin\n")

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
	b.WriteString("if getent passwd ${UID}; then user_name=$(getent passwd ${UID} | cut -d: -f1); usermod -d /home/debian -s /bin/bash ${user_name}; else useradd -m -u ${UID} -g ${GID} -s /bin/bash debian; fi; ")
	b.WriteString("mkdir -p /home/debian/workspace && chown -R ${UID}:${GID} /home/debian /home/linuxbrew/.linuxbrew\n")
	b.WriteString("ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin\n")
	b.WriteString("WORKDIR /home/debian/workspace\n")

	return b.String()
}
