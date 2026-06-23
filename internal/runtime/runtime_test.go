package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestNewDriverRejectsUnsupportedRuntime(t *testing.T) {
	if _, err := NewDriver("containerd"); err == nil {
		t.Fatal("NewDriver returned nil error, want unsupported runtime error")
	}
}

func TestDriverName(t *testing.T) {
	driver, err := NewDriver("docker")
	if err != nil {
		t.Fatalf("NewDriver returned error: %v", err)
	}

	if driver.Name() != "docker" {
		t.Fatalf("Name = %q, want docker", driver.Name())
	}
}

func TestCreateArgsIncludesReadOnlyMounts(t *testing.T) {
	spec := ContainerSpec{
		Name:      "opencode-manager-demo",
		ImageName: "opencode-manager/demo:latest",
		HomeDir:   "/data/demo/home",
		UID:       501,
		GID:       20,
		Mounts: []Mount{
			{Source: "/cfg/AGENTS.md", Target: "/home/debian/.config/opencode/AGENTS.md", ReadOnly: true},
			{Source: "/cfg/skills", Target: "/home/debian/.config/opencode/skills", ReadOnly: true},
		},
		Command: []string{"opencode"},
	}

	args := createArgs("podman", spec)

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--volume /data/demo/home:/home/debian",
		"--volume /cfg/AGENTS.md:/home/debian/.config/opencode/AGENTS.md:ro",
		"--volume /cfg/skills:/home/debian/.config/opencode/skills:ro",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("create args missing %q:\n%s", want, joined)
		}
	}

	if args[len(args)-1] != "opencode" || args[len(args)-2] != "opencode-manager/demo:latest" {
		t.Fatalf("expected image then command at end of args, got: %v", args)
	}
}

func TestCreateArgsUserNamespace(t *testing.T) {
	spec := ContainerSpec{
		Name:      "opencode-manager-demo",
		ImageName: "opencode-manager/demo:latest",
		HomeDir:   "/data/demo/home",
		UID:       501,
		GID:       20,
		Command:   []string{"opencode"},
	}

	// Rootless Podman maps the host user to container root, so the workspace
	// process needs keep-id to own its bind-mounted home.
	if podman := strings.Join(createArgs("podman", spec), " "); !strings.Contains(podman, "--userns keep-id") {
		t.Fatalf("podman create args missing --userns keep-id:\n%s", podman)
	}

	// Docker preserves numeric bind-mount ownership for --user and does not
	// support keep-id, so the flag must not be passed.
	if docker := strings.Join(createArgs("docker", spec), " "); strings.Contains(docker, "keep-id") {
		t.Fatalf("docker create args must not include keep-id:\n%s", docker)
	}
}

func TestRenderBaseContainerfileInstallsRequiredTools(t *testing.T) {
	content := renderBaseContainerfile(BaseBuildSpec{
		ImageName: "opencode-manager/base:test",
		FromImage: "debian:stable-slim",
		Packages:  []string{"kubectl"},
		Commands:  []string{"update-ca-certificates"},
	})

	for _, want := range []string{
		"FROM debian:stable-slim",
		"COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /usr/local/bin/",
		"git",
		"ripgrep",
		"jq",
		"nodejs",
		"npm",
		"kubectl",
		"RUN update-ca-certificates",
		"groupadd -r linuxbrew && useradd -r -g linuxbrew -m -s /bin/bash linuxbrew",
		"su linuxbrew -c 'git clone --depth=1 https://github.com/Homebrew/brew",
		"git --version && rg --version && jq --version && npx --version && uvx --version",
		"su linuxbrew -c '/home/linuxbrew/.linuxbrew/bin/brew --version'",
		"npm install -g opencode-ai && which opencode && opencode --version",
		"COPY opencode-manager-attach /usr/local/bin/opencode-manager-attach",
		"RUN chmod 0755 /usr/local/bin/opencode-manager-attach",
		"ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:/usr/local/bin",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("base Containerfile missing %q:\n%s", want, content)
		}
	}

	// The base image must not use heredocs: the buildah builder used by Podman
	// parses the heredoc body as further Containerfile instructions and fails.
	if strings.Contains(content, "<<") {
		t.Fatalf("base Containerfile must not use heredocs (unsupported by Podman/buildah):\n%s", content)
	}

	if !strings.Contains(attachScript, "exec opencode attach \"$url\" --dir \"$dir\" -c") {
		t.Fatalf("attach script missing attach-to-last-session command:\n%s", attachScript)
	}

	packages := strings.Index(content, "apt-get update && apt-get install")
	command := strings.Index(content, "RUN update-ca-certificates")
	opencodeInstall := strings.Index(content, "npm install -g opencode-ai")
	if packages == -1 || command == -1 || opencodeInstall == -1 || !(packages < command && command < opencodeInstall) {
		t.Fatalf("expected base image commands after package install and before OpenCode install:\n%s", content)
	}
}

