package workspace

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/agent"
	"go.yaml.in/yaml/v4"
)

const ManifestFile = "workspace.yaml"

type Manifest struct {
	Name          string      `yaml:"name"`
	Runtime       string      `yaml:"runtime"`
	ImageName     string      `yaml:"imageName"`
	Image         ImageConfig `yaml:"image"`
	ContainerName string      `yaml:"containerName"`
	HomeDir       string      `yaml:"homeDir"`
	// OpenCodePort is the loopback TCP port the workspace's OpenCode server binds
	// to (and that the attach client connects to). It is assigned a unique value
	// per workspace so the servers do not collide when config.HostNetwork makes
	// every container share the host loopback. Zero in manifests written before
	// this field existed; the lifecycle assigns and persists one on next start.
	OpenCodePort int `yaml:"openCodePort,omitempty"`
	// DeepSeekPort is the loopback TCP port for this workspace's optional DSH Web
	// server. Like OpenCodePort, zero is backfilled when an older workspace starts.
	DeepSeekPort   int               `yaml:"deepSeekPort,omitempty"`
	DefaultRuntime string            `yaml:"defaultRuntime,omitempty"`
	Runtimes       RuntimeConfigMap  `yaml:"runtimes,omitempty"`
	Env            map[string]string `yaml:"env"`
	Modules        []ModuleInstance  `yaml:"modules"`
	CreatedAt      time.Time         `yaml:"createdAt"`
	UpdatedAt      time.Time         `yaml:"updatedAt"`
}

type RuntimeConfig struct {
	Enabled bool `yaml:"enabled"`
}

type RuntimeConfigMap map[string]RuntimeConfig

func (m Manifest) EffectiveDefaultRuntime() string {
	if m.DefaultRuntime != "" {
		return m.DefaultRuntime
	}
	return agent.OpenCode
}

func (m Manifest) RuntimeEnabled(name string) bool {
	if len(m.Runtimes) == 0 {
		return name == agent.OpenCode
	}
	configured, ok := m.Runtimes[name]
	return ok && configured.Enabled
}

func (m Manifest) EnabledRuntimeNames() []string {
	names := make([]string, 0, 2)
	for _, runtime := range agent.All() {
		name := runtime.Name()
		if m.RuntimeEnabled(name) {
			names = append(names, name)
		}
	}
	return names
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
	ID string `yaml:"id,omitempty"`
	// Category is the module's category directory (e.g. "cloud"), recorded at
	// install time so the container mount path (modules/<category>/<name>) is
	// known without rescanning. Empty in manifests written before categories; the
	// lifecycle falls back to looking it up from the catalog by name.
	Category string         `yaml:"category,omitempty"`
	Version  int            `yaml:"version"`
	Values   map[string]any `yaml:"values,omitempty"`
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

	registry := agent.NewRegistry()
	for name := range m.Runtimes {
		if _, err := registry.Get(name); err != nil {
			return err
		}
	}
	defaultRuntime := m.EffectiveDefaultRuntime()
	if _, err := registry.Get(defaultRuntime); err != nil {
		return err
	}
	if !m.RuntimeEnabled(defaultRuntime) {
		return fmt.Errorf("default agent runtime %q is not enabled", defaultRuntime)
	}

	return nil
}
