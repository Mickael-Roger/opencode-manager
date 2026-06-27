package runtime

import (
	"context"
	"os"
	"path/filepath"
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

func readBuildFile(t *testing.T, name string) string {
	t.Helper()
	data, err := buildContextFS.ReadFile("buildcontext/" + name)
	if err != nil {
		t.Fatalf("read embedded %q: %v", name, err)
	}
	return string(data)
}

func hasBuildArg(args []string, kv string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--build-arg" && args[i+1] == kv {
			return true
		}
	}
	return false
}

func TestBaseDockerfileInstallsRequiredTools(t *testing.T) {
	content := readBuildFile(t, baseDockerfile)

	for _, want := range []string{
		"ARG BASE_IMAGE=debian:stable-slim",
		"FROM ${BASE_IMAGE}",
		"ARG EXTRA_PACKAGES=",
		"ARG EXTRA_COMMANDS=",
		"COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /usr/local/bin/",
		"git", "ripgrep", "jq", "nodejs", "npm",
		"${EXTRA_PACKAGES}",
		"RUN ${EXTRA_COMMANDS}",
		"getent group linuxbrew >/dev/null 2>&1 || groupadd -r linuxbrew",
		"id -u linuxbrew >/dev/null 2>&1 || useradd -r -g linuxbrew -m -s /bin/bash linuxbrew",
		"if [ ! -x /home/linuxbrew/.linuxbrew/bin/brew ]; then su linuxbrew -c 'git clone --depth=1 https://github.com/Homebrew/brew",
		"git --version && rg --version && jq --version && npx --version && uvx --version",
		"su linuxbrew -c '/home/linuxbrew/.linuxbrew/bin/brew --version'",
		"command -v opencode >/dev/null 2>&1 || npm install -g opencode-ai",
		"COPY opencode-manager-attach /usr/local/bin/opencode-manager-attach",
		"RUN chmod 0755 /usr/local/bin/opencode-manager-attach",
		"ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:/usr/local/bin",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("base Dockerfile missing %q:\n%s", want, content)
		}
	}

	// The base image must not use heredocs: the buildah builder used by Podman
	// parses the heredoc body as further Dockerfile instructions and fails.
	if strings.Contains(content, "<<") {
		t.Fatalf("base Dockerfile must not use heredocs (unsupported by Podman/buildah):\n%s", content)
	}

	// The install steps must be idempotent so the recipe can layer on a base that
	// already provides a tool without crashing (e.g. "user already exists"). Guard
	// against regressing to the unconditional forms.
	for _, unguarded := range []string{
		"RUN groupadd -r linuxbrew &&",
		"RUN npm install -g opencode-ai",
		"RUN npm install -g tokscale",
		"RUN echo '[ -f \"$HOME/.env\" ]",
	} {
		if strings.Contains(content, unguarded) {
			t.Fatalf("base Dockerfile has a non-idempotent step %q:\n%s", unguarded, content)
		}
	}

	// Package install -> user commands -> OpenCode install, in that order.
	packages := strings.Index(content, "apt-get update && apt-get install")
	command := strings.Index(content, "RUN ${EXTRA_COMMANDS}")
	opencodeInstall := strings.Index(content, "npm install -g opencode-ai")
	if packages == -1 || command == -1 || opencodeInstall == -1 || !(packages < command && command < opencodeInstall) {
		t.Fatalf("expected user commands after package install and before OpenCode install:\n%s", content)
	}
}

func TestBaseDockerfileModuleSupport(t *testing.T) {
	content := readBuildFile(t, baseDockerfile)
	for _, want := range []string{
		"COPY opencode-manager-entrypoint /usr/local/bin/opencode-manager-entrypoint",
		"chmod 0755 /usr/local/bin/opencode-manager-attach /usr/local/bin/opencode-manager-entrypoint",
		"%sudo ALL=(ALL) NOPASSWD:ALL",
		"/etc/sudoers.d/opencode-manager",
		"/etc/bash.bashrc",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("base Dockerfile missing %q:\n%s", want, content)
		}
	}
}

func TestOverlayDockerfileOnlyAddsExtras(t *testing.T) {
	content := readBuildFile(t, overlayDockerfile)
	for _, want := range []string{
		"FROM ${BASE_IMAGE}",
		"${EXTRA_PACKAGES}",
		"RUN ${EXTRA_COMMANDS}",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("overlay Dockerfile missing %q:\n%s", want, content)
		}
	}
	// The overlay inherits the heavy layers from the prebuilt base; its
	// instructions must not reinstall the tooling or re-COPY the manager scripts.
	// (Comment lines may mention them, so check only instruction lines.)
	instructions := nonCommentLines(content)
	for _, unwanted := range []string{"linuxbrew", "opencode-ai", "tokscale", "COPY "} {
		if strings.Contains(instructions, unwanted) {
			t.Fatalf("overlay Dockerfile instructions should not contain %q:\n%s", unwanted, instructions)
		}
	}
}