func TestRenderWorkspaceContainerfileUsesCachedBaseAndHostUIDGIDArgs(t *testing.T) {
	content := renderWorkspaceContainerfile(BuildSpec{
		ImageName: "test:latest",
		BaseImage: "opencode-manager/base:abc123",
		UID:       501,
		GID:       20,
	})

	for _, want := range []string{
		"FROM opencode-manager/base:abc123",
		"ARG UID",
		"ARG GID",
		"getent group ${GID}",
		"useradd -m -u ${UID} -g ${GID}",
		"/home/debian/workspace",
		"chown -R ${UID}:${GID} /home/debian /home/linuxbrew/.linuxbrew",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("workspace Containerfile missing %q:\n%s", want, content)
		}
	}
}

func TestRenderBaseContainerfileModuleSupport(t *testing.T) {
	content := renderBaseContainerfile(BaseBuildSpec{
		ImageName: "opencode-manager/base:test",
		FromImage: "debian:stable-slim",
	})

	for _, want := range []string{
		"COPY opencode-manager-entrypoint /usr/local/bin/opencode-manager-entrypoint",
		"chmod 0755 /usr/local/bin/opencode-manager-attach /usr/local/bin/opencode-manager-entrypoint",
		"%sudo ALL=(ALL) NOPASSWD:ALL",
		"/etc/sudoers.d/opencode-manager",
		"/etc/bash.bashrc",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("base Containerfile missing %q:\n%s", want, content)
		}
	}

	// The supervisor entrypoint must source ~/.env and run the OpenCode server.
	if !strings.Contains(entrypointScript, ". \"$HOME/.env\"") || !strings.Contains(entrypointScript, "opencode serve") {
		t.Fatalf("entrypoint script missing env sourcing or server launch:\n%s", entrypointScript)
	}
}

func TestRenderWorkspaceContainerfileAddsSudoGroup(t *testing.T) {
	content := renderWorkspaceContainerfile(BuildSpec{
		ImageName: "test:latest",
		BaseImage: "opencode-manager/base:abc123",
		UID:       501,
		GID:       20,
	})
	if !strings.Contains(content, "usermod -aG sudo ${user_name}") {
		t.Fatalf("workspace Containerfile missing sudo group membership:\n%s", content)
	}
}

func TestExecBuildsUserAndEnvArgs(t *testing.T) {
	// Exec runs the binary, so just confirm it validates inputs.
	d := CLIDriver{binary: "docker"}
	if _, err := d.Exec(context.Background(), ExecSpec{Container: "", Args: []string{"x"}}); err == nil {
		t.Fatal("expected error for missing container")
	}
	if _, err := d.Exec(context.Background(), ExecSpec{Container: "c"}); err == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestIsMissingResourceOutputRecognizesPodmanMissingImage(t *testing.T) {
	output := []byte("[]\nError: opencode-manager/base:abc123: image not known")

	if !isMissingResourceOutput(output) {
		t.Fatalf("expected Podman missing image output to be recognized")
	}
}
