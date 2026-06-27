package workspace

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"go.yaml.in/yaml/v4"
)

// templateExt is the file extension for a saved template under TemplatesDir.
const templateExt = ".yaml"

// Template is a reusable, named set of modules-with-values. Applying a template
// when creating a workspace copies its Modules into the new workspace manifest,
// which the lifecycle's reconcile step then installs on first start. A template
// carries no workspace-specific state (no container, image, or home): it is only
// the module recipe.
type Template struct {
	Name      string           `yaml:"name"`
	Modules   []ModuleInstance `yaml:"modules"`
	CreatedAt time.Time        `yaml:"createdAt"`
	UpdatedAt time.Time        `yaml:"updatedAt"`
}

func (t Template) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("template name is required")
	}
	if SafeName(t.Name) == "" {
		return fmt.Errorf("template name %q does not contain any valid ASCII letters or digits", t.Name)
	}
	return nil
}

// TemplateRegistry stores templates as one YAML file per template under
// <workspaceRoot>/templates, mirroring how Registry stores workspaces.
type TemplateRegistry struct {
	cfg config.Config
}

func NewTemplateRegistry(cfg config.Config) TemplateRegistry {
	return TemplateRegistry{cfg: cfg}
}

func (r TemplateRegistry) TemplatesDir() string {
	return filepath.Join(r.cfg.WorkspaceRoot, "templates")
}

// templatePath returns the on-disk path for a template, keyed by its slug so the
// filename is predictable and filesystem-safe.
func (r TemplateRegistry) templatePath(name string) string {
	return filepath.Join(r.TemplatesDir(), SafeName(name)+templateExt)
}

// List loads every template in TemplatesDir, sorted by name. A missing directory
// is not an error — it just means no templates exist yet.
func (r TemplateRegistry) List() ([]Template, error) {
	entries, err := os.ReadDir(r.TemplatesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read templates directory %q: %w", r.TemplatesDir(), err)
	}

	templates := make([]Template, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), templateExt) {
			continue
		}
		template, err := loadTemplate(filepath.Join(r.TemplatesDir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}

	sort.Slice(templates, func(i, j int) bool {
		return strings.ToLower(templates[i].Name) < strings.ToLower(templates[j].Name)
	})

	slog.Debug("listed templates", "count", len(templates))
	return templates, nil
}

// Load reads a single template by name.
func (r TemplateRegistry) Load(name string) (Template, error) {
	return loadTemplate(r.templatePath(name))
}

// Exists reports whether a template with the given name (slug) is on disk.
func (r TemplateRegistry) Exists(name string) bool {
	if SafeName(name) == "" {
		return false
	}
	_, err := os.Stat(r.templatePath(name))
	return err == nil
}

// Save writes a template to disk, creating TemplatesDir on first use.
func (r TemplateRegistry) Save(t Template) error {
	if err := t.Validate(); err != nil {
		return err
	}

	data, err := yaml.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode template %q: %w", t.Name, err)
	}

	if err := os.MkdirAll(r.TemplatesDir(), 0o700); err != nil {
		return fmt.Errorf("create templates directory %q: %w", r.TemplatesDir(), err)
	}

	path := r.templatePath(t.Name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write template %q: %w", path, err)
	}

	slog.Info("saved template", "name", t.Name, "path", path, "modules", len(t.Modules))
	return nil
}

// Delete removes a template file by name. It refuses to remove anything outside
// TemplatesDir, mirroring Registry.Delete's path-safety check.
func (r TemplateRegistry) Delete(name string) error {
	if SafeName(name) == "" {
		return fmt.Errorf("template name %q is not valid", name)
	}

	templatesDir, err := filepath.Abs(r.TemplatesDir())
	if err != nil {
		return fmt.Errorf("resolve templates directory: %w", err)
	}
	templatePath, err := filepath.Abs(r.templatePath(name))
	if err != nil {
		return fmt.Errorf("resolve template path: %w", err)
	}

	rel, err := filepath.Rel(templatesDir, templatePath)
	if err != nil {
		return fmt.Errorf("check template path: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("refuse to delete path outside templates directory: %s", templatePath)
	}

	if err := os.Remove(templatePath); err != nil {
		return fmt.Errorf("delete template %q: %w", templatePath, err)
	}

	slog.Debug("removed template", "template", name, "path", templatePath)
	return nil
}

func loadTemplate(path string) (Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Template{}, fmt.Errorf("read template %q: %w", path, err)
	}

	var template Template
	if err := yaml.Unmarshal(data, &template); err != nil {
		return Template{}, fmt.Errorf("parse template %q: %w", path, err)
	}

	if err := template.Validate(); err != nil {
		return Template{}, fmt.Errorf("validate template %q: %w", path, err)
	}

	return template, nil
}
