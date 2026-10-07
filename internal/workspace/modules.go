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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/module"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

// modulesContainerRoot is where the host module directory is bind-mounted
// (read-only) inside every workspace container. The host layout is mirrored, so a
// module "aws" in category "cloud" runs from modulesContainerRoot/cloud/aws/install.
const modulesContainerRoot = "/opt/opencode-manager/modules"

// markerPath records which modules (and versions) are installed in a container.
// It lives in the writable layer, so it is wiped when the container is recreated
// (e.g. after an OpenCode update) — that absence is what makes reconcile re-run
// the install scripts to converge a fresh container to the manifest.
const markerPath = "/var/lib/opencode-manager/installed"

func moduleContainerDir(category, name string) string {
	return modulesContainerRoot + "/" + category + "/" + name
}

// instanceCategory returns the category for an installed module instance. It uses
// the value recorded in the manifest, falling back to a catalog lookup by name
// for instances written before categories existed.
func (l Lifecycle) instanceCategory(inst ModuleInstance) string {
	if inst.Category != "" {
		return inst.Category
	}
	return l.lookupCategory(inst.Name)
}

// lookupCategory finds a module's category by scanning the catalog for its name.
// Returns "" when the module is not found on disk.
func (l Lifecycle) lookupCategory(name string) string {
	catalog, err := l.Catalog()
	if err != nil {
		slog.Debug("could not load catalog to resolve module category", "module", name, "error", err)
		return ""
	}
	for _, mod := range catalog {
		if mod.Name == name {
			return mod.Category
		}
	}
	return ""
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

// installedModuleMounts returns the bind mounts declared by the modules installed
// in the workspace, and a fingerprint of them that changes whenever the set does,
// so a workspace container is recreated exactly when one of its mounting modules
// is added or removed (or an optional source appears). Mount points under the
// workspace home are created on the host first, owned by the host user, so the
// runtime does not create them as root.
func (l Lifecycle) installedModuleMounts(manifest Manifest) ([]runtime.Mount, string, error) {
	if len(manifest.Modules) == 0 {
		return nil, "", nil
	}
	catalog, err := l.Catalog()
	if err != nil {
		return nil, "", err
	}
	byName := make(map[string]module.Module, len(catalog))
	for _, mod := range catalog {
		byName[mod.Name] = mod
	}

	var mounts []runtime.Mount
	targets := map[string]string{}
	for _, inst := range manifest.Modules {
		mod, ok := byName[inst.Name]
		if !ok {
			continue
		}
		for _, declared := range mod.Mounts {
			source, err := declared.HostSource()
			if err != nil {
				return nil, "", err
			}
			info, err := os.Stat(source)
			if err != nil {
				if os.IsNotExist(err) && declared.Optional {
					slog.Debug("skipping optional module mount with a missing source", "module", mod.Name, "source", source)
					continue
				}
				return nil, "", fmt.Errorf("module %s mount source %q: %w", mod.Name, source, err)
			}
			target := filepath.Clean(declared.Target)
			if previous, ok := targets[target]; ok {
				// Several instances of one module declare the same mount.
				if previous == source {
					continue
				}
				return nil, "", fmt.Errorf("module %s mounts %q onto %s, already used by %q", mod.Name, source, target, previous)
			}
			targets[target] = source
			if err := ensureHomeMountPoint(manifest.HomeDir, target, info.IsDir()); err != nil {
				return nil, "", err
			}
			mounts = append(mounts, runtime.Mount{Source: source, Target: target, ReadOnly: declared.ReadOnly})
		}
	}
	if len(mounts) == 0 {
		return nil, "", nil
	}
	data, err := json.Marshal(mounts)
	if err != nil {
		return nil, "", fmt.Errorf("encode module mounts: %w", err)
	}
	sum := sha256.Sum256(data)
	return mounts, hex.EncodeToString(sum[:]), nil
}

// ensureHomeMountPoint creates the host-side mount point of a target inside the
// workspace home (a directory or an empty file), with its parent directories.
// Targets outside the home are left to the runtime.
func ensureHomeMountPoint(homeDir, target string, dir bool) error {
	rel, err := filepath.Rel(openCodeHomeDir, target)
	if homeDir == "" || err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil
	}
	path := filepath.Join(homeDir, rel)
	if dir {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create module mount point %q: %w", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create module mount point %q: %w", filepath.Dir(path), err)
	}
	if _, err := os.Lstat(path); err == nil {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create module mount point %q: %w", path, err)
	}
	return file.Close()
}

