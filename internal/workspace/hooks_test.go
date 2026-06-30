package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

// execRecordingDriver captures the scripts passed to Exec so hook behavior can be
// asserted without a real container.
type execRecordingDriver struct {
	*fakeDriver
	scripts []string
	status  string
	started int
}

func (d *execRecordingDriver) ContainerStatus(context.Context, string) (string, error) {
	if d.status == "" {
		return runtime.StatusRunning, nil
	}
	return d.status, nil
}

func (d *execRecordingDriver) StartContainer(context.Context, string) error {
	d.started++
	return nil
}

func (d *execRecordingDriver) Exec(_ context.Context, spec runtime.ExecSpec) ([]byte, error) {
	// The command is the last arg of `/bin/sh -c <script>`.
	if len(spec.Args) > 0 {
		d.scripts = append(d.scripts, spec.Args[len(spec.Args)-1])
	}
	return nil, nil
}

func newHookLifecycle(t *testing.T, cfg config.Config, driver runtime.Driver) (Lifecycle, Summary) {
	t.Helper()
	cfg.Runtime = config.RuntimeDocker
	cfg.WorkspaceRoot = t.TempDir()
	l := Lifecycle{cfg: cfg, registry: NewRegistry(cfg), driver: driver}
	created, err := l.registry.Create("demo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return l, Summary{Manifest: created.Manifest, Path: created.Path}
}

func TestPostCreateHookRunsOnceAndMarks(t *testing.T) {
	rec := &execRecordingDriver{fakeDriver: &fakeDriver{}}
	cfg := config.Config{WorkspacePostCreateCommands: []string{"echo one", "echo two"}}
	l, summary := newHookLifecycle(t, cfg, rec)

	l.runPostCreateHook(context.Background(), summary)
	if len(rec.scripts) != 2 {
		t.Fatalf("expected 2 commands run, got %d: %v", len(rec.scripts), rec.scripts)
	}
	// Commands run in the project dir with ~/.env sourced.
	if !strings.Contains(rec.scripts[0], "echo one") || !strings.Contains(rec.scripts[0], runtime.ContainerWorkspaceDir) || !strings.Contains(rec.scripts[0], ".env") {
		t.Fatalf("first script missing env/cd/command: %q", rec.scripts[0])
	}
	if _, err := os.Stat(filepath.Join(summary.Path, postCreateMarker)); err != nil {
		t.Fatalf("expected marker written: %v", err)
	}

	// A second start must not re-run the commands.
	rec.scripts = nil
	l.runPostCreateHook(context.Background(), summary)
	if len(rec.scripts) != 0 {
		t.Fatalf("post-create re-ran after marker: %v", rec.scripts)
	}
}

func TestPostCreateHookNoCommandsNoMarker(t *testing.T) {
	rec := &execRecordingDriver{fakeDriver: &fakeDriver{}}
	l, summary := newHookLifecycle(t, config.Config{}, rec)

	l.runPostCreateHook(context.Background(), summary)
	if len(rec.scripts) != 0 {
		t.Fatalf("expected no commands, got %v", rec.scripts)
	}
	if _, err := os.Stat(filepath.Join(summary.Path, postCreateMarker)); !os.IsNotExist(err) {
		t.Fatalf("expected no marker when no commands configured, stat err = %v", err)
	}
}

func TestPreDeleteHookRunsWhenContainerPresent(t *testing.T) {
	rec := &execRecordingDriver{fakeDriver: &fakeDriver{}}
	cfg := config.Config{WorkspacePreDeleteCommands: []string{"git push"}}
	l, summary := newHookLifecycle(t, cfg, rec)

	l.runPreDeleteHook(context.Background(), summary)
	if len(rec.scripts) != 1 || !strings.Contains(rec.scripts[0], "git push") {
		t.Fatalf("expected pre-delete command run, got %v", rec.scripts)
	}
	if rec.started != 0 {
		t.Fatalf("running container should not be started, started=%d", rec.started)
	}
}

func TestPreDeleteHookStartsStoppedContainer(t *testing.T) {
	rec := &execRecordingDriver{fakeDriver: &fakeDriver{}, status: runtime.StatusExited}
	cfg := config.Config{WorkspacePreDeleteCommands: []string{"backup"}}
	l, summary := newHookLifecycle(t, cfg, rec)

	l.runPreDeleteHook(context.Background(), summary)
	if rec.started != 1 {
		t.Fatalf("stopped container should be started once, started=%d", rec.started)
	}
	if len(rec.scripts) != 1 {
		t.Fatalf("expected command to run after start, got %v", rec.scripts)
	}
}

func TestPreDeleteHookSkipsWhenContainerMissing(t *testing.T) {
	rec := &execRecordingDriver{fakeDriver: &fakeDriver{}, status: runtime.StatusMissing}
	cfg := config.Config{WorkspacePreDeleteCommands: []string{"backup"}}
	l, summary := newHookLifecycle(t, cfg, rec)

	l.runPreDeleteHook(context.Background(), summary)
	if len(rec.scripts) != 0 || rec.started != 0 {
		t.Fatalf("missing container: expected no run/start, scripts=%v started=%d", rec.scripts, rec.started)
	}
}
