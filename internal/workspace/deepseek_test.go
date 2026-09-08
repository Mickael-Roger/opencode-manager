package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/agent"
	"github.com/mickael-menu/opencode-manager/internal/config"
)

func TestReconcileDeepSeekProfilesInstallsFromSharedManifest(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".config", "deepseek", "profiles", "default", "package.json"), []byte(`{"dependencies":{"example":"1.0.0"}}`))
	writeTestFile(t, filepath.Join(home, ".config", "deepseek", "profiles", "default", "pnpm-lock.yaml"), []byte("workspace lockfile"))
	driver := &fakeDriver{output: func([]string) []byte { return nil }}
	lifecycle := Lifecycle{driver: driver}
	summary := Summary{Manifest: Manifest{
		Name: "demo", ContainerName: "demo", HomeDir: home,
		DefaultRuntime: agent.DeepSeek,
		Runtimes:       RuntimeConfigMap{agent.DeepSeek: {Enabled: true}},
	}}

	if err := lifecycle.reconcileDeepSeekProfiles(context.Background(), summary); err != nil {
		t.Fatalf("reconcileDeepSeekProfiles: %v", err)
	}
	want := []string{"dsh", "plugin", "--profile", "default", "install", "--no-frozen-lockfile"}
	if len(driver.gotArgs) != 2 || !reflect.DeepEqual(driver.gotArgs[1], want) {
		t.Fatalf("profile install args = %#v, want %#v", driver.gotArgs, want)
	}
	if wantPNPM := []string{"pnpm", "--version"}; !reflect.DeepEqual(driver.gotArgs[0], wantPNPM) {
		t.Fatalf("pnpm check args = %#v, want %#v", driver.gotArgs[0], wantPNPM)
	}
}

func TestEnsureStartedInstallsSynchronizedDeepSeekProfile(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	cfg := testConfig(t)
	registry := NewRegistry(cfg)
	created, err := registry.CreateWithOptions("demo", CreateOptions{DefaultRuntime: agent.DeepSeek})
	if err != nil {
		t.Fatal(err)
	}
	source, err := config.DeepSeekDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "profiles", "default"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "profiles", "web", "package.json"), []byte(`{"name":"web","private":true}`))

	driver := &fakeDriver{output: func([]string) []byte { return nil }}
	lifecycle := Lifecycle{cfg: cfg, registry: registry, driver: driver}
	if err := lifecycle.EnsureStarted(context.Background(), Summary{Manifest: created.Manifest, Path: created.Path}); err != nil {
		t.Fatalf("EnsureStarted: %v", err)
	}
	want := []string{"dsh", "plugin", "--profile", "web", "install", "--no-frozen-lockfile"}
	var found bool
	for _, args := range driver.gotArgs {
		if reflect.DeepEqual(args, want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("EnsureStarted did not install the synchronized DSH profile: %#v", driver.gotArgs)
	}
}
