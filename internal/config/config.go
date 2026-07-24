package config

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	// ExtraCACertificates are optional absolute paths to host CA certificates
	// installed into every workspace container's system trust store.
	ExtraCACertificates CACertificates `yaml:"extraCACertificate"`
	// WorkspaceEnv defines environment variables passed to every workspace
	// container. Values may be literal or {env:HOST_VARIABLE} references.
	WorkspaceEnv map[string]string `yaml:"workspaceEnv"`
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
	// WorkspacePostCreateCommands are shell commands run inside a workspace
	// container the first time it is started (after modules are installed). Each
	// runs as the workspace user in the project directory with ~/.env sourced.
	// Optional, one-shot per workspace — e.g. clone a repo or install
	// dependencies. A failing command is logged and does not block startup.
	WorkspacePostCreateCommands []string `yaml:"workspacePostCreateCommands"`
	// WorkspacePreDeleteCommands are shell commands run inside a workspace
	// container just before the workspace is deleted, while it still exists (the
	// container is started first if stopped). Each runs as the workspace user in
	// the project directory with ~/.env sourced. Optional teardown — e.g. push
	// pending commits or deregister. A failing command is logged and does not
	// block deletion.
	WorkspacePreDeleteCommands []string `yaml:"workspacePreDeleteCommands"`
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ResolveWorkspaceEnv resolves host environment references without persisting
// their values to workspace manifests.
func (c Config) ResolveWorkspaceEnv() (map[string]string, error) {
	resolved := make(map[string]string, len(c.WorkspaceEnv))
	for key, value := range c.WorkspaceEnv {
		if strings.HasPrefix(value, "{env:") {
			if !strings.HasSuffix(value, "}") {
				return nil, fmt.Errorf("workspaceEnv.%s has an invalid host environment reference", key)
			}
			hostKey := strings.TrimSuffix(strings.TrimPrefix(value, "{env:"), "}")
			if !environmentName.MatchString(hostKey) {
				return nil, fmt.Errorf("workspaceEnv.%s has an invalid host environment variable name", key)
			}
			hostValue, ok := os.LookupEnv(hostKey)
			if !ok {
				return nil, fmt.Errorf("workspaceEnv.%s requires host environment variable %q", key, hostKey)
			}
			resolved[key] = hostValue
			continue
		}
		resolved[key] = value
	}
	return resolved, nil
}

// WorkspaceEnvKeys returns the stable key list used to detect removed global
// environment variables on existing containers without exposing their values.
func (c Config) WorkspaceEnvKeys() string {
	keys := make([]string, 0, len(c.WorkspaceEnv))
	for key := range c.WorkspaceEnv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// CACertificates accepts the current YAML list form and the single-string form
// used by existing configs before multiple certificates were supported.
type CACertificates []string

func (c *CACertificates) UnmarshalYAML(unmarshal func(any) error) error {
	var paths []string
	if err := unmarshal(&paths); err == nil {
		*c = paths
		return nil
	}

	var path string
	if err := unmarshal(&path); err != nil {
		return err
	}
	if path == "" {
		*c = nil
		return nil
	}
	*c = []string{path}
	return nil
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
// (~/.config/opencode-manager). It holds config.yaml, modules, and the shared
// OpenCode configuration directory.
func GlobalDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}

	return filepath.Join(dir, "opencode-manager"), nil
}

// OpenCodeDir returns the host-side OpenCode configuration that is copied into
// each workspace. It is deliberately separate from manager configuration.
func OpenCodeDir() (string, error) {
	dir, err := GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "opencode"), nil
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

// GlobalTemplateDirs are conventional OpenCode config subdirectories created in
// the shared OpenCode configuration directory.
var GlobalTemplateDirs = []string{"agents", "commands", "plugins", "skills"}

// defaultOpenCodeJSON is the minimal valid content seeded into the global
// opencode.json when it does not exist yet. OpenCode requires a non-empty
// config, so it cannot be left blank like AGENTS.md.
const defaultOpenCodeJSON = "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n"

