// Package module loads workspace modules. A module is a self-contained
// directory holding a declarative module.yml plus an install/uninstall
// executable that does all the work (install packages, write files into the
// workspace home, export environment variables). The manager never interprets
// what a module does; it only collects the module's declared prompt values and
// runs its executables inside the workspace container.
package module

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v4"
)

// ManifestFile is the per-module declarative file name.
const ManifestFile = "module.yml"

// InstallScript and UninstallScript are the executables every module must
// provide, relative to the module directory.
const (
	InstallScript   = "install"
	UninstallScript = "uninstall"
)

// Prompt types.
const (
	PromptString      = "string"
	PromptSecret      = "secret"
	PromptBool        = "bool"
	PromptSelect      = "select"
	PromptMultiSelect = "multiselect"
)

// Module is a loaded module definition.
type Module struct {
	Name        string
	Version     int
	Description string
	Prompts     []Prompt
	// Key, when set, names the prompt whose value identifies one instance of a
	// multi-instance module. Such a module can be installed several times in the
	// same workspace (e.g. one ssh module per host), each entry added and removed
	// independently. When empty the module is a singleton.
	Key string
	// Dir is the module's directory on the host.
	Dir string
}

// Multi reports whether the module can be installed as multiple independent
// instances, distinguished by the value of its Key prompt.
func (m Module) Multi() bool { return m.Key != "" }

// InstanceID returns the manifest/marker identity for an installation of this
// module with the given prompt values. Singleton modules use their name, so a
// second install replaces the first; multi-instance modules append the key
// prompt value, so each distinct key is a separate, independently removable
// entry.
func (m Module) InstanceID(values map[string]string) string {
	if m.Key == "" {
		return m.Name
	}
	return m.Name + ":" + values[m.Key]
}

// Prompt is a value the manager collects from the user before installing a
// module. The collected value is passed to the install/uninstall scripts as an
// OCM_<NAME> environment variable.
type Prompt struct {
	Name     string   `yaml:"name"`
	Label    string   `yaml:"label"`
	Type     string   `yaml:"type"`
	Required bool     `yaml:"required"`
	Options  []string `yaml:"options"`
	Default  string   `yaml:"default"`
}

// Secret reports whether the prompt holds a sensitive value that the UI should
// mask. Such values are still stored in plaintext in the workspace manifest.
func (p Prompt) Secret() bool { return p.Type == PromptSecret }

// definition mirrors the on-disk module.yml.
type definition struct {
	Name        string   `yaml:"name"`
	Version     int      `yaml:"version"`
	Description string   `yaml:"description"`
	Key         string   `yaml:"key"`
	Prompts     []Prompt `yaml:"prompts"`
}

// Load reads and validates the module in dir.
func Load(dir string) (Module, error) {
	path := filepath.Join(dir, ManifestFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Module{}, fmt.Errorf("read module %q: %w", path, err)
	}

	var def definition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return Module{}, fmt.Errorf("parse module %q: %w", path, err)
	}

	mod := Module{
		Name:        def.Name,
		Version:     def.Version,
		Description: def.Description,
		Prompts:     def.Prompts,
		Key:         def.Key,
		Dir:         dir,
	}

	if err := mod.validate(); err != nil {
		return Module{}, fmt.Errorf("invalid module %q: %w", path, err)
	}

	return mod, nil
}

func (m Module) validate() error {
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	// The module's directory base must equal its name so the container mount
	// path (modulesRoot/<name>) is predictable from the manifest alone.
	if base := filepath.Base(m.Dir); base != m.Name {
		return fmt.Errorf("name %q must match directory name %q", m.Name, base)
	}
	if !isSlug(m.Name) {
		return fmt.Errorf("name %q must contain only lowercase letters, digits, and dashes", m.Name)
	}
	if m.Version < 1 {
		return fmt.Errorf("version must be >= 1")
	}

	seen := map[string]bool{}
	for _, p := range m.Prompts {
		if p.Name == "" {
			return fmt.Errorf("prompt name is required")
		}
		if !isEnvName(p.Name) {
			return fmt.Errorf("prompt name %q must contain only letters, digits, and underscores", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate prompt name %q", p.Name)
		}
		seen[p.Name] = true

		switch p.Type {
		case PromptString, PromptSecret, PromptBool, PromptSelect, PromptMultiSelect:
		case "":
			return fmt.Errorf("prompt %q is missing a type", p.Name)
		default:
			return fmt.Errorf("prompt %q has unknown type %q", p.Name, p.Type)
		}

		if (p.Type == PromptSelect || p.Type == PromptMultiSelect) && len(p.Options) == 0 {
			return fmt.Errorf("prompt %q of type %q requires options", p.Name, p.Type)
		}
	}

	// A multi-instance key must name a required prompt: its value identifies the
	// instance, so it has to exist and never be empty.
	if m.Key != "" {
		var key *Prompt
		for i := range m.Prompts {
			if m.Prompts[i].Name == m.Key {
				key = &m.Prompts[i]
				break
			}
		}
		if key == nil {
			return fmt.Errorf("key %q does not match any prompt", m.Key)
		}
		if !key.Required {
			return fmt.Errorf("key prompt %q must be required", m.Key)
		}
		if key.Secret() {
			return fmt.Errorf("key prompt %q must not be a secret", m.Key)
		}
	}

	if err := checkExecutable(filepath.Join(m.Dir, InstallScript)); err != nil {
		return err
	}
	if err := checkExecutable(filepath.Join(m.Dir, UninstallScript)); err != nil {
		return err
	}

	return nil
}

// checkExecutable verifies the script exists, is a regular file, and has an
// executable bit set (so the bind-mounted copy can be run inside the container).
func checkExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("missing %s script", filepath.Base(path))
		}
		return fmt.Errorf("check %s script: %w", filepath.Base(path), err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s script is a directory", filepath.Base(path))
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s script is not executable (chmod +x it)", filepath.Base(path))
	}
	return nil
}

// Catalog loads every module found across the given roots. Roots are scanned in
// order and the first definition of a given name wins, so earlier roots (e.g.
// the user's own module directory) can shadow built-ins. Directories without a
// module.yml are ignored; an invalid module.yml is an error.
func Catalog(roots []string) ([]Module, error) {
	byName := map[string]Module{}
	var order []string

	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read module directory %q: %w", root, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if _, err := os.Stat(filepath.Join(dir, ManifestFile)); err != nil {
				continue
			}
			mod, err := Load(dir)
			if err != nil {
				return nil, err
			}
			if _, ok := byName[mod.Name]; ok {
				continue
			}
			byName[mod.Name] = mod
			order = append(order, mod.Name)
		}
	}

	mods := make([]Module, 0, len(order))
	for _, name := range order {
		mods = append(mods, byName[name])
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Name < mods[j].Name })
	return mods, nil
}

func isSlug(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// EnvVarName is the environment variable a prompt value is passed through to the
// install/uninstall scripts, e.g. prompt "profile" -> OCM_PROFILE.
func EnvVarName(promptName string) string {
	return "OCM_" + strings.ToUpper(promptName)
}
