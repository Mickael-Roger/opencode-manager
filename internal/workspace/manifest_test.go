package workspace

import (
	"path/filepath"
	"testing"
	"time"
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

func TestLoadManifestValidatesRequiredFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), ManifestFile)
	writeTestFile(t, path, []byte("name: broken\n"))

	if _, err := LoadManifest(path); err == nil {
		t.Fatal("LoadManifest returned nil error, want validation error")
	}
}
