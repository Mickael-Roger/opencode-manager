package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/module"
)

func TestEnvHashChangesWithContent(t *testing.T) {
	home := t.TempDir()
	if envHash(home) != "" {
		t.Fatal("expected empty hash when .env is missing")
	}

	writeTestFile(t, filepath.Join(home, ".env"), []byte("export A=1\n"))
	h1 := envHash(home)
	if h1 == "" {
		t.Fatal("expected non-empty hash once .env exists")
	}

	writeTestFile(t, filepath.Join(home, ".env"), []byte("export A=2\n"))
	if envHash(home) == h1 {
		t.Fatal("expected hash to change when .env changes")
	}
}

func TestUpsertAndRemoveModule(t *testing.T) {
	var mods []ModuleInstance
	mods = upsertModule(mods, "aws", "aws", "cloud", 1, map[string]string{"profile": "prod"})
	mods = upsertModule(mods, "git", "git", "tools", 1, nil)
	if len(mods) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(mods))
	}

	// Upsert existing updates version/values in place.
	mods = upsertModule(mods, "aws", "aws", "cloud", 2, map[string]string{"profile": "dev"})
	if len(mods) != 2 {
		t.Fatalf("upsert should not append duplicate, got %d", len(mods))
	}
	for _, m := range mods {
		if m.Name == "aws" && (m.Version != 2 || m.Values["profile"] != "dev") {
			t.Fatalf("aws not updated in place: %+v", m)
		}
	}

	mods = removeModule(mods, "aws")
	if len(mods) != 1 || mods[0].Name != "git" {
		t.Fatalf("removeModule did not drop aws: %+v", mods)
	}
}

// TestMultiInstanceModule verifies that several entries of the same module
// (distinguished by instance ID) coexist and are upserted/removed independently.
func TestMultiInstanceModule(t *testing.T) {
	var mods []ModuleInstance
	mods = upsertModule(mods, "ssh:github.com", "ssh", "tools", 1, map[string]string{"host": "github.com"})
	mods = upsertModule(mods, "ssh:gitlab.com", "ssh", "tools", 1, map[string]string{"host": "gitlab.com"})
	if len(mods) != 2 {
		t.Fatalf("expected 2 ssh instances, got %d: %+v", len(mods), mods)
	}

	// Re-upserting one instance updates it in place without touching the other.
	mods = upsertModule(mods, "ssh:github.com", "ssh", "tools", 1, map[string]string{"host": "github.com", "user": "git"})
	if len(mods) != 2 {
		t.Fatalf("upsert of existing instance should not append, got %d", len(mods))
	}

	// Removing one instance leaves the other.
	mods = removeModule(mods, "ssh:github.com")
	if len(mods) != 1 || mods[0].InstanceID() != "ssh:gitlab.com" {
		t.Fatalf("removeModule did not target the right instance: %+v", mods)
	}
}

// TestInstanceIDFallback verifies a manifest entry with no ID (written before
// multi-instance support) reports its name as identity.
func TestInstanceIDFallback(t *testing.T) {
	if got := (ModuleInstance{Name: "git"}).InstanceID(); got != "git" {
		t.Fatalf("InstanceID fallback = %q, want git", got)
	}
	if got := (ModuleInstance{Name: "ssh", ID: "ssh:github.com"}).InstanceID(); got != "ssh:github.com" {
		t.Fatalf("InstanceID = %q, want ssh:github.com", got)
	}
}

func TestValueToString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hi", "hi"},
		{true, "true"},
		{false, "false"},
		{nil, ""},
		{[]any{"a", "b"}, "a,b"},
		{42, "42"},
	}
	for _, c := range cases {
		if got := valueToString(c.in); got != c.want {
			t.Errorf("valueToString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModuleMounts(t *testing.T) {
	if got := moduleMounts(config.Config{}); got != nil {
		t.Fatalf("expected no mounts without module dirs, got %v", got)
	}

	dir := t.TempDir()
	mounts := moduleMounts(config.Config{ModuleDirs: []string{dir}})
	if len(mounts) != 1 || mounts[0].Source != dir || mounts[0].Target != modulesContainerRoot || !mounts[0].ReadOnly {
		t.Fatalf("unexpected module mount: %+v", mounts)
	}

	// A non-existent directory is not mounted.
	if got := moduleMounts(config.Config{ModuleDirs: []string{filepath.Join(dir, "nope")}}); got != nil {
		t.Fatalf("expected no mount for missing dir, got %v", got)
	}
}

func TestModuleContainerDir(t *testing.T) {
	if got := moduleContainerDir("cloud", "aws"); got != "/opt/opencode-manager/modules/cloud/aws" {
		t.Fatalf("moduleContainerDir = %q", got)
	}
}

// TestAddModuleRunsInstallAndRecordsManifest exercises AddModule end-to-end with
// a fake driver: the install script path is exec'd and the manifest records the
// module.
func TestAddModuleRunsInstallAndRecordsManifest(t *testing.T) {
	fake := &fakeDriver{output: func(args []string) []byte { return nil }}

	workspacePath := t.TempDir()
	home := filepath.Join(workspacePath, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := Manifest{
		Name:          "demo",
		Runtime:       "docker",
		ImageName:     "opencode-manager/demo:latest",
		ContainerName: "opencode-manager-demo",
		HomeDir:       home,
	}
	summary := Summary{Manifest: manifest, Path: workspacePath}

	l := Lifecycle{driver: fake}
	mod := module.Module{Name: "hello", Category: "tools", Version: 1}
	if err := l.AddModule(context.Background(), summary, mod, map[string]string{"name": "world"}); err != nil {
		t.Fatalf("AddModule: %v", err)
	}

	// The install script must have been exec'd inside the container, at the
	// category-qualified path.
	sawInstall := false
	for _, args := range fake.gotArgs {
		if strings.Join(args, " ") == moduleContainerDir("tools", "hello")+"/install" {
			sawInstall = true
		}
	}
	if !sawInstall {
		t.Fatalf("install script not exec'd, calls: %v", fake.gotArgs)
	}

	// The manifest on disk records the module.
	saved, err := LoadManifest(filepath.Join(workspacePath, ManifestFile))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(saved.Modules) != 1 || saved.Modules[0].Name != "hello" || saved.Modules[0].Version != 1 {
		t.Fatalf("manifest did not record module: %+v", saved.Modules)
	}
	if saved.Modules[0].Values["name"] != "world" {
		t.Fatalf("manifest did not store prompt value: %+v", saved.Modules[0].Values)
	}
}
