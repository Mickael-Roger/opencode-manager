package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/module"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

// modulesContainerRoot is where the host module directory is bind-mounted
// (read-only) inside every workspace container. A module named "aws" runs from
// modulesContainerRoot/aws/install.
const modulesContainerRoot = "/opt/opencode-manager/modules"

// markerPath records which modules (and versions) are installed in a container.
// It lives in the writable layer, so it is wiped when the container is recreated
// (e.g. after an OpenCode update) — that absence is what makes reconcile re-run
// the install scripts to converge a fresh container to the manifest.
const markerPath = "/var/lib/opencode-manager/installed"

func moduleContainerDir(name string) string {
	return modulesContainerRoot + "/" + name
}

// primaryModuleDir is the single host module directory used by the module
// system: the first configured moduleDir (default ~/.config/opencode-manager/
// modules), into which the built-in modules are also seeded.
func primaryModuleDir(cfg config.Config) string {
	if len(cfg.ModuleDirs) == 0 {
		return ""
	}
	return cfg.ModuleDirs[0]
}

// moduleMounts returns the read-only bind mount exposing the module directory
// inside the workspace container. It is omitted when the directory does not
// exist so the runtime does not auto-create a root-owned mount point.
func moduleMounts(cfg config.Config) []runtime.Mount {
	dir := primaryModuleDir(cfg)
	if dir == "" {
		return nil
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil
	}
	return []runtime.Mount{{Source: dir, Target: modulesContainerRoot, ReadOnly: true}}
}

// Catalog lists the modules available to install, from the primary module
// directory (built-ins plus any the user added there).
func (l Lifecycle) Catalog() ([]module.Module, error) {
	dir := primaryModuleDir(l.cfg)
	if dir == "" {
		return nil, nil
	}
	return module.Catalog([]string{dir})
}

// AddModule installs a module into the workspace and records it in the manifest.
// The install script runs inside the container as the workspace user (which has
// passwordless sudo) with the prompt values passed as OCM_* environment
// variables. If the script changes ~/.env, the OpenCode server is bounced in
// place so the new variables take effect.
func (l Lifecycle) AddModule(ctx context.Context, summary Summary, mod module.Module, values map[string]string) error {
	slog.Info("adding module to workspace", "workspace", summary.Manifest.Name, "module", mod.Name)
	if err := l.EnsureStarted(ctx, summary); err != nil {
		return err
	}

	homeDir := summary.Manifest.HomeDir
	before := envHash(homeDir)

	if err := l.runModuleScript(ctx, summary, mod.Name, module.InstallScript, "install", values); err != nil {
		return err
	}

	manifest := summary.Manifest
	manifest.Modules = upsertModule(manifest.Modules, mod.Name, mod.Version, values)
	manifest.UpdatedAt = time.Now().UTC()
	if err := SaveManifest(filepath.Join(summary.Path, ManifestFile), manifest); err != nil {
		return err
	}
	if err := l.writeMarker(ctx, manifest.ContainerName, manifest.Modules); err != nil {
		return err
	}

	if envHash(homeDir) != before {
		l.bounceServer(ctx, manifest.ContainerName)
	}

	slog.Info("module added", "workspace", manifest.Name, "module", mod.Name)
	return nil
}

// RemoveModule runs a module's uninstall script and drops it from the manifest.
func (l Lifecycle) RemoveModule(ctx context.Context, summary Summary, name string) error {
	slog.Info("removing module from workspace", "workspace", summary.Manifest.Name, "module", name)
	if err := l.EnsureStarted(ctx, summary); err != nil {
		return err
	}

	values := map[string]string{}
	for _, m := range summary.Manifest.Modules {
		if m.Name == name {
			values = valuesFromInstance(m)
		}
	}

	homeDir := summary.Manifest.HomeDir
	before := envHash(homeDir)

	if err := l.runModuleScript(ctx, summary, name, module.UninstallScript, "uninstall", values); err != nil {
		return err
	}

	manifest := summary.Manifest
	manifest.Modules = removeModule(manifest.Modules, name)
	manifest.UpdatedAt = time.Now().UTC()
	if err := SaveManifest(filepath.Join(summary.Path, ManifestFile), manifest); err != nil {
		return err
	}
	if err := l.writeMarker(ctx, manifest.ContainerName, manifest.Modules); err != nil {
		return err
	}

	if envHash(homeDir) != before {
		l.bounceServer(ctx, manifest.ContainerName)
	}

	slog.Info("module removed", "workspace", manifest.Name, "module", name)
	return nil
}

