package workspace

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

// postCreateMarker is the per-workspace sentinel recording that the post-create
// commands have already run. It is stored on the host (in the workspace
// directory) so it survives container recreation, and is removed with the
// workspace, making the post-create commands a true one-shot.
const postCreateMarker = ".ocm-postcreate-done"

// runPostCreateHook runs the configured workspacePostCreateCommands inside the
// workspace container exactly once: the first time the workspace is started,
// after modules are installed. Later starts (attach, shell, module edits, …) are
// no-ops thanks to the host-side marker, so the commands behave like an
// "after first creation" step. Failures are logged and never block the start.
func (l Lifecycle) runPostCreateHook(ctx context.Context, summary Summary) {
	commands := l.cfg.WorkspacePostCreateCommands
	if len(commands) == 0 || summary.Path == "" {
		return
	}
	marker := filepath.Join(summary.Path, postCreateMarker)
	if _, err := os.Stat(marker); err == nil {
		return // already ran for this workspace
	}

	slog.Info("running workspace post-create commands", "workspace", summary.Manifest.Name, "count", len(commands))
	l.runWorkspaceCommands(ctx, summary, commands, "post-create")

	// Record completion even if some commands failed: these are a one-shot step,
	// so they must not re-run on subsequent starts.
	if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		slog.Warn("failed to record post-create completion; commands may re-run next start", "workspace", summary.Manifest.Name, "error", err)
	}
}

// runPreDeleteHook runs the configured workspacePreDeleteCommands inside the
// workspace container just before it is destroyed, so they can back up work,
// push commits, or otherwise clean up while the workspace still exists. It
// starts the container when it exists but is stopped, and skips silently when no
// container is present. Failures are logged and never block deletion.
func (l Lifecycle) runPreDeleteHook(ctx context.Context, summary Summary) {
	commands := l.cfg.WorkspacePreDeleteCommands
	if len(commands) == 0 {
		return
	}
	name := summary.Manifest.ContainerName
	status, err := l.driver.ContainerStatus(ctx, name)
	if err != nil {
		slog.Warn("pre-delete hook: cannot determine container status; skipping", "workspace", summary.Manifest.Name, "error", err)
		return
	}
	if status == runtime.StatusMissing {
		slog.Debug("pre-delete hook: no container to run in; skipping", "workspace", summary.Manifest.Name)
		return
	}
	if status != runtime.StatusRunning {
		if serr := l.driver.StartContainer(ctx, name); serr != nil {
			slog.Warn("pre-delete hook: failed to start container; skipping", "workspace", summary.Manifest.Name, "error", serr)
			return
		}
	}

	slog.Info("running workspace pre-delete commands", "workspace", summary.Manifest.Name, "count", len(commands))
	l.runWorkspaceCommands(ctx, summary, commands, "pre-delete")
}

// runWorkspaceCommands executes each command inside the workspace container as
// the workspace user, in the project directory, with ~/.env sourced so
// module-provided environment variables are available — mirroring the runtime
// the OpenCode agent sees. Each command runs in its own shell; a non-zero exit
// is logged and the remaining commands still run.
func (l Lifecycle) runWorkspaceCommands(ctx context.Context, summary Summary, commands []string, phase string) {
	name := summary.Manifest.ContainerName
	for i, command := range commands {
		script := `set -a; [ -f "$HOME/.env" ] && . "$HOME/.env"; set +a; cd ` + runtime.ContainerWorkspaceDir + ` 2>/dev/null || true; ` + command
		out, err := l.driver.Exec(ctx, runtime.ExecSpec{
			Container: name,
			Env: map[string]string{
				"HOME":          openCodeHomeDir,
				"OCM_WORKSPACE": summary.Manifest.Name,
				"OCM_PHASE":     phase,
			},
			Args: []string{"/bin/sh", "-c", script},
		})
		if err != nil {
			slog.Warn("workspace command failed", "phase", phase, "workspace", summary.Manifest.Name, "index", i+1, "command", command, "error", err, "output", strings.TrimSpace(string(out)))
			continue
		}
		slog.Debug("workspace command ran", "phase", phase, "workspace", summary.Manifest.Name, "index", i+1, "command", command, "output", strings.TrimSpace(string(out)))
	}
}
