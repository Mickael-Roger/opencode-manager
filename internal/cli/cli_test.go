package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// testConfig returns a Config rooted at a temp dir so commands never touch the
// real workspace/template store.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("config.Default: %v", err)
	}
	cfg.WorkspaceRoot = t.TempDir()
	// Point module discovery at an empty dir so Catalog is deterministic.
	cfg.ModuleDirs = []string{t.TempDir()}
	return cfg
}

// run executes the command tree with args and returns captured stdout, stderr,
// and the execution error.
func run(t *testing.T, cfg config.Config, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCommand(cfg)
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestUnknownCommandIsAnError(t *testing.T) {
	cfg := testConfig(t)
	_, _, err := run(t, cfg, "bogus")
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("error = %q, want it to mention unknown command", err)
	}
}

func TestWorkspacesCreateAndList(t *testing.T) {
	cfg := testConfig(t)

	if out, _, err := run(t, cfg, "workspaces", "create", "My Project"); err != nil {
		t.Fatalf("create: %v", err)
	} else if !strings.Contains(out, "My Project") {
		t.Fatalf("create output = %q, want it to mention the workspace", out)
	}

	out, _, err := run(t, cfg, "workspaces", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "My Project") {
		t.Fatalf("list output = %q, want it to include My Project", out)
	}
}

func TestWorkspacesListJSONEmpty(t *testing.T) {
	cfg := testConfig(t)
	out, _, err := run(t, cfg, "workspaces", "list", "-o", "json")
	if err != nil {
		t.Fatalf("list json: %v", err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list json = %q, want []", out)
	}
}

func TestWorkspacesGetNotFound(t *testing.T) {
	cfg := testConfig(t)
	_, _, err := run(t, cfg, "workspaces", "get", "missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("get missing err = %v, want not found", err)
	}
}

func TestInvalidOutputFormat(t *testing.T) {
	cfg := testConfig(t)
	_, _, err := run(t, cfg, "workspaces", "list", "-o", "xml")
	if err == nil || !strings.Contains(err.Error(), "invalid --output") {
		t.Fatalf("err = %v, want invalid --output", err)
	}
}

func TestTemplatesListEmptyAndPopulated(t *testing.T) {
	cfg := testConfig(t)

	if out, _, err := run(t, cfg, "templates", "list"); err != nil {
		t.Fatalf("templates list: %v", err)
	} else if !strings.Contains(out, "No templates") {
		t.Fatalf("empty templates list = %q", out)
	}

	if err := workspace.NewTemplateRegistry(cfg).Save(workspace.Template{Name: "web"}); err != nil {
		t.Fatalf("save template: %v", err)
	}
	out, _, err := run(t, cfg, "templates", "list")
	if err != nil {
		t.Fatalf("templates list: %v", err)
	}
	if !strings.Contains(out, "web") {
		t.Fatalf("templates list = %q, want web", out)
	}
}

func TestModulesListEmptyCatalog(t *testing.T) {
	cfg := testConfig(t)
	out, _, err := run(t, cfg, "modules", "list")
	if err != nil {
		t.Fatalf("modules list: %v", err)
	}
	if !strings.Contains(out, "No modules") {
		t.Fatalf("modules list = %q, want No modules", out)
	}
}

func TestModulesRemoveRequiresWorkspace(t *testing.T) {
	cfg := testConfig(t)
	_, _, err := run(t, cfg, "modules", "remove", "git")
	if err == nil || !strings.Contains(err.Error(), "--workspace") {
		t.Fatalf("err = %v, want --workspace required", err)
	}
}

func TestVersionCommand(t *testing.T) {
	cfg := testConfig(t)
	out, _, err := run(t, cfg, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out, "opencode-manager") {
		t.Fatalf("version output = %q", out)
	}
}

func TestFindWorkspaceMatchesNameOrSlug(t *testing.T) {
	cfg := testConfig(t)
	if _, err := workspace.NewRegistry(cfg).Create("My Project"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, name := range []string{"My Project", "my-project"} {
		s, err := findWorkspace(cfg, name)
		if err != nil {
			t.Fatalf("findWorkspace(%q): %v", name, err)
		}
		if s.Manifest.Name != "My Project" {
			t.Fatalf("findWorkspace(%q) = %q, want My Project", name, s.Manifest.Name)
		}
	}
}
