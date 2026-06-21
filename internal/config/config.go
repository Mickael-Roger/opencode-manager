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
	WorkspaceRoot string          `yaml:"workspaceRoot"`
	Runtime       string          `yaml:"runtime"`
	BaseImage     BaseImageConfig `yaml:"baseImage"`
	ModuleDirs    []string        `yaml:"moduleDirs"`
}

type BaseImageConfig struct {
	Name     string   `yaml:"name"`
	Packages []string `yaml:"packages"`
	Commands []string `yaml:"commands"`
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}

	return filepath.Join(dir, "opencode-manager", "config.yaml"), nil
}

func Default() (Config, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Config{}, fmt.Errorf("find user config directory: %w", err)
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
		ModuleDirs: []string{filepath.Join(configDir, "opencode-manager", "modules")},
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
