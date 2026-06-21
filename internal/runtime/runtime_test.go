package runtime

import (
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
	args := createArgs(ContainerSpec{
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
	})

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
		"useradd -m -s /bin/bash linuxbrew",
		"su linuxbrew -c 'git clone --depth=1 https://github.com/Homebrew/brew",
		"git --version && rg --version && jq --version && npx --version && uvx --version",
		"su linuxbrew -c '/home/linuxbrew/.linuxbrew/bin/brew --version'",
		"npm install -g opencode-ai && which opencode && opencode --version",
		"/usr/local/bin/opencode-manager-attach",
		"exec opencode attach \"$url\" --dir \"$dir\" -c",
		"ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:/usr/local/bin",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("base Containerfile missing %q:\n%s", want, content)
		}
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
