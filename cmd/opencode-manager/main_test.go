package main

import (
	"strings"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

func TestRunCLIRejectsUnsupportedCommands(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("Default returned error: %v", err)
	}
	cfg.WorkspaceRoot = t.TempDir()

	for _, args := range [][]string{
		{"open"},
		{"start"},
		{"stop"},
		{"list", "extra"},
		{"attach"},
	} {
		err := runCLI(cfg, args)
		if err == nil {
			t.Fatalf("runCLI(%v) returned nil error", args)
		}
		if !strings.Contains(err.Error(), "opencode-manager list") || !strings.Contains(err.Error(), "opencode-manager attach <workspace>") {
			t.Fatalf("runCLI(%v) error = %q, want usage with only list and attach", args, err)
		}
	}
}

func TestFindWorkspaceMatchesNameOrSlug(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("Default returned error: %v", err)
	}
	cfg.WorkspaceRoot = t.TempDir()
	registry := workspace.NewRegistry(cfg)
	if _, err := registry.Create("My Project"); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	for _, name := range []string{"My Project", "my-project"} {
		summary, err := findWorkspace(cfg, name)
		if err != nil {
			t.Fatalf("findWorkspace(%q) returned error: %v", name, err)
		}
		if summary.Manifest.Name != "My Project" {
			t.Fatalf("findWorkspace(%q) returned %q, want My Project", name, summary.Manifest.Name)
		}
	}
}
