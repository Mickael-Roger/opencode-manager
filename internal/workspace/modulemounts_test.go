package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

// writeMountingModule creates a module in <root>/tools/<name> declaring mounts.
func writeMountingModule(t *testing.T, root, name, mounts string) {
	t.Helper()
	dir := filepath.Join(root, "tools", name)
	writeTestFile(t, filepath.Join(dir, "module.yml"), []byte("name: "+name+"\nversion: 1\nmounts:\n"+mounts))
	for _, script := range []string{"install", "uninstall"} {
		if err := os.WriteFile(filepath.Join(dir, script), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstalledModuleMountsOnlyForInstalledModules(t *testing.T) {
	modules := t.TempDir()
	hostDir := t.TempDir()
	creds := filepath.Join(hostDir, "credentials.json")
	writeTestFile(t, creds, []byte("{}"))

	writeMountingModule(t, modules, "claude-auth", "  - { source: "+creds+", target: /home/debian/.claude/.credentials.json }\n")
	writeMountingModule(t, modules, "other", "  - { source: "+hostDir+", target: /opt/other }\n")
	writeMountingModule(t, modules, "maybe", "  - { source: "+filepath.Join(hostDir, "missing")+", target: /opt/maybe, optional: true }\n")

	l := Lifecycle{cfg: config.Config{ModuleDirs: []string{modules}}}
	home := t.TempDir()
	manifest := Manifest{HomeDir: home}

	mounts, fingerprint, err := l.installedModuleMounts(manifest)
	if err != nil || mounts != nil || fingerprint != "" {
		t.Fatalf("no modules: got %v %q %v", mounts, fingerprint, err)
	}

	manifest.Modules = []ModuleInstance{
		{Name: "claude-auth", Category: "tools", Version: 1},
		{Name: "maybe", Category: "tools", Version: 1},
	}
	mounts, fingerprint, err = l.installedModuleMounts(manifest)
	if err != nil {
		t.Fatalf("installedModuleMounts: %v", err)
	}
	if len(mounts) != 1 || mounts[0].Source != creds || mounts[0].Target != "/home/debian/.claude/.credentials.json" || mounts[0].ReadOnly {
		t.Fatalf("unexpected mounts: %+v", mounts)
	}
	if fingerprint == "" {
		t.Fatal("expected a fingerprint")
	}
	info, err := os.Stat(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil || info.IsDir() {
		t.Fatalf("mount point not created as a file in the workspace home: %v", err)
	}

	// The optional source appearing changes the fingerprint, so the container is
	// recreated with the new mount.
	if err := os.Mkdir(filepath.Join(hostDir, "missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	mounts, withOptional, err := l.installedModuleMounts(manifest)
	if err != nil || len(mounts) != 2 || withOptional == fingerprint {
		t.Fatalf("optional mount: got %+v %q (was %q) %v", mounts, withOptional, fingerprint, err)
	}
}

func TestInstalledModuleMountsRequiredSourceMissing(t *testing.T) {
	modules := t.TempDir()
	writeMountingModule(t, modules, "need", "  - { source: /nonexistent/ocm-test, target: /opt/need }\n")
	l := Lifecycle{cfg: config.Config{ModuleDirs: []string{modules}}}
	manifest := Manifest{HomeDir: t.TempDir(), Modules: []ModuleInstance{{Name: "need", Category: "tools", Version: 1}}}
	if _, _, err := l.installedModuleMounts(manifest); err == nil {
		t.Fatal("expected an error for a missing required mount source")
	}
}

func TestAddMountingModuleRecordsBeforeInstall(t *testing.T) {
	modules := t.TempDir()
	creds := filepath.Join(t.TempDir(), "credentials.json")
	writeTestFile(t, creds, []byte("{}"))
	writeMountingModule(t, modules, "claude-auth", "  - { source: "+creds+", target: /home/debian/.claude/.credentials.json }\n")

	workspacePath := t.TempDir()
	home := filepath.Join(workspacePath, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Name: "demo", Runtime: "docker", ImageName: "img", ContainerName: "c", HomeDir: home}
	if err := SaveManifest(filepath.Join(workspacePath, ManifestFile), manifest); err != nil {
		t.Fatal(err)
	}

	fake := &fakeDriver{output: func(args []string) []byte { return nil }}
	l := Lifecycle{cfg: config.Config{ModuleDirs: []string{modules}}, driver: fake}
	catalog, err := l.Catalog()
	if err != nil || len(catalog) != 1 {
		t.Fatalf("Catalog: %v %v", catalog, err)
	}

	if err := l.AddModule(context.Background(), Summary{Manifest: manifest, Path: workspacePath}, catalog[0], nil); err != nil {
		t.Fatalf("AddModule: %v", err)
	}

	saved, err := LoadManifest(filepath.Join(workspacePath, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Modules) != 1 || saved.Modules[0].Name != "claude-auth" {
		t.Fatalf("manifest modules: %+v", saved.Modules)
	}
	installs := 0
	for _, args := range fake.gotArgs {
		if contains(args, "/opt/opencode-manager/modules/tools/claude-auth/install") {
			installs++
		}
	}
	// The fake marker never records it, so reconcile and the explicit run both
	// install; what matters is that it ran after the manifest listed it.
	if installs == 0 {
		t.Fatal("install script never ran")
	}
}

// mountDriver keeps the spec of the container it last created, reports it back
// as the running container's config, and fails the scripts matching failExec.
type mountDriver struct {
	*fakeDriver
	current  *runtime.ContainerSpec
	created  int
	failExec string
}

func (d *mountDriver) ContainerStatus(context.Context, string) (string, error) {
	if d.current == nil {
		return runtime.StatusMissing, nil
	}
	return runtime.StatusRunning, nil
}
func (d *mountDriver) ContainerRuntimeConfig(context.Context, string) (runtime.ContainerRuntimeConfig, error) {
	if d.current == nil {
		return runtime.ContainerRuntimeConfig{}, errors.New("no container")
	}
	return runtime.ContainerRuntimeConfig{Env: d.current.Env}, nil
}
func (d *mountDriver) CreateContainer(_ context.Context, spec runtime.ContainerSpec) error {
	d.current = &spec
	d.created++
	return nil
}
func (d *mountDriver) RemoveContainer(context.Context, string) error { d.current = nil; return nil }
func (d *mountDriver) Exec(ctx context.Context, spec runtime.ExecSpec) ([]byte, error) {
	if d.failExec != "" && contains(spec.Args, d.failExec) {
		return nil, errors.New("install failed")
	}
	return d.fakeDriver.Exec(ctx, spec)
}

func TestAddMountingModuleFailedInstallRemovesMount(t *testing.T) {
	modules := t.TempDir()
	creds := filepath.Join(t.TempDir(), "credentials.json")
	writeTestFile(t, creds, []byte("{}"))
	writeMountingModule(t, modules, "claude-auth", "  - { source: "+creds+", target: /home/debian/.claude/.credentials.json }\n")

	workspacePath := t.TempDir()
	home := filepath.Join(workspacePath, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Name: "demo", Runtime: "docker", ImageName: "img", ContainerName: "c", HomeDir: home, OpenCodePort: 4100}
	manifestPath := filepath.Join(workspacePath, ManifestFile)
	if err := SaveManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}

	d := &mountDriver{
		fakeDriver: &fakeDriver{output: func([]string) []byte { return nil }},
		failExec:   "/opt/opencode-manager/modules/tools/claude-auth/install",
	}
	l := Lifecycle{cfg: config.Config{ModuleDirs: []string{modules}}, driver: d}
	summary := Summary{Manifest: manifest, Path: workspacePath}
	if err := l.ensureStarted(context.Background(), summary, false); err != nil {
		t.Fatalf("ensureStarted: %v", err)
	}
	catalog, err := l.Catalog()
	if err != nil || len(catalog) != 1 {
		t.Fatalf("Catalog: %v %v", catalog, err)
	}

	err = l.AddModule(context.Background(), summary, catalog[0], nil)
	if err == nil || !strings.Contains(err.Error(), "install failed") {
		t.Fatalf("AddModule error = %v, want the install failure", err)
	}
	// The container was recreated with the mount for the install, then again
	// without it after the rollback.
	if d.created != 3 {
		t.Fatalf("containers created = %d, want 3", d.created)
	}
	for _, m := range d.current.Mounts {
		if m.Source == creds {
			t.Fatalf("failed module mount %+v still in the container", m)
		}
	}
	if _, ok := d.current.Env[moduleMountsFingerprintEnv]; ok {
		t.Fatal("container still carries the module mounts fingerprint")
	}
	saved, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Modules) != 0 {
		t.Fatalf("manifest modules after rollback: %+v", saved.Modules)
	}
}