// nonCommentLines returns the Dockerfile content with comment lines removed.
func nonCommentLines(content string) string {
	var b strings.Builder
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestWorkspaceDockerfileUsesBaseAndHostUIDGIDArgs(t *testing.T) {
	content := readBuildFile(t, workspaceDockerfile)
	for _, want := range []string{
		"FROM ${BASE_IMAGE}",
		"ARG UID",
		"ARG GID",
		"getent group ${GID}",
		"useradd -m -u ${UID} -g ${GID}",
		"usermod -aG sudo ${user_name}",
		"/home/debian/workspace",
		"chown -R ${UID}:${GID} /home/debian /home/linuxbrew/.linuxbrew",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("workspace Dockerfile missing %q:\n%s", want, content)
		}
	}
}

func TestBaseBuildArgs(t *testing.T) {
	// Full base build (custom distro, extra package + command).
	dockerfile, args := baseBuildArgs(BaseBuildSpec{
		FromImage: "debian:stable-slim",
		Packages:  []string{"kubectl"},
		Commands:  []string{"update-ca-certificates", "echo hi"},
	})
	if dockerfile != baseDockerfile {
		t.Fatalf("dockerfile = %q, want %q", dockerfile, baseDockerfile)
	}
	for _, want := range []string{"BASE_IMAGE=debian:stable-slim", "EXTRA_PACKAGES=kubectl", "EXTRA_COMMANDS=update-ca-certificates && echo hi"} {
		if !hasBuildArg(args, want) {
			t.Fatalf("base build args missing %q: %v", want, args)
		}
	}

	// Prebuilt overlay uses the overlay Dockerfile.
	dockerfile, args = baseBuildArgs(BaseBuildSpec{
		FromImage: "docker.io/mroger78/ocm-base:latest",
		Packages:  []string{"htop"},
		Prebuilt:  true,
	})
	if dockerfile != overlayDockerfile {
		t.Fatalf("prebuilt dockerfile = %q, want %q", dockerfile, overlayDockerfile)
	}
	if !hasBuildArg(args, "BASE_IMAGE=docker.io/mroger78/ocm-base:latest") || !hasBuildArg(args, "EXTRA_PACKAGES=htop") {
		t.Fatalf("overlay build args wrong: %v", args)
	}

	// No extras: only BASE_IMAGE, no EXTRA_* args.
	_, args = baseBuildArgs(BaseBuildSpec{FromImage: "debian:stable-slim"})
	for _, unwanted := range []string{"EXTRA_PACKAGES=", "EXTRA_COMMANDS="} {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--build-arg" && strings.HasPrefix(args[i+1], unwanted) {
				t.Fatalf("did not expect %q with no extras: %v", unwanted, args)
			}
		}
	}
}

func TestWorkspaceBuildArgs(t *testing.T) {
	args := workspaceBuildArgs(BuildSpec{BaseImage: "opencode-manager/base:abc", UID: 501, GID: 20})
	for _, want := range []string{"BASE_IMAGE=opencode-manager/base:abc", "UID=501", "GID=20"} {
		if !hasBuildArg(args, want) {
			t.Fatalf("workspace build args missing %q: %v", want, args)
		}
	}
}

func TestWriteBuildContextMaterializesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := writeBuildContext(dir); err != nil {
		t.Fatalf("writeBuildContext: %v", err)
	}
	for _, name := range []string{baseDockerfile, overlayDockerfile, workspaceDockerfile, attachScriptName, entrypointScriptName} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("build context missing %q: %v", name, err)
		}
		if name == attachScriptName || name == entrypointScriptName {
			if info.Mode().Perm()&0o111 == 0 {
				t.Fatalf("script %q must be executable, got %v", name, info.Mode())
			}
		}
	}
}

func TestManagerScriptsContent(t *testing.T) {
	if attach := readBuildFile(t, attachScriptName); !strings.Contains(attach, "exec opencode attach \"$url\" --dir \"$dir\" -c") {
		t.Fatalf("attach script missing attach-to-last-session command:\n%s", attach)
	}
	entrypoint := readBuildFile(t, entrypointScriptName)
	if !strings.Contains(entrypoint, ". \"$HOME/.env\"") || !strings.Contains(entrypoint, "opencode serve") {
		t.Fatalf("entrypoint script missing env sourcing or server launch:\n%s", entrypoint)
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