// reconcile re-runs the install scripts for any manifest modules not recorded in
// the container's installed marker. A freshly (re)created container has no
// marker, so all selected modules are reinstalled, converging the container to
// the manifest. Module install scripts must be idempotent because of this.
func (l Lifecycle) reconcile(ctx context.Context, summary Summary) error {
	manifest := summary.Manifest
	if len(manifest.Modules) == 0 {
		return nil
	}

	installed := l.readMarker(ctx, manifest.ContainerName)
	homeDir := manifest.HomeDir
	before := envHash(homeDir)

	changed := false
	for _, m := range manifest.Modules {
		if v, ok := installed[m.Name]; ok && v == m.Version {
			continue
		}
		slog.Info("reconciling module", "workspace", manifest.Name, "module", m.Name)
		if err := l.runModuleScript(ctx, summary, m.Name, module.InstallScript, "install", valuesFromInstance(m)); err != nil {
			return err
		}
		changed = true
	}

	if !changed {
		return nil
	}

	if err := l.writeMarker(ctx, manifest.ContainerName, manifest.Modules); err != nil {
		return err
	}
	if envHash(homeDir) != before {
		l.bounceServer(ctx, manifest.ContainerName)
	}
	return nil
}

// runModuleScript runs a module's install/uninstall script inside the container
// as the workspace user, passing prompt values as OCM_* environment variables.
func (l Lifecycle) runModuleScript(ctx context.Context, summary Summary, modName, script, phase string, values map[string]string) error {
	env := map[string]string{
		"HOME":       openCodeHomeDir,
		"OCM_HOME":   openCodeHomeDir,
		"OCM_MODULE": modName,
		"OCM_PHASE":  phase,
	}
	for key, value := range values {
		env[module.EnvVarName(key)] = value
	}

	scriptPath := moduleContainerDir(modName) + "/" + script
	out, err := l.driver.Exec(ctx, runtime.ExecSpec{
		Container: summary.Manifest.ContainerName,
		Env:       env,
		Args:      []string{scriptPath},
	})
	if err != nil {
		return fmt.Errorf("module %s %s failed: %w", modName, phase, err)
	}
	slog.Debug("module script ran", "module", modName, "phase", phase, "output", strings.TrimSpace(string(out)))
	return nil
}

// bounceServer kills the running OpenCode server so the supervisor entrypoint
// relaunches it with a freshly sourced ~/.env. It is best-effort: pkill exits
// non-zero when nothing matched (server not yet started), which is fine.
func (l Lifecycle) bounceServer(ctx context.Context, containerName string) {
	if _, err := l.driver.Exec(ctx, runtime.ExecSpec{
		Container: containerName,
		User:      "0",
		Args:      []string{"pkill", "-f", "opencode serve"},
	}); err != nil {
		slog.Debug("bounce server: pkill returned non-zero (likely no match)", "container", containerName, "error", err)
		return
	}
	slog.Debug("bounced OpenCode server to reload environment", "container", containerName)
}

type markerEntry struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

func (l Lifecycle) readMarker(ctx context.Context, containerName string) map[string]int {
	out, err := l.driver.Exec(ctx, runtime.ExecSpec{
		Container: containerName,
		Args:      []string{"sh", "-c", "cat " + markerPath + " 2>/dev/null || true"},
	})
	result := map[string]int{}
	if err != nil {
		return result
	}
	var entries []markerEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &entries); err != nil {
		return result
	}
	for _, e := range entries {
		result[e.Name] = e.Version
	}
	return result
}

func (l Lifecycle) writeMarker(ctx context.Context, containerName string, mods []ModuleInstance) error {
	entries := make([]markerEntry, 0, len(mods))
	for _, m := range mods {
		entries = append(entries, markerEntry{Name: m.Name, Version: m.Version})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("encode module marker: %w", err)
	}
	// base64-encode so the payload is shell-safe in the sh -c command.
	encoded := base64.StdEncoding.EncodeToString(data)
	cmd := fmt.Sprintf("mkdir -p %s && echo '%s' | base64 -d > %s", filepath.Dir(markerPath), encoded, markerPath)
	if _, err := l.driver.Exec(ctx, runtime.ExecSpec{
		Container: containerName,
		User:      "0",
		Args:      []string{"sh", "-c", cmd},
	}); err != nil {
		return fmt.Errorf("write module marker: %w", err)
	}
	return nil
}

// envHash returns a hash of the workspace's ~/.env file (read from the host-side
// bind-mounted home), or an empty string if it does not exist. Comparing the
// hash before and after a module script tells us whether the OpenCode server
// must be bounced to pick up new environment variables.
func envHash(homeDir string) string {
	if homeDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(homeDir, ".env"))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func upsertModule(mods []ModuleInstance, name string, version int, values map[string]string) []ModuleInstance {
	vals := make(map[string]any, len(values))
	for key, value := range values {
		vals[key] = value
	}
	for i := range mods {
		if mods[i].Name == name {
			mods[i].Version = version
			mods[i].Values = vals
			return mods
		}
	}
	return append(mods, ModuleInstance{Name: name, Version: version, Values: vals})
}

func removeModule(mods []ModuleInstance, name string) []ModuleInstance {
	out := make([]ModuleInstance, 0, len(mods))
	for _, m := range mods {
		if m.Name != name {
			out = append(out, m)
		}
	}
	return out
}

func valuesFromInstance(m ModuleInstance) map[string]string {
	out := make(map[string]string, len(m.Values))
	for key, value := range m.Values {
		out[key] = valueToString(value)
	}
	return out
}

func valueToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return ""
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, valueToString(e))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(t)
	}
}
