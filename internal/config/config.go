package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v4"
)

const (
	RuntimeDocker = "docker"
	RuntimePodman = "podman"
)

const (
	LogLevelDebug   = "debug"
	LogLevelInfo    = "info"
	LogLevelWarning = "warning"
	LogLevelError   = "error"
)

// BaseImageRepository is the repository of the published, prebuilt base image
// (without a tag). Any tag of it (:latest, :dev, :X.Y.Z, a digest) already
// contains the full tooling (uv, OpenCode, tokscale, and the manager
// scripts). It is built and published by .github/workflows from the Dockerfile in
// internal/runtime/buildcontext.
const BaseImageRepository = "docker.io/mroger78/ocm-base"

// DefaultBaseImage is the base image used unless the user overrides
// baseImage.name. A default workspace pulls it instead of building a base image
// locally.
const DefaultBaseImage = BaseImageRepository + ":latest"

// IsManagedBaseImage reports whether name refers to the published ocm-base image,
// at any tag or digest. Such an image already embeds all required tools, so it
// must only be pulled — never rebuilt and never have tools (re)installed on top.
// The optional "docker.io/" registry prefix is normalized so both
// "mroger78/ocm-base:dev" and "docker.io/mroger78/ocm-base:dev" match.
func IsManagedBaseImage(name string) bool {
	repo := name
	if i := strings.IndexByte(repo, '@'); i >= 0 { // strip @digest
		repo = repo[:i]
	}
	// Strip a :tag, but not a registry port (a colon before the last slash).
	if i := strings.LastIndexByte(repo, ':'); i > strings.LastIndexByte(repo, '/') {
		repo = repo[:i]
	}
	return strings.TrimPrefix(repo, "docker.io/") == strings.TrimPrefix(BaseImageRepository, "docker.io/")
}

type Config struct {
	WorkspaceRoot        string `yaml:"workspaceRoot"`
	Runtime              string `yaml:"runtime"`
	UseLocalOpenCodeAuth bool   `yaml:"useLocalOpenCodeAuth"`
	// HostNetwork shares the host's network namespace with each workspace
	// container (docker/podman `--network host`) instead of giving it an isolated
	// one. Off by default. Because every workspace then shares the host loopback,
	// each workspace's OpenCode server is bound to its own port (see
	// workspace.Manifest.OpenCodePort) so the servers do not collide.
	HostNetwork bool `yaml:"hostNetwork"`
	// RuntimeArgs are extra flags passed verbatim to the `docker`/`podman create`
	// command for every workspace container, inserted just before the image name.
	// Optional escape hatch for options the manager does not model natively (e.g.
	// `--dns`, `--add-host`, `--device`, extra `--volume`s) without code changes.
	RuntimeArgs []string        `yaml:"runtimeArgs"`
	LogLevel    string          `yaml:"logLevel"`
	BaseImage   BaseImageConfig `yaml:"baseImage"`
	ModuleDirs  []string        `yaml:"moduleDirs"`
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

// DataDir returns the opencode-manager data directory
// (~/.local/share/opencode-manager). It holds workspaces and logs.
func DataDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".local", "share", "opencode-manager"), nil
}

// LogDir returns the directory where log files are stored
// (~/.local/share/opencode-manager/logs).
func LogDir() (string, error) {
	dataDir, err := DataDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dataDir, "logs"), nil
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

	dataDir, err := DataDir()
	if err != nil {
		return Config{}, err
	}

	return Config{
		WorkspaceRoot: dataDir,
		Runtime:       RuntimeDocker,
		LogLevel:      LogLevelWarning,
		BaseImage: BaseImageConfig{
			Name: DefaultBaseImage,
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
			// Logging is configured from this very config, so it may not be wired
			// up yet on the first load; these messages surface on later reloads.
			slog.Debug("config file not found, using defaults", "path", path)
			return cfg, cfg.Validate()
		}

		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	slog.Debug("loaded config", "path", path, "runtime", cfg.Runtime, "logLevel", cfg.LogLevel, "workspaceRoot", cfg.WorkspaceRoot)
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.WorkspaceRoot == "" {
		return errors.New("workspaceRoot is required")
	}

	if c.Runtime != RuntimeDocker && c.Runtime != RuntimePodman {
		return fmt.Errorf("runtime must be %q or %q", RuntimeDocker, RuntimePodman)
	}

	switch c.LogLevel {
	case LogLevelDebug, LogLevelInfo, LogLevelWarning, LogLevelError:
	default:
		return fmt.Errorf("logLevel must be one of %q, %q, %q, %q", LogLevelDebug, LogLevelInfo, LogLevelWarning, LogLevelError)
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

	for _, arg := range c.RuntimeArgs {
		if arg == "" {
			return errors.New("runtimeArgs cannot contain empty arguments")
		}
	}

	return nil
}