// moduleHasMounts reports whether an installed module declares mounts.
func (l Lifecycle) moduleHasMounts(category, name string) bool {
	dir := primaryModuleDir(l.cfg)
	if dir == "" || category == "" {
		return false
	}
	mod, err := module.Load(filepath.Join(dir, category, name))
	return err == nil && len(mod.Mounts) > 0
}

// AddModule installs a module into the workspace and records it in the manifest.
// The install script runs inside the container as the workspace user (which has
// passwordless sudo) with the prompt values passed as OCM_* environment
// variables. If the script changes ~/.env, the OpenCode server is bounced in
// place so the new variables take effect.
func (l Lifecycle) AddModule(ctx context.Context, summary Summary, mod module.Module, values map[string]string) error {
	summary, unlock, err := l.beginWorkspaceOperation(ctx, summary)
	if err != nil {
		return err
	}
	defer unlock()
	slog.Info("adding module to workspace", "workspace", summary.Manifest.Name, "module", mod.Name)
	if len(mod.Mounts) > 0 {
		return l.addMountingModule(ctx, summary, mod, values)
	}
	if err := l.ensureStarted(ctx, summary, false); err != nil {
		return err
	}

	homeDir := summary.Manifest.HomeDir
	before := envHash(homeDir)

	installVals, err := l.resolveInstallValues(ctx, mod.Category, mod.Name, values)
	if err != nil {
		return err
	}
	if err := l.runModuleScript(ctx, summary, mod.Category, mod.Name, module.InstallScript, "install", installVals); err != nil {
		return err
	}

	// Read the manifest fresh from disk rather than trusting summary.Manifest: it
	// may be stale (EnsureStarted's port backfill wrote to disk, and a batch of
	// adds passes the same snapshot to every call). Loading here makes each add
	// accumulate instead of clobbering the previous one's manifest entry.
	manifestPath := filepath.Join(summary.Path, ManifestFile)
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	manifest.Modules = upsertModule(manifest.Modules, mod.InstanceID(values), mod.Name, mod.Category, mod.Version, values)
	manifest.UpdatedAt = time.Now().UTC()
	if err := SaveManifest(manifestPath, manifest); err != nil {
		return err
	}
	if err := l.writeMarker(ctx, manifest.ContainerName, manifest.Modules); err != nil {
		return err
	}

	l.maybeBounce(ctx, manifest.ContainerName, mod.Name, mod.RestartServer, before, envHash(homeDir))

	slog.Info("module added", "workspace", manifest.Name, "module", mod.Name)
	return nil
}

