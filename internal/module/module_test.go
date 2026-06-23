package module

import (
	"os"
	"path/filepath"
	"testing"
)

// writeModule creates a module directory with the given module.yml content and
// executable install/uninstall scripts.
func writeModule(t *testing.T, root, name, manifest string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write module.yml: %v", err)
	}
	for _, s := range []string{InstallScript, UninstallScript} {
		if err := os.WriteFile(filepath.Join(dir, s), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", s, err)
		}
	}
	return dir
}

func TestLoadValidModule(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "aws", `name: aws
version: 2
description: AWS access.
prompts:
  - { name: profile, label: Profile, type: string, required: true }
  - { name: region, label: Region, type: select, options: [a, b], default: a }
`)

	mod, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if mod.Name != "aws" || mod.Version != 2 {
		t.Fatalf("unexpected module: %+v", mod)
	}
	if len(mod.Prompts) != 2 || mod.Prompts[0].Name != "profile" {
		t.Fatalf("unexpected prompts: %+v", mod.Prompts)
	}
	if !mod.Prompts[1].Secret() && mod.Prompts[1].Type != PromptSelect {
		t.Fatalf("expected select prompt: %+v", mod.Prompts[1])
	}
}

func TestLoadRejectsNameMismatch(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "aws", "name: other\nversion: 1\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for name not matching directory")
	}
}

func TestLoadRejectsMissingInstall(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte("name: broken\nversion: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for missing install script")
	}
}

func TestLoadRejectsNonExecutableInstall(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "noexec", "name: noexec\nversion: 1\n")
	if err := os.Chmod(filepath.Join(dir, InstallScript), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for non-executable install script")
	}
}

func TestLoadRejectsBadPromptType(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "bad", `name: bad
version: 1
prompts:
  - { name: x, label: X, type: bogus }
`)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for unknown prompt type")
	}
}

func TestLoadRejectsSelectWithoutOptions(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "sel", `name: sel
version: 1
prompts:
  - { name: x, label: X, type: select }
`)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for select prompt without options")
	}
}

func TestCatalogFirstRootWins(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	writeModule(t, root1, "git", "name: git\nversion: 5\n")
	writeModule(t, root2, "git", "name: git\nversion: 1\n")
	writeModule(t, root2, "aws", "name: aws\nversion: 1\n")

	mods, err := Catalog([]string{root1, root2})
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(mods) != 2 {
		t.Fatalf("expected 2 modules, got %d: %+v", len(mods), mods)
	}
	// Sorted by name: aws then git; git from root1 (version 5) shadows root2.
	if mods[1].Name != "git" || mods[1].Version != 5 {
		t.Fatalf("expected git v5 from first root, got %+v", mods[1])
	}
}

func TestEnvVarName(t *testing.T) {
	if got := EnvVarName("access_key"); got != "OCM_ACCESS_KEY" {
		t.Fatalf("EnvVarName = %q, want OCM_ACCESS_KEY", got)
	}
}

// TestBuiltinsSeedAndLoad verifies every shipped built-in module extracts with
// an executable install/uninstall and a valid module.yml.
func TestBuiltinsSeedAndLoad(t *testing.T) {
	dest := t.TempDir()
	if err := SeedBuiltins(dest); err != nil {
		t.Fatalf("SeedBuiltins: %v", err)
	}

	mods, err := Catalog([]string{dest})
	if err != nil {
		t.Fatalf("Catalog over seeded built-ins: %v", err)
	}

	found := map[string]bool{}
	for _, m := range mods {
		found[m.Name] = true
	}
	for _, want := range []string{"hello", "git", "aws", "github", "kubectl", "ssh"} {
		if !found[want] {
			t.Errorf("built-in module %q missing from catalog %v", want, found)
		}
	}
}
