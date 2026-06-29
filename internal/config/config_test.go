package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesDefaultsWhenConfigDoesNotExist(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Runtime != RuntimeDocker {
		t.Fatalf("Runtime = %q, want %q", cfg.Runtime, RuntimeDocker)
	}

	if cfg.BaseImage.Name == "" {
		t.Fatal("BaseImage should have a default value")
	}

	if cfg.UseLocalOpenCodeAuth {
		t.Fatal("UseLocalOpenCodeAuth should default to false")
	}

	if cfg.LogLevel != LogLevelWarning {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, LogLevelWarning)
	}

	if cfg.HostNetwork {
		t.Fatal("HostNetwork should default to false")
	}
}

func TestLoadParsesHostNetwork(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("hostNetwork: true\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.HostNetwork {
		t.Fatal("HostNetwork = false, want true")
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("logLevel: verbose\n"))

	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject an invalid logLevel")
	}
}

func TestLoadAcceptsValidLogLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("logLevel: debug\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.LogLevel != LogLevelDebug {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, LogLevelDebug)
	}
}

func TestLoadMergesConfigWithDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("runtime: podman\nworkspaceRoot: /tmp/workspaces\nuseLocalOpenCodeAuth: true\nbaseImage:\n  packages:\n    - ripgrep\n  commands:\n    - update-ca-certificates\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Runtime != RuntimePodman {
		t.Fatalf("Runtime = %q, want %q", cfg.Runtime, RuntimePodman)
	}

	if cfg.WorkspaceRoot != "/tmp/workspaces" {
		t.Fatalf("WorkspaceRoot = %q, want /tmp/workspaces", cfg.WorkspaceRoot)
	}
	if !cfg.UseLocalOpenCodeAuth {
		t.Fatal("UseLocalOpenCodeAuth = false, want true")
	}

	if cfg.BaseImage.Name == "" {
		t.Fatal("BaseImage should keep default value")
	}
	if len(cfg.BaseImage.Packages) != 1 || cfg.BaseImage.Packages[0] != "ripgrep" {
		t.Fatalf("BaseImage.Packages = %#v, want ripgrep", cfg.BaseImage.Packages)
	}
	if len(cfg.BaseImage.Commands) != 1 || cfg.BaseImage.Commands[0] != "update-ca-certificates" {
		t.Fatalf("BaseImage.Commands = %#v, want update-ca-certificates", cfg.BaseImage.Commands)
	}
}

func TestEnsureGlobalConfigCreatesTemplates(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	if err := EnsureGlobalConfig(); err != nil {
		t.Fatalf("EnsureGlobalConfig returned error: %v", err)
	}

	dir := filepath.Join(configHome, "opencode-manager")

	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("AGENTS.md = %q, want empty", string(agents))
	}

	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatalf("read opencode.json: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("opencode.json must not be empty")
	}
	if !json.Valid(data) {
		t.Fatalf("opencode.json is not valid JSON: %q", string(data))
	}

	for _, name := range GlobalTemplateDirs {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat template dir %q: %v", name, err)
		}
		if !info.IsDir() {
			t.Fatalf("template %q should be a directory", name)
		}
	}
}

func TestEnsureGlobalConfigPreservesExistingFiles(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	dir := filepath.Join(configHome, "opencode-manager")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create global dir: %v", err)
	}
	custom := "{\"model\":\"custom/model\"}\n"
	writeFile(t, filepath.Join(dir, "opencode.json"), []byte(custom))

	if err := EnsureGlobalConfig(); err != nil {
		t.Fatalf("EnsureGlobalConfig returned error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatalf("read opencode.json: %v", err)
	}
	if string(got) != custom {
		t.Fatalf("opencode.json = %q, want preserved %q", string(got), custom)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func TestIsManagedBaseImage(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"docker.io/mroger78/ocm-base:latest", true},
		{"docker.io/mroger78/ocm-base:dev", true},
		{"docker.io/mroger78/ocm-base:0.3.0", true},
		{"docker.io/mroger78/ocm-base", true},
		{"mroger78/ocm-base:dev", true},
		{"docker.io/mroger78/ocm-base@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true},
		{"debian:stable-slim", false},
		{"docker.io/debian:stable-slim", false},
		{"docker.io/mroger78/other:latest", false},
		{"ghcr.io/mroger78/ocm-base:latest", false},
	}
	for _, c := range cases {
		if got := IsManagedBaseImage(c.name); got != c.want {
			t.Errorf("IsManagedBaseImage(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
