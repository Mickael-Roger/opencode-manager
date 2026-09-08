package workspace

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/agent"
)

func TestSaveAndLoadManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), ManifestFile)
	manifest := Manifest{
		Name:          "demo",
		Runtime:       "docker",
		ImageName:     "opencode-manager/demo:latest",
		ContainerName: "opencode-manager-demo",
		HomeDir:       "/tmp/demo/home",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	if err := SaveManifest(path, manifest); err != nil {
		t.Fatalf("SaveManifest returned error: %v", err)
	}

	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}

	if loaded.Name != manifest.Name || loaded.HomeDir != manifest.HomeDir {
		t.Fatalf("loaded manifest = %#v, want %#v", loaded, manifest)
	}
}

func TestLegacyManifestDefaultsToOpenCode(t *testing.T) {
	manifest := Manifest{Name: "legacy", ContainerName: "legacy", HomeDir: "/tmp/legacy"}
	if got := manifest.EffectiveDefaultRuntime(); got != agent.OpenCode {
		t.Fatalf("default runtime = %q, want %q", got, agent.OpenCode)
	}
	if !manifest.RuntimeEnabled(agent.OpenCode) || manifest.RuntimeEnabled(agent.DeepSeek) {
		t.Fatalf("legacy runtime enablement is incorrect: %#v", manifest.EnabledRuntimeNames())
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("legacy manifest validation failed: %v", err)
	}
}

func TestManifestValidatesDefaultRuntimeIsEnabled(t *testing.T) {
	manifest := Manifest{
		Name: "demo", ContainerName: "demo", HomeDir: "/tmp/demo",
		DefaultRuntime: agent.DeepSeek,
		Runtimes:       RuntimeConfigMap{agent.OpenCode: {Enabled: true}},
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate returned nil error for disabled default runtime")
	}
}

func TestLoadManifestValidatesRequiredFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), ManifestFile)
	writeTestFile(t, path, []byte("name: broken\n"))

	if _, err := LoadManifest(path); err == nil {
		t.Fatal("LoadManifest returned nil error, want validation error")
	}
}