// addMountingModule adds a module that declares mounts. The mounts must exist
// before its install script runs, so the module is recorded in the manifest
// first and the container recreated with them; the start's reconcile then runs
// the install. When the install does not succeed the manifest entry is rolled
// back and the container reconciled with it, so the workspace does not keep a
// mount that no installed module grants.
func (l Lifecycle) addMountingModule(ctx context.Context, summary Summary, mod module.Module, values map[string]string) error {
	id := mod.InstanceID(values)
	manifestPath := filepath.Join(summary.Path, ManifestFile)
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	var previous *ModuleInstance
	for _, m := range manifest.Modules {
		if m.InstanceID() == id {
			m := m
			previous = &m
		}
	}
	manifest.Modules = upsertModule(manifest.Modules, id, mod.Name, mod.Category, mod.Version, values)
	manifest.UpdatedAt = time.Now().UTC()
	if err := SaveManifest(manifestPath, manifest); err != nil {
		return err
	}

	rollback := func(cause error) error {
		current, err := LoadManifest(manifestPath)
		if err != nil {
			return fmt.Errorf("%w (roll back manifest: %v)", cause, err)
		}
		current.Modules = removeModule(current.Modules, id)
		if previous != nil {
			current.Modules = append(current.Modules, *previous)
		}
		current.UpdatedAt = time.Now().UTC()
		if err := SaveManifest(manifestPath, current); err != nil {
			return fmt.Errorf("%w (roll back manifest: %v)", cause, err)
		}
		// The container may already run with the module's mounts; recreate it
		// with the restored mount set. Failing that, stop it: the next start
		// recreates it from the manifest.
		if err := l.ensureStarted(ctx, Summary{Manifest: current, Path: summary.Path}, false); err != nil {
			if stopErr := l.driver.StopContainer(ctx, current.ContainerName); stopErr != nil {
				return fmt.Errorf("%w (remove module mounts: %v; stop container: %v)", cause, err, stopErr)
			}
			return fmt.Errorf("%w (remove module mounts: %v; container stopped)", cause, err)
		}
		return cause
	}

	summary.Manifest = manifest
	if err := l.ensureStarted(ctx, summary, false); err != nil {
		return rollback(err)
	}

	// Reconcile already installed it unless the container was not recreated (the
	// mounts were unchanged) or its install failed; run it here then, so the
	// error reaches the user.
	if v, ok := l.readMarker(ctx, manifest.ContainerName)[id]; !ok || v != mod.Version {
		homeDir := manifest.HomeDir
		before := envHash(homeDir)
		installVals, err := l.resolveInstallValues(ctx, mod.Category, mod.Name, values)
		if err != nil {
			return rollback(err)
		}
		if err := l.runModuleScript(ctx, summary, mod.Category, mod.Name, module.InstallScript, "install", installVals); err != nil {
			return rollback(err)
		}
		if err := l.writeMarker(ctx, manifest.ContainerName, manifest.Modules); err != nil {
			return err
		}
		l.maybeBounce(ctx, manifest.ContainerName, mod.Name, mod.RestartServer, before, envHash(homeDir))
	}

	slog.Info("module added", "workspace", manifest.Name, "module", mod.Name)
	return nil
}

