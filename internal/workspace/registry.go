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

// workspaceHomeSubdir is the workspace subdirectory bind-mounted as the
// container home. It holds the workspace's data and is the directory preserved
// on delete when config.PreserveData is set.
const workspaceHomeSubdir = "home"

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
		manifestPath := filepath.Join(workspacePath, ManifestFile)
		if _, err := os.Stat(manifestPath); errors.Is(err, os.ErrNotExist) {
			// A directory without a manifest is not a live workspace — e.g. the
			// home directory left behind by a preserveData deletion. Skip it.
			slog.Debug("skipping directory without manifest", "path", workspacePath)
			continue
		} else if err != nil {
			return nil, fmt.Errorf("check workspace manifest %q: %w", manifestPath, err)
		}
		manifest, err := LoadManifest(manifestPath)
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

	port, err := r.AllocateOpenCodePort()
	if err != nil {
		return Manifest{}, err
	}

	return Manifest{
		Name:          name,
		Runtime:       r.cfg.Runtime,
		ImageName:     "opencode-manager/" + safeName + ":latest",
		Image:         imageConfigFromConfig(r.cfg),
		ContainerName: "opencode-manager-" + safeName,
		HomeDir:       filepath.Join(r.WorkspaceDir(safeName), workspaceHomeSubdir),
		OpenCodePort:  port,
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

	if r.cfg.PreserveData {
		return r.deletePreservingHome(workspacePath, summary.Manifest.Name)
	}

	if err := os.RemoveAll(workspacePath); err != nil {
		return fmt.Errorf("delete workspace directory %q: %w", workspacePath, err)
	}

	slog.Debug("removed workspace directory", "workspace", summary.Manifest.Name, "path", workspacePath)
	return nil
}

// deletePreservingHome removes a workspace's metadata (its manifest and any
// other files) while leaving the home subdirectory — the bind-mounted container
// home — intact, so its data survives the deletion. The leftover directory has
// no manifest, so Registry.List skips it. Its slug stays reserved until the
// directory is removed by hand.
func (r Registry) deletePreservingHome(workspacePath, name string) error {
	entries, err := os.ReadDir(workspacePath)
	if err != nil {
		return fmt.Errorf("read workspace directory %q: %w", workspacePath, err)
	}

	for _, entry := range entries {
		if entry.Name() == workspaceHomeSubdir {
			continue
		}
		path := filepath.Join(workspacePath, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("delete workspace file %q: %w", path, err)
		}
	}

	slog.Info("preserved workspace home directory", "workspace", name, "home", filepath.Join(workspacePath, workspaceHomeSubdir))
	return nil
}

func (r Registry) createLayout(workspacePath string) error {
	// Shared OpenCode configuration is copied into this writable directory during
	// provisioning. Only the base home layout is created here.
	dirs := []string{
		workspaceHomeSubdir,
		filepath.Join(workspaceHomeSubdir, "workspace"),
		filepath.Join(workspaceHomeSubdir, ".config", "opencode"),
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
