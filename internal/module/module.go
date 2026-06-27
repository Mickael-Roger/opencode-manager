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
// provide, relative to the module directory. They run inside the workspace
// container as the workspace user.
const (
	InstallScript   = "install"
	UninstallScript = "uninstall"
)

// ResolveScript is an optional host-side hook, relative to the module
// directory. When present, the manager runs it ON THE HOST before the container
// install script, passing the collected prompt values as OCM_* environment
// variables. It prints additional "key=value" lines that are merged into the
// values handed to the install script (but not persisted to the manifest). This
// lets a module derive container input from host-only state — e.g. reading
// ~/.kube/config to extract the selected contexts. Unlike install/uninstall it
// runs with full host access, so it is opt-in per module by simply existing.
const ResolveScript = "resolve"

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
	// RestartServer reports whether installing or removing this module changes
	// the workspace environment (~/.env) and therefore requires bouncing the
	// OpenCode server for the change to take effect. When true, an edit is only
	// allowed while the workspace is idle, since the bounce would interrupt an
	// in-flight task. When false, the module only writes its own config files
	// (e.g. ~/.kube/config, ~/.aws/credentials) that tools read live, so it can
	// be installed or removed without restarting the server — even mid-task.
	// Defaults to true (conservative) when the manifest omits it.
	RestartServer bool
	// Dir is the module's directory on the host.
	Dir string
	// Category is the module's grouping, taken from the name of the directory it
	// lives under (e.g. "cloud" for modules/cloud/aws). It organizes the module
	// browser in the TUI and is mirrored in the container mount path.
	Category string
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

// PromptByName returns a pointer to the prompt with the given name, or nil when
// the module has no such prompt.
func (m Module) PromptByName(name string) *Prompt {
	for i := range m.Prompts {
		if m.Prompts[i].Name == name {
			return &m.Prompts[i]
		}
	}
	return nil
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
	// OptionsCommand names an executable, relative to the module directory, that
	// the manager runs ON THE HOST to populate a select/multiselect prompt's
	// choices (one option per stdout line). It is an alternative to a static
	// Options list — useful when the choices depend on host state, e.g. listing
	// the user's kube contexts.
	OptionsCommand string `yaml:"optionsCommand"`
	Default        string `yaml:"default"`
}

// DynamicOptions reports whether this prompt sources its choices from a
// host-side command instead of a static Options list.
func (p Prompt) DynamicOptions() bool { return p.OptionsCommand != "" }

// Secret reports whether the prompt holds a sensitive value that the UI should
// mask. Such values are still stored in plaintext in the workspace manifest.
func (p Prompt) Secret() bool { return p.Type == PromptSecret }

// definition mirrors the on-disk module.yml. RestartServer is a pointer so an
// absent key defaults to true (restart required) rather than to Go's false.
type definition struct {
	Name          string   `yaml:"name"`
	Version       int      `yaml:"version"`
	Description   string   `yaml:"description"`
	Key           string   `yaml:"key"`
	RestartServer *bool    `yaml:"restartServer"`
	Prompts       []Prompt `yaml:"prompts"`
}

// HasResolveHook reports whether the module ships a host-side resolve script.
func (m Module) HasResolveHook() bool {
	return checkExecutable(filepath.Join(m.Dir, ResolveScript)) == nil
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
		Name:          def.Name,
		Version:       def.Version,
		Description:   def.Description,
		Prompts:       def.Prompts,
		Key:           def.Key,
		RestartServer: def.RestartServer == nil || *def.RestartServer,
		Dir:           dir,
		// The category is the name of the directory the module lives under, e.g.
		// modules/cloud/aws -> "cloud".
		Category: filepath.Base(filepath.Dir(dir)),
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
	// path (modulesRoot/<category>/<name>) is predictable from the manifest alone.
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

		if p.Type == PromptSelect || p.Type == PromptMultiSelect {
			if len(p.Options) == 0 && p.OptionsCommand == "" {
				return fmt.Errorf("prompt %q of type %q requires options or an optionsCommand", p.Name, p.Type)
			}
		} else if p.OptionsCommand != "" && p.Name != m.Key {
			// optionsCommand is normally for select/multiselect choices, but the
			// key prompt of a multi-instance module may also carry one: there it
			// lists the host accounts importable as new instances, while the prompt
			// stays a free-text field so a brand-new account can be typed in.
			return fmt.Errorf("prompt %q has optionsCommand but is not a select/multiselect or the module key", p.Name)
		}

		if p.OptionsCommand != "" {
			if err := checkExecutable(filepath.Join(m.Dir, p.OptionsCommand)); err != nil {
				return fmt.Errorf("prompt %q optionsCommand: %w", p.Name, err)
			}
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

	// The resolve hook is optional, but if a file by that name exists it must be
	// executable so the manager can run it on the host.
	if resolve := filepath.Join(m.Dir, ResolveScript); fileExists(resolve) {
		if err := checkExecutable(resolve); err != nil {
			return err
		}
	}

	return nil
}

// fileExists reports whether path names an existing file (of any kind).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

// Catalog loads every module found across the given roots. Modules live two
// levels deep, under a category directory: <root>/<category>/<module>/module.yml
// (e.g. modules/cloud/aws). Roots are scanned in order and the first definition
// of a given name wins, so earlier roots (e.g. the user's own module directory)
// can shadow built-ins. Directories without a module.yml are ignored; an invalid
// module.yml is an error. The result is sorted by category, then name.
func Catalog(roots []string) ([]Module, error) {
	byName := map[string]Module{}
	var order []string

	for _, root := range roots {
		categories, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read module directory %q: %w", root, err)
		}

		for _, category := range categories {
			if !category.IsDir() {
				continue
			}
			categoryDir := filepath.Join(root, category.Name())
			entries, err := os.ReadDir(categoryDir)
			if err != nil {
				return nil, fmt.Errorf("read module category %q: %w", categoryDir, err)
			}

			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				dir := filepath.Join(categoryDir, entry.Name())
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
	}

	mods := make([]Module, 0, len(order))
	for _, name := range order {
		mods = append(mods, byName[name])
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].Category != mods[j].Category {
			return mods[i].Category < mods[j].Category
		}
		return mods[i].Name < mods[j].Name
	})
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
