package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/config"
)

func TestSafeName(t *testing.T) {
	tests := map[string]string{
		"My Project": "my-project",
		"foo/bar":    "foo-bar",
		"foo  bar":   "foo-bar",
		"!!!":        "",
		"équipe":     "quipe",
	}

	for input, want := range tests {
		if got := SafeName(input); got != want {
			t.Fatalf("SafeName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNewManifestRejectsEmptySafeName(t *testing.T) {
	registry := NewRegistry(testConfig(t))

	if _, err := registry.NewManifest("!!!"); err == nil {
		t.Fatal("NewManifest returned nil error, want invalid name error")
	}
}

func TestNewManifestRejectsSlugCollision(t *testing.T) {
	registry := NewRegistry(testConfig(t))
	if err := os.MkdirAll(registry.WorkspaceDir("foo-bar"), 0o700); err != nil {
		t.Fatalf("create workspace directory: %v", err)
	}

	if _, err := registry.NewManifest("foo/bar"); err == nil {
		t.Fatal("NewManifest returned nil error, want slug collision error")
	}
}

func TestRegistryListSortsWorkspaces(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t)
	cfg.WorkspaceRoot = root
	registry := NewRegistry(cfg)

	alpha, err := registry.NewManifest("alpha")
	if err != nil {
		t.Fatalf("NewManifest alpha returned error: %v", err)
	}
	zeta, err := registry.NewManifest("zeta")
	if err != nil {
		t.Fatalf("NewManifest zeta returned error: %v", err)
	}

	if err := SaveManifest(filepath.Join(registry.WorkspaceDir("zeta"), ManifestFile), zeta); err != nil {
		t.Fatalf("SaveManifest zeta returned error: %v", err)
	}
	if err := SaveManifest(filepath.Join(registry.WorkspaceDir("alpha"), ManifestFile), alpha); err != nil {
		t.Fatalf("SaveManifest alpha returned error: %v", err)
	}

	workspaces, err := registry.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if len(workspaces) != 2 || workspaces[0].Manifest.Name != "alpha" || workspaces[1].Manifest.Name != "zeta" {
		t.Fatalf("workspaces = %#v, want alpha then zeta", workspaces)
	}
}

func TestCreateWorkspaceWritesLayoutAndManifest(t *testing.T) {
	registry := NewRegistry(testConfig(t))

	result, err := registry.Create("Demo Workspace")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if result.Manifest.Name != "Demo Workspace" {
		t.Fatalf("Name = %q, want Demo Workspace", result.Manifest.Name)
	}

	paths := []string{
		ManifestFile,
		"home",
		filepath.Join("home", "workspace"),
		filepath.Join("home", ".config", "opencode", "opencode.json"),
		filepath.Join("home", ".config", "opencode", "agent"),
		filepath.Join("home", ".config", "opencode", "commands"),
		filepath.Join("home", ".config", "opencode", "plugins"),
		filepath.Join("home", ".config", "opencode", "skills"),
	}

	for _, path := range paths {
		if _, err := os.Stat(filepath.Join(result.Path, path)); err != nil {
			t.Fatalf("expected workspace path %q: %v", path, err)
		}
	}

	for _, path := range []string{"env", "opencode", "image", "modules", "opencode.json"} {
		if _, err := os.Stat(filepath.Join(result.Path, path)); !os.IsNotExist(err) {
			t.Fatalf("top-level path %q should not exist", path)
		}
	}

	if result.Manifest.Image.BaseImage == "" {
		t.Fatal("manifest image base image should be declared")
	}
	if len(result.Manifest.Image.Packages) != 1 || result.Manifest.Image.Packages[0] != "ripgrep" {
		t.Fatalf("manifest packages = %#v, want ripgrep", result.Manifest.Image.Packages)
	}
	if len(result.Manifest.Image.Commands) != 1 || result.Manifest.Image.Commands[0] != "update-ca-certificates" {
		t.Fatalf("manifest commands = %#v, want update-ca-certificates", result.Manifest.Image.Commands)
	}
	if result.Manifest.Env == nil {
		t.Fatal("manifest env map should be initialized")
	}

	workspaces, err := registry.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].Manifest.Name != "Demo Workspace" {
		t.Fatalf("workspaces = %#v, want created workspace", workspaces)
	}
}

func TestCreateWorkspaceCopiesOpenCodePreconfiguration(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	preconfigDir := filepath.Join(configHome, "opencode-manager", "opencode")
	seedFiles := map[string]string{
		"opencode.json":                                 "{\"model\":\"test/model\"}\n",
		filepath.Join("agent", "reviewer.md"):           "review instructions\n",
		filepath.Join("commands", "ship.md"):            "ship command\n",
		filepath.Join("plugins", "local-plugin.js"):     "export default {}\n",
		filepath.Join("skills", "debug", "SKILL.md"):    "debug skill\n",
		filepath.Join("skills", "debug", "script.sh"):   "#!/bin/sh\n",
		filepath.Join("skills", "debug", "assets", "x"): "asset\n",
	}
	for name, content := range seedFiles {
		path := filepath.Join(preconfigDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create preconfiguration directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write preconfiguration file: %v", err)
		}
	}

	registry := NewRegistry(testConfig(t))
	result, err := registry.Create("Configured Workspace")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	targetDir := filepath.Join(result.Path, "home", ".config", "opencode")
	for name, want := range seedFiles {
		got, err := os.ReadFile(filepath.Join(targetDir, name))
		if err != nil {
			t.Fatalf("read copied file %q: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("copied file %q = %q, want %q", name, string(got), want)
		}
	}
}

func TestCreateWorkspaceSkipsMissingOpenCodePreconfiguration(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	registry := NewRegistry(testConfig(t))
	result, err := registry.Create("Plain Workspace")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	targetDir := filepath.Join(result.Path, "home", ".config", "opencode")
	got, err := os.ReadFile(filepath.Join(targetDir, "opencode.json"))
	if err != nil {
		t.Fatalf("read default opencode.json: %v", err)
	}
	if string(got) != "{}\n" {
		t.Fatalf("default opencode.json = %q, want {}", string(got))
	}

	if _, err := os.Stat(filepath.Join(configHome, "opencode-manager")); !os.IsNotExist(err) {
		t.Fatalf("host preconfiguration directory should not be created: %v", err)
	}
	for _, name := range []string{"agent", "commands", "plugins", "skills"} {
		if _, err := os.Stat(filepath.Join(targetDir, name)); err != nil {
			t.Fatalf("expected default workspace directory %q: %v", name, err)
		}
	}
}

func TestCreateWorkspaceCopiesPartialOpenCodePreconfiguration(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	preconfigDir := filepath.Join(configHome, "opencode-manager", "opencode")
	seedFiles := map[string]string{
		"opencode.json":                      "{\"model\":\"partial/model\"}\n",
		filepath.Join("commands", "test.md"): "test command\n",
	}
	for name, content := range seedFiles {
		path := filepath.Join(preconfigDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create preconfiguration directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write preconfiguration file: %v", err)
		}
	}

	registry := NewRegistry(testConfig(t))
	result, err := registry.Create("Partial Workspace")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	targetDir := filepath.Join(result.Path, "home", ".config", "opencode")
	for name, want := range seedFiles {
		got, err := os.ReadFile(filepath.Join(targetDir, name))
		if err != nil {
			t.Fatalf("read copied file %q: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("copied file %q = %q, want %q", name, string(got), want)
		}
	}
	for _, name := range []string{"agent", "plugins", "skills"} {
		entries, err := os.ReadDir(filepath.Join(targetDir, name))
		if err != nil {
			t.Fatalf("read default directory %q: %v", name, err)
		}
		if len(entries) != 0 {
			t.Fatalf("default directory %q entries = %d, want empty", name, len(entries))
		}
	}
}

func TestDeleteWorkspaceRemovesWorkspaceDirectory(t *testing.T) {
	registry := NewRegistry(testConfig(t))
	result, err := registry.Create("Demo Workspace")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	summary := Summary{Manifest: result.Manifest, Path: result.Path}
	if err := registry.Delete(summary); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
		t.Fatalf("workspace path still exists or stat failed unexpectedly: %v", err)
	}
}

func TestDeleteWorkspaceRejectsPathOutsideWorkspaceRoot(t *testing.T) {
	registry := NewRegistry(testConfig(t))
	manifest, err := registry.NewManifest("Demo Workspace")
	if err != nil {
		t.Fatalf("NewManifest returned error: %v", err)
	}

	summary := Summary{Manifest: manifest, Path: t.TempDir()}
	if err := registry.Delete(summary); err == nil {
		t.Fatal("Delete returned nil error, want path safety error")
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		WorkspaceRoot: t.TempDir(),
		Runtime:       config.RuntimeDocker,
		BaseImage: config.BaseImageConfig{
			Name:     "debian:stable-slim",
			Packages: []string{"ripgrep"},
			Commands: []string{"update-ca-certificates"},
		},
	}
}