// RemoveModule runs a module's uninstall script and drops it from the manifest.
// id is the instance identity (ModuleInstance.InstanceID): the module name for a
// singleton, or "name:keyvalue" for one entry of a multi-instance module.
func (l Lifecycle) RemoveModule(ctx context.Context, summary Summary, id string) error {
	summary, unlock, err := l.beginWorkspaceOperation(ctx, summary)
	if err != nil {
		return err
	}
	defer unlock()
	slog.Info("removing module from workspace", "workspace", summary.Manifest.Name, "module", id)
	if err := l.ensureStarted(ctx, summary, false); err != nil {
		return err
	}

	modName := id
	category := ""
	values := map[string]string{}
	for _, m := range summary.Manifest.Modules {
		if m.InstanceID() == id {
			modName = m.Name
			category = l.instanceCategory(m)
			values = valuesFromInstance(m)
		}
	}
	restartServer := l.moduleRestartServer(category, modName)
	hasMounts := l.moduleHasMounts(category, modName)

	homeDir := summary.Manifest.HomeDir
	before := envHash(homeDir)

	if err := l.runModuleScript(ctx, summary, category, modName, module.UninstallScript, "uninstall", values); err != nil {
		return err
	}

	// Read the manifest fresh from disk so a batch of removes (and the adds that
	// ran before them) accumulate instead of each clobbering the snapshot.
	manifestPath := filepath.Join(summary.Path, ManifestFile)
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	manifest.Modules = removeModule(manifest.Modules, id)
	manifest.UpdatedAt = time.Now().UTC()
	if err := SaveManifest(manifestPath, manifest); err != nil {
		return err
	}
	if err := l.writeMarker(ctx, manifest.ContainerName, manifest.Modules); err != nil {
		return err
	}

	if hasMounts {
		// Recreate the container without the module's mounts, so the workspace
		// loses access to them right away. The fresh server reads ~/.env anyway.
		summary.Manifest = manifest
		if err := l.ensureStarted(ctx, summary, false); err != nil {
			return err
		}
	} else {
		l.maybeBounce(ctx, manifest.ContainerName, modName, restartServer, before, envHash(homeDir))
	}

	slog.Info("module removed", "workspace", manifest.Name, "module", id)
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
	restartServer := false
	for _, m := range manifest.Modules {
		if v, ok := installed[m.InstanceID()]; ok && v == m.Version {
			continue
		}
		slog.Info("reconciling module", "workspace", manifest.Name, "module", m.Name)
		category := l.instanceCategory(m)
		installVals, err := l.resolveInstallValues(ctx, category, m.Name, valuesFromInstance(m))
		if err != nil {
			return err
		}
		if err := l.runModuleScript(ctx, summary, category, m.Name, module.InstallScript, "install", installVals); err != nil {
			return err
		}
		changed = true
		restartServer = restartServer || l.moduleRestartServer(category, m.Name)
	}

	if !changed {
		return nil
	}

	if err := l.writeMarker(ctx, manifest.ContainerName, manifest.Modules); err != nil {
		return err
	}
	l.maybeBounce(ctx, manifest.ContainerName, "reconcile", restartServer, before, envHash(homeDir))
	return nil
}

// runModuleScript runs a module's install/uninstall script inside the container
// as the workspace user, passing prompt values as OCM_* environment variables.
func (l Lifecycle) runModuleScript(ctx context.Context, summary Summary, category, modName, script, phase string, values map[string]string) error {
	env := map[string]string{
		"HOME":       openCodeHomeDir,
		"OCM_HOME":   openCodeHomeDir,
		"OCM_MODULE": modName,
		"OCM_PHASE":  phase,
	}
	for key, value := range values {
		env[module.EnvVarName(key)] = value
	}

	scriptPath := moduleContainerDir(category, modName) + "/" + script
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

// resolveInstallValues runs a module's optional host-side resolve hook and
// merges its output into the values passed to the container install script. The
// hook runs ON THE HOST (with full host access and the collected prompt values
// as OCM_* environment variables) and prints "key=value" lines; each becomes an
// additional OCM_<KEY> for the install script. This lets a module derive
// container input from host-only state (e.g. extract the selected kube contexts
// from ~/.kube/config) without persisting that derived value to the manifest.
// Modules without a resolve hook get their values back unchanged.
func (l Lifecycle) resolveInstallValues(ctx context.Context, category, modName string, values map[string]string) (map[string]string, error) {
	dir := primaryModuleDir(l.cfg)
	if dir == "" {
		return values, nil
	}
	moduleDir := filepath.Join(dir, category, modName)
	script := filepath.Join(moduleDir, module.ResolveScript)
	info, err := os.Stat(script)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return values, nil
	}

	env := os.Environ()
	env = append(env, "OCM_MODULE="+modName)
	for key, value := range values {
		env = append(env, module.EnvVarName(key)+"="+value)
	}

	cmd := exec.CommandContext(ctx, script)
	cmd.Env = env
	cmd.Dir = moduleDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("module %s resolve hook failed: %w", modName, err)
	}

	merged := make(map[string]string, len(values))
	for key, value := range values {
		merged[key] = value
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.Index(line, "=")
		if i <= 0 {
			continue
		}
		merged[line[:i]] = line[i+1:]
	}
	slog.Debug("module resolve hook ran", "module", modName)
	return merged, nil
}

