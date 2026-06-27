package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTemplateSaveListLoadRoundTrip(t *testing.T) {
	registry := NewTemplateRegistry(testConfig(t))

	want := Template{
		Name: "Go Dev",
		Modules: []ModuleInstance{
			{Name: "git", ID: "git", Category: "tools", Version: 1},
			{
				Name:     "aws",
				ID:       "aws:prod",
				Category: "cloud",
				Version:  2,
				Values:   map[string]any{"profile": "prod"},
			},
		},
	}

	if err := registry.Save(want); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	templates, err := registry.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(templates) != 1 || templates[0].Name != "Go Dev" {
		t.Fatalf("templates = %#v, want one named Go Dev", templates)
	}

	loaded, err := registry.Load("Go Dev")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded.Modules) != 2 {
		t.Fatalf("loaded modules = %#v, want 2", loaded.Modules)
	}
	if loaded.Modules[1].InstanceID() != "aws:prod" {
		t.Fatalf("instance id = %q, want aws:prod", loaded.Modules[1].InstanceID())
	}
	if loaded.Modules[1].Value("profile") != "prod" {
		t.Fatalf("profile value = %q, want prod", loaded.Modules[1].Value("profile"))
	}
}

func TestTemplateListSortsAndHandlesMissingDir(t *testing.T) {
	registry := NewTemplateRegistry(testConfig(t))

	// No templates directory yet -> empty, not an error.
	templates, err := registry.List()
	if err != nil {
		t.Fatalf("List on missing dir returned error: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("templates = %#v, want empty", templates)
	}

	for _, name := range []string{"zeta", "alpha"} {
		if err := registry.Save(Template{Name: name}); err != nil {
			t.Fatalf("Save %q returned error: %v", name, err)
		}
	}

	templates, err = registry.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(templates) != 2 || templates[0].Name != "alpha" || templates[1].Name != "zeta" {
		t.Fatalf("templates = %#v, want alpha then zeta", templates)
	}
}

func TestTemplateDeleteRemovesFile(t *testing.T) {
	registry := NewTemplateRegistry(testConfig(t))
	if err := registry.Save(Template{Name: "scratch"}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if !registry.Exists("scratch") {
		t.Fatal("Exists = false after Save, want true")
	}

	if err := registry.Delete("scratch"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if registry.Exists("scratch") {
		t.Fatal("Exists = true after Delete, want false")
	}
	if _, err := os.Stat(registry.templatePath("scratch")); !os.IsNotExist(err) {
		t.Fatalf("template file still exists or stat failed unexpectedly: %v", err)
	}
}

func TestTemplateDeleteRejectsInvalidName(t *testing.T) {
	registry := NewTemplateRegistry(testConfig(t))
	if err := registry.Delete("!!!"); err == nil {
		t.Fatal("Delete returned nil error, want invalid name error")
	}
}

func TestTemplateSaveRejectsUnsluggableName(t *testing.T) {
	registry := NewTemplateRegistry(testConfig(t))
	if err := registry.Save(Template{Name: "!!!"}); err == nil {
		t.Fatal("Save returned nil error, want validation error")
	}
}

func TestTemplateValidate(t *testing.T) {
	if err := (Template{Name: "ok"}).Validate(); err != nil {
		t.Fatalf("Validate(ok) returned error: %v", err)
	}
	if err := (Template{Name: ""}).Validate(); err == nil {
		t.Fatal("Validate(empty) returned nil error, want error")
	}
	if err := (Template{Name: "!!!"}).Validate(); err == nil {
		t.Fatal("Validate(unsluggable) returned nil error, want error")
	}
}

func TestTemplatePathStaysWithinTemplatesDir(t *testing.T) {
	registry := NewTemplateRegistry(testConfig(t))
	got := registry.templatePath("Go Dev")
	want := filepath.Join(registry.TemplatesDir(), "go-dev.yaml")
	if got != want {
		t.Fatalf("templatePath = %q, want %q", got, want)
	}
}
