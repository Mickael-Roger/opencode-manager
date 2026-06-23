package workspace

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/config"
)

// statusPluginJS is the opencode plugin that reports per-session activity to a
// status file. It is seeded into the global plugins directory and mounted
// read-only into every workspace container.
//
//go:embed assets/opencode-manager-status.js
var statusPluginJS string

const statusPluginName = "opencode-manager-status.js"

// statusFileRelPath is where the plugin writes (inside the container home) and
// the manager reads (under the host-side workspace home directory).
var statusFileRelPath = filepath.Join(".local", "state", "opencode-manager", "status.json")

// activityStaleAfter is how long the status file may go without a heartbeat
// before the manager assumes opencode is no longer running in the container.
// The plugin heartbeats every 10s, so this leaves room for a couple of misses.
const activityStaleAfter = 30 * time.Second

// Activity describes what opencode is doing inside a workspace, independent of
// the container lifecycle status.
type Activity string

const (
	ActivityUnknown  Activity = ""         // no report yet (container just started)
	ActivityNew      Activity = "new"      // workspace has never been used (no status file ever)
	ActivityWorking  Activity = "working"  // agent is generating or running tools
	ActivityWaiting  Activity = "waiting"  // finished a turn, waiting for human input
	ActivityApproval Activity = "approval" // blocked on a permission prompt
	ActivityError    Activity = "error"    // a session errored
	ActivityAsleep   Activity = "asleep"   // opencode not active in a running container
)

// statusReport mirrors the JSON written by the status-reporter plugin.
type statusReport struct {
	Activity        string    `json:"activity"`
	PendingApproval int       `json:"pendingApproval"`
	Sessions        int       `json:"sessions"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// SeedStatusPlugin writes the manager-owned status-reporter plugin into the
// global plugins template directory, overwriting any previous copy so the
// shipped version is always current. Unlike user templates it is intentionally
// overwritten on every startup because the manager owns and versions it.
func SeedStatusPlugin() error {
	dir, err := config.GlobalDir()
	if err != nil {
		return err
	}

	pluginsDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o700); err != nil {
		return fmt.Errorf("create plugins directory %q: %w", pluginsDir, err)
	}

	path := filepath.Join(pluginsDir, statusPluginName)
	if err := os.WriteFile(path, []byte(statusPluginJS), 0o600); err != nil {
		return fmt.Errorf("write status plugin %q: %w", path, err)
	}

	slog.Debug("seeded status plugin", "path", path)
	return nil
}

// EnsureWorkspaceStatusPlugin writes the manager-owned status-reporter plugin
// into a workspace's OpenCode plugins directory, overwriting any previous copy
// so the shipped version is always current. It is used now that the asset
// directories are workspace-owned rather than mounted read-only from the global
// templates (the manager owns and versions this plugin).
func EnsureWorkspaceStatusPlugin(configDir string) error {
	pluginsDir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o700); err != nil {
		return fmt.Errorf("create workspace plugins directory %q: %w", pluginsDir, err)
	}

	path := filepath.Join(pluginsDir, statusPluginName)
	if err := os.WriteFile(path, []byte(statusPluginJS), 0o600); err != nil {
		return fmt.Errorf("write workspace status plugin %q: %w", path, err)
	}

	slog.Debug("ensured workspace status plugin", "path", path)
	return nil
}

// readActivity reads and interprets the status file written by the plugin under
// the host-side workspace home directory. The status file is created the first
// time opencode boots in a workspace and persists afterwards, so its absence is
// a reliable "never used" signal.
//
//   - file missing, container stopped -> ActivityNew (never used)
//   - file missing, container running -> ActivityUnknown (opencode still booting)
//   - file present, container stopped -> ActivityUnknown (used before, now off)
//   - file present, container running -> mapped from the report (with staleness)
func readActivity(homeDir string, running bool) (Activity, int) {
	if homeDir == "" {
		return ActivityUnknown, 0
	}

	data, err := os.ReadFile(filepath.Join(homeDir, statusFileRelPath))
	if err != nil {
		if running {
			return ActivityUnknown, 0
		}
		return ActivityNew, 0
	}

	if !running {
		return ActivityUnknown, 0
	}

	var report statusReport
	if err := json.Unmarshal(data, &report); err != nil {
		slog.Warn("malformed workspace status file", "homeDir", homeDir, "error", err)
		return ActivityUnknown, 0
	}

	return activityFromReport(report, time.Now())
}

// activityFromReport maps a raw plugin report to a manager-side Activity,
// accounting for heartbeat staleness. Split out for testing.
func activityFromReport(report statusReport, now time.Time) (Activity, int) {
	if !report.UpdatedAt.IsZero() && now.Sub(report.UpdatedAt) > activityStaleAfter {
		return ActivityAsleep, 0
	}

	switch report.Activity {
	case "working":
		return ActivityWorking, report.PendingApproval
	case "needs-approval":
		return ActivityApproval, report.PendingApproval
	case "error":
		return ActivityError, 0
	case "idle":
		return ActivityWaiting, 0
	default:
		return ActivityAsleep, 0
	}
}
