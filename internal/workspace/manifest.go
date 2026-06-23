package workspace

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v4"
)

const ManifestFile = "workspace.yaml"

type Manifest struct {
	Name          string            `yaml:"name"`
	Runtime       string            `yaml:"runtime"`
	ImageName     string            `yaml:"imageName"`
	Image         ImageConfig       `yaml:"image"`
	ContainerName string            `yaml:"containerName"`
	HomeDir       string            `yaml:"homeDir"`
	Env           map[string]string `yaml:"env"`
	Modules       []ModuleInstance  `yaml:"modules"`
	CreatedAt     time.Time         `yaml:"createdAt"`
	UpdatedAt     time.Time         `yaml:"updatedAt"`
}

type ImageConfig struct {
	BaseImage string   `yaml:"baseImage"`
	Packages  []string `yaml:"packages"`
	Commands  []string `yaml:"commands"`
}

type ModuleInstance struct {
	Name string `yaml:"name"`
	// ID is the instance identity. For singleton modules it equals Name; for
	// multi-instance modules it is "name:keyvalue", letting several entries of
	// the same module coexist and be removed independently. Empty in manifests
	// written before multi-instance support, where it falls back to Name.
	ID      string         `yaml:"id,omitempty"`
	Version int            `yaml:"version"`
	Values  map[string]any `yaml:"values,omitempty"`
}

// InstanceID returns the stable identity of this installed instance, falling
// back to the module name for singletons and pre-multi-instance manifests.
func (mi ModuleInstance) InstanceID() string {
	if mi.ID != "" {
		return mi.ID
	}
	return mi.Name
}

// Value returns a named prompt value as a string, e.g. the key prompt used to
// label a multi-instance entry.
func (mi ModuleInstance) Value(name string) string {
	return valueToString(mi.Values[name])
}

func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %q: %w", path, err)
	}

	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %q: %w", path, err)
	}

	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("validate manifest %q: %w", path, err)
	}

	return manifest, nil
}

func SaveManifest(path string, manifest Manifest) error {
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("validate manifest %q: %w", path, err)
	}

	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode manifest %q: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create manifest directory %q: %w", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write manifest %q: %w", path, err)
	}

	slog.Debug("saved workspace manifest", "name", manifest.Name, "path", path)
	return nil
}

func (m Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest name is required")
	}

	if m.ContainerName == "" {
		return fmt.Errorf("manifest containerName is required")
	}

	if m.HomeDir == "" {
		return fmt.Errorf("manifest homeDir is required")
	}

	return nil
}
