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
// status file. It is seeded into the shared plugins directory and copied into
// every workspace container.
//
//go:embed assets/opencode-manager-status.js
var statusPluginJS string

const statusPluginName = "opencode-manager-status.js"

// statusFileRelPath is where the plugin writes (inside the container home) and
// the manager reads (under the host-side workspace home directory).
var statusFileRelPath = filepath.Join(".local", "state", "opencode-manager", "status.json")

// deepSeekStatusFileRelPath is written by dsh-tui while it is attached to a
// DeepSeek Harness session. It stays separate from OpenCode's plugin-owned
// report so either runtime can be observed independently.
var deepSeekStatusFileRelPath = filepath.Join(".local", "state", "opencode-manager", "deepseek-status.json")

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
	ActivityWaiting  Activity = "waiting"  // blocked needing a human (a permission prompt)
	ActivitySleeping Activity = "sleeping" // finished its turn, opencode idle but still running
	ActivityError    Activity = "error"    // a session errored
	ActivityOff      Activity = "off"      // opencode not active in a running container
)

// statusReport mirrors the JSON written by the status-reporter plugin.
type statusReport struct {
	Activity        string    `json:"activity"`
	PendingApproval int       `json:"pendingApproval"`
	Sessions        int       `json:"sessions"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// SeedStatusPlugin writes the manager-owned status-reporter plugin into the
// shared plugins source directory, overwriting any previous copy so the
// shipped version is always current. Unlike user templates it is intentionally
// overwritten on every startup because the manager owns and versions it.
func SeedStatusPlugin() error {
	dir, err := config.GlobalDir()
	if err != nil {
		return err
	}

	pluginsDir := filepath.Join(dir, "opencode", "plugins")
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
	return readStatusActivity(homeDir, statusFileRelPath, running, true)
}

func readStatusActivity(homeDir, relativePath string, running, newWhenMissing bool) (Activity, int) {
	if homeDir == "" {
		return ActivityUnknown, 0
	}

	data, err := os.ReadFile(filepath.Join(homeDir, relativePath))
	if err != nil {
		if running {
			return ActivityUnknown, 0
		}
		if newWhenMissing {
			return ActivityNew, 0
		}
		return ActivityUnknown, 0
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

// readWorkspaceActivity combines OpenCode's status plugin with dsh-tui's
// optional heartbeat. A fresh DSH "starting" report takes precedence so the
// dashboard reflects connection/bootstrap before a turn begins; otherwise the
// most urgent live runtime state wins.
func readWorkspaceActivity(homeDir string, running, deepSeekEnabled bool) (Activity, int) {
	openCodeActivity, openCodePending := readActivity(homeDir, running)
	if !deepSeekEnabled {
		return openCodeActivity, openCodePending
	}

	path := filepath.Join(homeDir, deepSeekStatusFileRelPath)
	if _, err := os.Stat(path); err != nil {
		return openCodeActivity, openCodePending
	}
	deepSeekActivity, deepSeekPending := readStatusActivity(homeDir, deepSeekStatusFileRelPath, running, false)
	if deepSeekActivity == ActivityUnknown && running {
		return ActivityUnknown, deepSeekPending
	}
	if activityPriority(deepSeekActivity) > activityPriority(openCodeActivity) {
		return deepSeekActivity, deepSeekPending
	}
	return openCodeActivity, openCodePending
}

func activityPriority(activity Activity) int {
	switch activity {
	case ActivityWaiting:
		return 5
	case ActivityError:
		return 4
	case ActivityWorking:
		return 3
	case ActivitySleeping:
		return 2
	case ActivityNew:
		return 1
	default:
		return 0
	}
}

// activityFromReport maps a raw plugin report to a manager-side Activity,
// accounting for heartbeat staleness. Split out for testing.
func activityFromReport(report statusReport, now time.Time) (Activity, int) {
	if !report.UpdatedAt.IsZero() && now.Sub(report.UpdatedAt) > activityStaleAfter {
		return ActivityOff, 0
	}

	switch report.Activity {
	case "starting":
		return ActivityUnknown, 0
	case "working":
		return ActivityWorking, report.PendingApproval
	case "needs-approval":
		return ActivityWaiting, report.PendingApproval
	case "error":
		return ActivityError, 0
	case "idle":
		return ActivitySleeping, 0
	default:
		return ActivityOff, 0
	}
}