// EnsureGlobalConfig creates the global config directory and shared OpenCode
// files/directories if they are missing. Existing files are never overwritten.
func EnsureGlobalConfig() error {
	dir, err := GlobalDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create global config directory %q: %w", dir, err)
	}

	openCodeDir := filepath.Join(dir, "opencode")
	if err := migrateLegacyOpenCodeTemplates(dir, openCodeDir); err != nil {
		return err
	}
	if err := os.MkdirAll(openCodeDir, 0o700); err != nil {
		return fmt.Errorf("create shared OpenCode directory %q: %w", openCodeDir, err)
	}

	for _, name := range GlobalTemplateDirs {
		path := filepath.Join(openCodeDir, name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create global template directory %q: %w", path, err)
		}
	}

	files := map[string]string{
		"AGENTS.md":     "",
		"opencode.json": defaultOpenCodeJSON,
	}
	for name, content := range files {
		path := filepath.Join(openCodeDir, name)
		if err := ensureFile(path, content); err != nil {
			return err
		}
	}

	return nil
}

// migrateLegacyOpenCodeTemplates moves the former fixed root template layout
// into opencode/. It only runs when the new location is absent or empty so an
// existing shared config is never overwritten.
func migrateLegacyOpenCodeTemplates(globalDir, openCodeDir string) error {
	entries, err := os.ReadDir(openCodeDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read shared OpenCode directory %q: %w", openCodeDir, err)
	}
	if err == nil && len(entries) != 0 {
		return nil
	}
	if err := os.MkdirAll(openCodeDir, 0o700); err != nil {
		return fmt.Errorf("create shared OpenCode directory %q: %w", openCodeDir, err)
	}

	for _, name := range append([]string{"AGENTS.md", "opencode.json"}, GlobalTemplateDirs...) {
		legacy := filepath.Join(globalDir, name)
		if _, err := os.Lstat(legacy); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("check legacy OpenCode template %q: %w", legacy, err)
		}
		if err := os.Rename(legacy, filepath.Join(openCodeDir, name)); err != nil {
			return fmt.Errorf("migrate legacy OpenCode template %q: %w", legacy, err)
		}
	}
	return nil
}

// ensureFile writes content to path when the file does not already exist, and in
// all cases makes the file world-readable. Workspace sync writes private copies,
// but readable source files remain convenient for host-side OpenCode tooling.
func ensureFile(path, content string) error {
	if info, err := os.Stat(path); err == nil {
		if perm := info.Mode().Perm(); perm&0o044 != 0o044 {
			if err := os.Chmod(path, perm|0o044); err != nil {
				return fmt.Errorf("make global template file %q readable: %w", path, err)
			}
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check global template file %q: %w", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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

	for key, value := range c.WorkspaceEnv {
		if !environmentName.MatchString(key) {
			return fmt.Errorf("workspaceEnv key %q is not a valid environment variable name", key)
		}
		if key == "HOME" || key == "TERM" || strings.HasPrefix(key, "OCM_") {
			return fmt.Errorf("workspaceEnv key %q is reserved by the manager", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("workspaceEnv.%s cannot contain NUL", key)
		}
		if strings.HasPrefix(value, "{env:") && (!strings.HasSuffix(value, "}") || !environmentName.MatchString(strings.TrimSuffix(strings.TrimPrefix(value, "{env:"), "}"))) {
			return fmt.Errorf("workspaceEnv.%s has an invalid host environment reference", key)
		}
	}

	for _, path := range c.ExtraCACertificates {
		if path == "" {
			return errors.New("extraCACertificate cannot contain empty paths")
		}
		if !filepath.IsAbs(path) {
			return errors.New("extraCACertificate paths must be absolute")
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("check extra CA certificate %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("extra CA certificate %q must be a regular file", path)
		}
		if err := ValidateCACertificate(path); err != nil {
			return err
		}
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

	for _, command := range c.WorkspacePostCreateCommands {
		if strings.TrimSpace(command) == "" {
			return errors.New("workspacePostCreateCommands cannot contain empty commands")
		}
	}

	for _, command := range c.WorkspacePreDeleteCommands {
		if strings.TrimSpace(command) == "" {
			return errors.New("workspacePreDeleteCommands cannot contain empty commands")
		}
	}

	return nil
}

// ValidateCACertificate verifies that path contains one or more PEM-encoded CA
// certificates suitable for installation into the container trust store.
func ValidateCACertificate(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read extra CA certificate %q: %w", path, err)
	}

	count := 0
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return fmt.Errorf("extra CA certificate %q must contain PEM-encoded X.509 certificates", path)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("parse extra CA certificate %q: %w", path, err)
		}
		if !certificate.IsCA {
			return fmt.Errorf("extra CA certificate %q must contain CA certificates", path)
		}
		count++
		data = rest
	}
	if count == 0 {
		return fmt.Errorf("extra CA certificate %q must contain PEM-encoded X.509 certificates", path)
	}

	return nil
}
