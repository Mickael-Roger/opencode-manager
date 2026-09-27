package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/agent"
	"github.com/mickael-menu/opencode-manager/internal/module"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

func TestOperationLockAcrossProcesses(t *testing.T) {
	if path := os.Getenv("OCM_TEST_LOCK"); path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		unlock, err := acquireOperationLock(ctx, path)
		if !errors.Is(err, context.DeadlineExceeded) {
			if unlock != nil {
				unlock()
			}
			t.Fatalf("child acquired parent's lock: %v", err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "workspace.lock")
	unlock, err := acquireOperationLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestOperationLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "OCM_TEST_LOCK="+path)
	out, childErr := cmd.CombinedOutput()
	unlock()
	if childErr != nil {
		t.Fatalf("child: %v\n%s", childErr, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err = acquireOperationLock(ctx, path)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	unlock()
}

func TestWorkspaceOperationsWaitAndCancel(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	l := Lifecycle{agents: agent.NewRegistry()} // Any unguarded driver call panics.
	summary := Summary{Manifest: Manifest{Name: "demo", ContainerName: "demo"}}
	_, unlock, err := l.beginWorkspaceOperation(context.Background(), summary)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	for name, operation := range map[string]func(context.Context) error{
		"start":    func(ctx context.Context) error { return l.EnsureStarted(ctx, summary) },
		"update":   func(ctx context.Context) error { return l.UpdateWorkspaceImage(ctx, summary) },
		"recreate": func(ctx context.Context) error { return l.RecreateContainer(ctx, summary) },
		"stop":     func(ctx context.Context) error { return l.Stop(ctx, summary) },
		"delete":   func(ctx context.Context) error { return l.Delete(ctx, summary) },
		"attach": func(ctx context.Context) error {
			_, err := l.AttachCommand(ctx, summary)
			return err
		},
		"shell": func(ctx context.Context) error {
			_, err := l.ShellCommand(ctx, summary)
			return err
		},
		"add module": func(ctx context.Context) error {
			return l.AddModule(ctx, summary, module.Module{Name: "golang"}, nil)
		},
		"remove module": func(ctx context.Context) error { return l.RemoveModule(ctx, summary, "golang") },
		"profile sync":  func(ctx context.Context) error { return l.reconcileDeepSeekProfiles(ctx, summary) },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if err := operation(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("operation did not wait for workspace: %v", err)
			}
		})
	}
	// Another workspace remains usable while this one is busy.
	other := Summary{Manifest: Manifest{Name: "other", ContainerName: "other"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, releaseOther, err := l.beginWorkspaceOperation(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	releaseOther()
}

func TestWorkspaceOperationReloadsManifestAndRejectsDeletedWorkspace(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := t.TempDir()
	summary := Summary{Path: path, Manifest: Manifest{Name: "demo", ContainerName: "demo", HomeDir: filepath.Join(path, "home")}}
	updated := summary.Manifest
	updated.Modules = []ModuleInstance{{Name: "golang", Version: 2}}
	if err := SaveManifest(filepath.Join(path, ManifestFile), updated); err != nil {
		t.Fatal(err)
	}
	l := Lifecycle{}
	fresh, unlock, err := l.beginWorkspaceOperation(context.Background(), summary)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if len(fresh.Manifest.Modules) != 1 {
		t.Fatal("operation retained stale module snapshot")
	}
	if err := os.Remove(filepath.Join(path, ManifestFile)); err != nil {
		t.Fatal(err)
	}
	if _, unlock, err := l.beginWorkspaceOperation(context.Background(), summary); err == nil {
		unlock()
		t.Fatal("deleted workspace accepted")
	}
}

type blockingInstallDriver struct {
	*fakeDriver
	entered   chan struct{}
	resume    chan struct{}
	builds    atomic.Int32
	installs  atomic.Int32
	installed atomic.Bool
}

func (d *blockingInstallDriver) BuildImage(context.Context, runtime.BuildSpec) error {
	d.builds.Add(1)
	return nil
}

func (d *blockingInstallDriver) Exec(ctx context.Context, spec runtime.ExecSpec) ([]byte, error) {
	args := strings.Join(spec.Args, " ")
	switch {
	case strings.HasSuffix(args, "/golang/install"):
		d.installs.Add(1)
		d.entered <- struct{}{}
		select {
		case <-d.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case strings.Contains(args, "cat "+markerPath):
		if d.installed.Load() {
			return []byte(`[{"name":"golang","version":1}]`), nil
		}
	case strings.Contains(args, "base64 -d > "+markerPath):
		d.installed.Store(true)
	}
	return nil, nil
}

func TestStartHoldsLockThroughModuleInstallation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := testConfig(t)
	r := NewRegistry(cfg)
	created, err := r.Create("demo")
	if err != nil {
		t.Fatal(err)
	}
	created.Manifest.Modules = []ModuleInstance{{Name: "golang", Category: "language", Version: 1}}
	if err := SaveManifest(filepath.Join(created.Path, ManifestFile), created.Manifest); err != nil {
		t.Fatal(err)
	}
	summary := Summary{Path: created.Path, Manifest: created.Manifest}
	d := &blockingInstallDriver{fakeDriver: &fakeDriver{}, entered: make(chan struct{}, 2), resume: make(chan struct{})}
	l := Lifecycle{cfg: cfg, registry: r, driver: d}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- l.EnsureStarted(ctx, summary) }()
	select {
	case <-d.entered:
	case <-ctx.Done():
		t.Fatal("install never started")
	}
	// A distinct Lifecycle must wait for the entire install/marker transaction,
	// not merely for container creation. Attach must not trigger a second build.
	other := Lifecycle{cfg: cfg, registry: r, driver: d}
	waitCtx, waitCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer waitCancel()
	_, err = other.ShellCommand(waitCtx, summary)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent shell did not wait: %v", err)
	}
	if d.builds.Load() != 1 || d.installs.Load() != 1 {
		t.Fatal("concurrent provision/install entered the critical section")
	}
	close(d.resume)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := other.EnsureStarted(ctx, summary); err != nil {
		t.Fatal(err)
	}
	if d.installs.Load() != 1 {
		t.Fatal("completed module install was repeated")
	}
}
