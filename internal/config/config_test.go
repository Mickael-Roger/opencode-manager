package config

import (
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
}

func TestLoadMergesConfigWithDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("runtime: podman\nworkspaceRoot: /tmp/workspaces\nbaseImage:\n  packages:\n    - ripgrep\n  commands:\n    - update-ca-certificates\n"))

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

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}
