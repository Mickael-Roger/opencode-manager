package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"
)

const (
	RuntimeDocker = "docker"
	RuntimePodman = "podman"
)

type Config struct {
	WorkspaceRoot        string          `yaml:"workspaceRoot"`
	Runtime              string          `yaml:"runtime"`
	UseLocalOpenCodeAuth bool            `yaml:"useLocalOpenCodeAuth"`
	BaseImage            BaseImageConfig `yaml:"baseImage"`
	ModuleDirs           []string        `yaml:"moduleDirs"`
}

type BaseImageConfig struct {
	Name     string   `yaml:"name"`
	Packages []string `yaml:"packages"`
	Commands []string `yaml:"commands"`
}

func DefaultPath() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "config.yaml"), nil
}

// GlobalDir returns the opencode-manager configuration directory
// (~/.config/opencode-manager). It holds config.yaml as well as the OpenCode
// templates (AGENTS.md, opencode.json, agents/, commands/, plugins/, skills/)
// mounted read-only into every workspace container.
func GlobalDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}

	return filepath.Join(dir, "opencode-manager"), nil
}

// GlobalTemplateDirs are the OpenCode template subdirectories created in the
// global config directory and mounted read-only into each workspace.
var GlobalTemplateDirs = []string{"agents", "commands", "plugins", "skills"}

// defaultOpenCodeJSON is the minimal valid content seeded into the global
// opencode.json when it does not exist yet. OpenCode requires a non-empty
// config, so it cannot be left blank like AGENTS.md.
const defaultOpenCodeJSON = "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n"

// EnsureGlobalConfig creates the global config directory and the OpenCode
// template files/directories if they are missing. Existing files are never
// overwritten so user edits are preserved.
func EnsureGlobalConfig() error {
	dir, err := GlobalDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create global config directory %q: %w", dir, err)
	}

	for _, name := range GlobalTemplateDirs {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create global template directory %q: %w", path, err)
		}
	}

	files := map[string]string{
		"AGENTS.md":     "",
		"opencode.json": defaultOpenCodeJSON,
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := ensureFile(path, content); err != nil {
			return err
		}
	}

	return nil
}

// ensureFile writes content to path only if the file does not already exist.
func ensureFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check global template file %q: %w", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write global template file %q: %w", path, err)
	}

	return nil
}

func Default() (Config, error) {
	globalDir, err := GlobalDir()
	if err != nil {
		return Config{}, err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("find user home directory: %w", err)
	}

	dataDir := filepath.Join(homeDir, ".local", "share", "opencode-manager")

	return Config{
		WorkspaceRoot: dataDir,
		Runtime:       RuntimeDocker,
		BaseImage: BaseImageConfig{
			Name: "debian:stable-slim",
		},
		ModuleDirs: []string{filepath.Join(globalDir, "modules")},
	}, nil
}

func Load(path string) (Config, error) {
	cfg, err := Default()
	if err != nil {
		return Config{}, err
	}

	if path == "" {
		path, err = DefaultPath()
		if err != nil {
			return Config{}, err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, cfg.Validate()
		}

		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.WorkspaceRoot == "" {
		return errors.New("workspaceRoot is required")
	}

	if c.Runtime != RuntimeDocker && c.Runtime != RuntimePodman {
		return fmt.Errorf("runtime must be %q or %q", RuntimeDocker, RuntimePodman)
	}

	if c.BaseImage.Name == "" {
		return errors.New("baseImage.name is required")
	}

	for _, pkg := range c.BaseImage.Packages {
		if pkg == "" {
			return errors.New("baseImage.packages cannot contain empty package names")
		}
	}

	for _, command := range c.BaseImage.Commands {
		if command == "" {
			return errors.New("baseImage.commands cannot contain empty commands")
		}
	}

	return nil
}
