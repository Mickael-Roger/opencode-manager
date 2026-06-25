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

// writeExecutable adds an executable script named name to an existing module dir.
func writeExecutable(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
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

func TestLoadSelectWithOptionsCommand(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "k8s", `name: k8s
version: 1
prompts:
  - { name: contexts, label: Contexts, type: multiselect, required: true, optionsCommand: list }
`)
	writeExecutable(t, dir, "list")

	mod, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !mod.Prompts[0].DynamicOptions() {
		t.Fatal("expected prompt to report dynamic options")
	}
}

func TestLoadRejectsOptionsCommandOnNonSelect(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "bad", `name: bad
version: 1
prompts:
  - { name: x, label: X, type: string, optionsCommand: list }
`)
	writeExecutable(t, dir, "list")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for optionsCommand on a non-select prompt")
	}
}

func TestLoadAllowsOptionsCommandOnKeyPrompt(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "aws", `name: aws
version: 1
key: profile
prompts:
  - { name: profile, label: Profile, type: string, required: true, optionsCommand: list-accounts }
  - { name: secret_key, label: Secret, type: secret }
`)
	writeExecutable(t, dir, "list-accounts")
	mod, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if key := mod.PromptByName("profile"); key == nil || key.OptionsCommand != "list-accounts" {
		t.Fatalf("expected key prompt to carry optionsCommand: %+v", mod.Prompts)
	}
}

func TestLoadRejectsMissingOptionsCommand(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "k8s", `name: k8s
version: 1
prompts:
  - { name: contexts, label: Contexts, type: multiselect, required: true, optionsCommand: list }
`)
	// No "list" script written -> must fail.
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for missing optionsCommand script")
	}
}

func TestLoadResolveHook(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "k8s", `name: k8s
version: 1
prompts:
  - { name: contexts, label: Contexts, type: multiselect, required: true, optionsCommand: list }
`)
	writeExecutable(t, dir, "list")

	mod, err := Load(dir)
	if err != nil {
		t.Fatalf("Load without resolve: %v", err)
	}
	if mod.HasResolveHook() {
		t.Fatal("did not expect a resolve hook")
	}

	writeExecutable(t, dir, ResolveScript)
	mod, err = Load(dir)
	if err != nil {
		t.Fatalf("Load with resolve: %v", err)
	}
	if !mod.HasResolveHook() {
		t.Fatal("expected a resolve hook")
	}
}

func TestLoadRejectsNonExecutableResolveHook(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "git", `name: git
version: 1
`)
	if err := os.WriteFile(filepath.Join(dir, ResolveScript), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write resolve: %v", err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for non-executable resolve hook")
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

func TestMultiInstanceModule(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "ssh", `name: ssh
version: 1
key: host
prompts:
  - { name: host, label: Host, type: string, required: true }
`)
	mod, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !mod.Multi() {
		t.Fatal("expected multi-instance module")
	}
	if got := mod.InstanceID(map[string]string{"host": "github.com"}); got != "ssh:github.com" {
		t.Fatalf("InstanceID = %q, want ssh:github.com", got)
	}
}

func TestSingletonInstanceID(t *testing.T) {
	mod := Module{Name: "git"}
	if mod.Multi() {
		t.Fatal("module without key should be singleton")
	}
	if got := mod.InstanceID(nil); got != "git" {
		t.Fatalf("InstanceID = %q, want git", got)
	}
}

func TestLoadRejectsKeyWithoutPrompt(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "k", `name: k
version: 1
key: missing
prompts:
  - { name: host, label: Host, type: string, required: true }
`)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for key not matching a prompt")
	}
}

func TestLoadRejectsKeyOnOptionalPrompt(t *testing.T) {
	root := t.TempDir()
	dir := writeModule(t, root, "k", `name: k
version: 1
key: host
prompts:
  - { name: host, label: Host, type: string }
`)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected error for key prompt that is not required")
	}
}

func TestEnvVarName(t *testing.T) {
	if got := EnvVarName("access_key"); got != "OCM_ACCESS_KEY" {
		t.Fatalf("EnvVarName = %q, want OCM_ACCESS_KEY", got)
	}
}