// maybeBounce restarts the OpenCode server after a module change, but only when
// the module declares it affects the environment (restartServer) AND ~/.env
// actually changed. Splitting the decision this way keeps a promise the TUI's
// idle guard relies on: a module declared restartServer:false is never bounced,
// so it can be installed or removed mid-task without interrupting it. A module
// that changes ~/.env despite declaring restartServer:false is a manifest bug —
// we log it so the stale environment is diagnosable rather than silent.
func (l Lifecycle) maybeBounce(ctx context.Context, containerName, modName string, restartServer bool, before, after string) {
	if before == after {
		return
	}
	if !restartServer {
		slog.Warn("module changed ~/.env but is declared restartServer:false; not bouncing server (env change will not take effect until the next restart)", "container", containerName, "module", modName)
		return
	}
	l.bounceServer(ctx, containerName)
}

// moduleRestartServer reports the RestartServer flag for a module by name,
// loaded from the primary module directory. Removal and reconcile only have the
// installed module name (not the loaded definition), so they look it up here.
// Unknown or unreadable modules default to true (restart required) to stay on
// the safe side.
func (l Lifecycle) moduleRestartServer(category, name string) bool {
	dir := primaryModuleDir(l.cfg)
	if dir == "" {
		return true
	}
	mod, err := module.Load(filepath.Join(dir, category, name))
	if err != nil {
		slog.Debug("could not load module to read restartServer flag; assuming restart required", "module", name, "error", err)
		return true
	}
	return mod.RestartServer
}

// bounceServer kills the running agent servers so the supervisor entrypoint
// relaunches them with a freshly sourced ~/.env. It is best-effort: pkill exits
// non-zero when nothing matched (a runtime is disabled or not yet started).
func (l Lifecycle) bounceServer(ctx context.Context, containerName string) {
	for _, pattern := range []string{"opencode serve", "dsh web"} {
		if _, err := l.driver.Exec(ctx, runtime.ExecSpec{
			Container: containerName,
			User:      "0",
			Args:      []string{"pkill", "-f", pattern},
		}); err != nil {
			slog.Debug("bounce server: pkill returned non-zero (likely no match)", "container", containerName, "pattern", pattern, "error", err)
		}
	}
	slog.Debug("bounced agent servers to reload environment", "container", containerName)
}

type markerEntry struct {
	ID      string `json:"id"`
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
		id := e.ID
		if id == "" {
			id = e.Name
		}
		result[id] = e.Version
	}
	return result
}

func (l Lifecycle) writeMarker(ctx context.Context, containerName string, mods []ModuleInstance) error {
	entries := make([]markerEntry, 0, len(mods))
	for _, m := range mods {
		entries = append(entries, markerEntry{ID: m.InstanceID(), Name: m.Name, Version: m.Version})
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

func upsertModule(mods []ModuleInstance, id, name, category string, version int, values map[string]string) []ModuleInstance {
	vals := make(map[string]any, len(values))
	for key, value := range values {
		vals[key] = value
	}
	for i := range mods {
		if mods[i].InstanceID() == id {
			mods[i].ID = id
			mods[i].Name = name
			mods[i].Category = category
			mods[i].Version = version
			mods[i].Values = vals
			return mods
		}
	}
	return append(mods, ModuleInstance{Name: name, ID: id, Category: category, Version: version, Values: vals})
}

func removeModule(mods []ModuleInstance, id string) []ModuleInstance {
	out := make([]ModuleInstance, 0, len(mods))
	for _, m := range mods {
		if m.InstanceID() != id {
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

// ValuesMap returns the instance's prompt values as strings, e.g. to repopulate
// the module editor when editing a template built from these instances.
func (mi ModuleInstance) ValuesMap() map[string]string {
	return valuesFromInstance(mi)
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
