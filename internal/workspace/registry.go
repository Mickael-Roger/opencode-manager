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
)

type Registry struct {
	cfg config.Config
}

type Summary struct {
	Manifest Manifest
	Path     string
}

type CreateResult struct {
	Manifest Manifest
	Path     string
}

func NewRegistry(cfg config.Config) Registry {
	return Registry{cfg: cfg}
}

func (r Registry) WorkspacesDir() string {
	return filepath.Join(r.cfg.WorkspaceRoot, "workspaces")
}

func (r Registry) WorkspaceDir(name string) string {
	return filepath.Join(r.WorkspacesDir(), name)
}

func (r Registry) List() ([]Summary, error) {
	entries, err := os.ReadDir(r.WorkspacesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("read workspaces directory %q: %w", r.WorkspacesDir(), err)
	}

	workspaces := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workspacePath := filepath.Join(r.WorkspacesDir(), entry.Name())
		manifest, err := LoadManifest(filepath.Join(workspacePath, ManifestFile))
		if err != nil {
			return nil, err
		}

		workspaces = append(workspaces, Summary{Manifest: manifest, Path: workspacePath})
	}

	sort.Slice(workspaces, func(i, j int) bool {
		return strings.ToLower(workspaces[i].Manifest.Name) < strings.ToLower(workspaces[j].Manifest.Name)
	})

	slog.Debug("listed workspaces", "count", len(workspaces))
	return workspaces, nil
}

func (r Registry) NewManifest(name string) (Manifest, error) {
	now := time.Now().UTC()
	safeName := SafeName(name)
	if safeName == "" {
		return Manifest{}, fmt.Errorf("workspace name %q does not contain any valid ASCII letters or digits", name)
	}
	if _, err := os.Stat(r.WorkspaceDir(safeName)); err == nil {
		return Manifest{}, fmt.Errorf("workspace name %q conflicts with existing workspace slug %q", name, safeName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, fmt.Errorf("check workspace path %q: %w", r.WorkspaceDir(safeName), err)
	}

	return Manifest{
		Name:          name,
		Runtime:       r.cfg.Runtime,
		ImageName:     "opencode-manager/" + safeName + ":latest",
		Image:         imageConfigFromConfig(r.cfg),
		ContainerName: "opencode-manager-" + safeName,
		HomeDir:       filepath.Join(r.WorkspaceDir(safeName), "home"),
		Env:           map[string]string{},
		Modules:       nil,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

func (r Registry) Create(name string) (CreateResult, error) {
	slog.Info("creating workspace", "name", name, "slug", SafeName(name))

	manifest, err := r.NewManifest(name)
	if err != nil {
		return CreateResult{}, err
	}

	safeName := SafeName(name)
	workspacePath := r.WorkspaceDir(safeName)
	if err := r.createLayout(workspacePath); err != nil {
		return CreateResult{}, err
	}

	if err := SaveManifest(filepath.Join(workspacePath, ManifestFile), manifest); err != nil {
		return CreateResult{}, err
	}

	slog.Info("workspace created", "name", name, "container", manifest.ContainerName, "path", workspacePath)
	return CreateResult{Manifest: manifest, Path: workspacePath}, nil
}

func (r Registry) Delete(summary Summary) error {
	if summary.Path == "" {
		return fmt.Errorf("workspace path is required")
	}

	workspacesDir, err := filepath.Abs(r.WorkspacesDir())
	if err != nil {
		return fmt.Errorf("resolve workspaces directory: %w", err)
	}
	workspacePath, err := filepath.Abs(summary.Path)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}

	rel, err := filepath.Rel(workspacesDir, workspacePath)
	if err != nil {
		return fmt.Errorf("check workspace path: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("refuse to delete path outside workspace root: %s", summary.Path)
	}

	expectedSlug := SafeName(summary.Manifest.Name)
	if filepath.Base(workspacePath) != expectedSlug {
		return fmt.Errorf("refuse to delete workspace path %q because it does not match slug %q", summary.Path, expectedSlug)
	}

	if err := os.RemoveAll(workspacePath); err != nil {
		return fmt.Errorf("delete workspace directory %q: %w", workspacePath, err)
	}

	slog.Debug("removed workspace directory", "workspace", summary.Manifest.Name, "path", workspacePath)
	return nil
}

func (r Registry) createLayout(workspacePath string) error {
	// opencode.json and AGENTS.md are mounted read-only from
	// ~/.config/opencode-manager at container creation. The OpenCode asset
	// directories (agents/, commands/, plugins/, skills/) are seeded into the
	// home by ensureWorkspaceOpenCodeAssets during provisioning so they are
	// writable by module install scripts; they are not materialized here. Only
	// the writable home layout is created.
	dirs := []string{
		"home",
		filepath.Join("home", "workspace"),
		filepath.Join("home", ".config", "opencode"),
	}

	for _, dir := range dirs {
		path := filepath.Join(workspacePath, dir)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create workspace directory %q: %w", path, err)
		}
	}

	return nil
}

func SafeName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder

	lastDash := false
	for _, r := range lower {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}

		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}

	return strings.Trim(b.String(), "-")
}
