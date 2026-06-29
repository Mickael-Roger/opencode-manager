package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

func TestCompactCount(t *testing.T) {
	cases := map[int64]string{
		0:             "0",
		999:           "999",
		1000:          "1k",
		1234:          "1.2k",
		12345:         "12.3k",
		999999:        "1000k",
		1_000_000:     "1M",
		2_500_000:     "2.5M",
		1_000_000_000: "1B",
		3_400_000_000: "3.4B",
	}
	for in, want := range cases {
		if got := compactCount(in); got != want {
			t.Errorf("compactCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkspaceTokensDisplay(t *testing.T) {
	ws := workspace.Summary{Manifest: workspace.Manifest{Name: "app"}}
	m := model{tokens: map[string]tokenState{}}

	if got := m.workspaceTokens(ws); got != "—" {
		t.Errorf("never-measured = %q, want —", got)
	}

	m.tokens["app"] = tokenState{loading: true}
	if got := m.workspaceTokens(ws); got != "…" {
		t.Errorf("loading = %q, want …", got)
	}

	m.tokens["app"] = tokenState{loaded: true, usage: workspace.TokenUsage{TotalInput: 12345, TotalOutput: 4500, TotalCacheRead: 89000}}
	if got := m.workspaceTokens(ws); got != "12.3k/4.5k/89k" {
		t.Errorf("loaded = %q, want 12.3k/4.5k/89k", got)
	}

	m.tokens["app"] = tokenState{loaded: true, err: "boom"}
	if got := m.workspaceTokens(ws); got != "err" {
		t.Errorf("error = %q, want err", got)
	}
}

func TestResolveKind(t *testing.T) {
	cases := map[string]string{
		"workspaces": "workspaces",
		"workspace":  "workspaces",
		"ws":         "workspaces",
		"WS":         "workspaces", // case-insensitive
		"templates":  "templates",
		"template":   "templates",
		"tmpl":       "templates",
		"pods":       "", // unknown
		"attach":     "", // ":" is no longer a command line
		"create":     "",
		"":           "",
	}
	for in, want := range cases {
		if got := resolveKind(in); got != want {
			t.Errorf("resolveKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandSuggestionsSwitchKindsOnly(t *testing.T) {
	// Empty prompt offers every kind.
	m := model{command: ""}
	if got := m.commandSuggestions(); len(got) != 2 {
		t.Fatalf("empty prompt suggestions = %v, want both kinds", got)
	}

	// A prefix narrows by name or alias; "w" resolves to the workspaces kind only.
	m = model{command: "w"}
	if got := m.commandSuggestions(); len(got) != 1 || got[0] != "workspaces" {
		t.Fatalf("'w' suggestions = %v, want [workspaces]", got)
	}

	// Former command words are not kinds, so they suggest nothing.
	m = model{command: "att"}
	if got := m.commandSuggestions(); len(got) != 0 {
		t.Fatalf("'att' suggestions = %v, want none", got)
	}
}

func TestExecuteCommandSwitchesView(t *testing.T) {
	m := model{command: "templates", templateRegistry: workspace.NewTemplateRegistry(config.Config{WorkspaceRoot: t.TempDir()})}
	next, _ := m.executeCommand()
	if !next.(model).templatesMode {
		t.Fatal("`:templates` did not switch to the templates view")
	}

	m2 := model{command: "ws", templatesMode: true}
	next2, _ := m2.executeCommand()
	if next2.(model).templatesMode {
		t.Fatal("`:ws` did not switch back to the workspaces view")
	}
}

func TestRenderCommandBoxLayout(t *testing.T) {
	const width = 60
	for _, cmd := range []string{"", "t", "workspaces", "doesnotmatchanything"} {
		m := model{command: cmd}
		box := m.renderCommandBox(width)
		lines := strings.Split(box, "\n")
		if len(lines) != 3 {
			t.Fatalf("command box for %q has %d lines, want 3", cmd, len(lines))
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w != width {
				t.Errorf("command box for %q line %d width = %d, want %d", cmd, i, w, width)
			}
		}
	}
}

func TestHumanDuration(t *testing.T) {
	const (
		s = time.Second
		m = time.Minute
		h = time.Hour
		d = 24 * time.Hour
	)
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-5 * s, "0s"},
		{30 * s, "30s"},
		{119 * s, "119s"},
		{2 * m, "2m"},
		{2*m + 5*s, "2m5s"},
		{9*m + 30*s, "9m30s"},
		{10 * m, "10m"},
		{179 * m, "179m"},
		{3 * h, "3h"},
		{3*h + 30*m, "3h30m"},
		{47 * h, "47h"},
		{48 * h, "2d"},
		{49 * h, "2d1h"},
		{7 * d, "7d"},
		{8 * d, "8d"},
		{400 * d, "400d"},
		{800 * d, "2y70d"},
	}
	for _, c := range cases {
		if got := humanDuration(c.in); got != c.want {
			t.Errorf("humanDuration(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func updateTestModel(activity workspace.Activity) model {
	ws := workspace.Summary{Manifest: workspace.Manifest{Name: "app"}}
	return model{
		workspaces: []workspace.Summary{ws},
		statuses: map[string]workspace.Status{
			"app": {Activity: activity},
		},
	}
}

// requestDelete must open the confirmation focused on Cancel (dialogFocus 1), so
// an accidental Enter does not delete the workspace.
func TestRequestDeleteDefaultsToCancel(t *testing.T) {
	m := model{workspaces: []workspace.Summary{{Manifest: workspace.Manifest{Name: "app"}}}}

	m.requestDelete()
	if !m.confirmDelete {
		t.Fatal("requestDelete should open the confirmation dialog")
	}
	if m.dialogFocus != 1 {
		t.Fatalf("dialogFocus = %d, want 1 (Cancel)", m.dialogFocus)
	}

	// Enter on the default focus cancels rather than deletes.
	updated, _ := m.updateDeleteConfirmation(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(model).confirmDelete {
		t.Fatal("Enter on the default (Cancel) should dismiss the dialog")
	}
	if updated.(model).message != "Delete cancelled." {
		t.Fatalf("message = %q, want %q", updated.(model).message, "Delete cancelled.")
	}
}

// A workspace whose container has not been created yet must show "creating"
// while provisioning, not "missing" (the raw runtime status of a container that
// does not exist).
func TestWorkspaceStatusCreatingWhileProvisioning(t *testing.T) {
	ws := workspace.Summary{Manifest: workspace.Manifest{Name: "app"}}
	m := model{
		workspaces:   []workspace.Summary{ws},
		statuses:     map[string]workspace.Status{"app": {Container: runtime.StatusMissing}},
		provisioning: map[string]bool{"app": true},
	}

	if label, _ := m.workspaceStatus(ws); label != "creating" {
		t.Fatalf("status while provisioning = %q, want %q", label, "creating")
	}

	// Once provisioning completes the entry is gone and the raw runtime status
	// shows through again.
	delete(m.provisioning, "app")
	if label, _ := m.workspaceStatus(ws); label != "missing" {
		t.Fatalf("status after provisioning = %q, want %q", label, "missing")
	}
}

// Update must be refused while OpenCode is mid-task so the post-update restart
// cannot interrupt active work.
func TestUpdateRefusedWhileTaskRunning(t *testing.T) {
	for _, activity := range []workspace.Activity{workspace.ActivityWorking, workspace.ActivityWaiting} {
		m := updateTestModel(activity)
		next, cmd := m.updateSelected()
		if cmd != nil {
			t.Fatalf("activity %q: expected no command", activity)
		}
		if msg := next.(model).message; !strings.Contains(msg, "Cannot update") {
			t.Fatalf("activity %q: message = %q, want refusal", activity, msg)
		}
	}
}

// Update is allowed (a command is dispatched) when no task is running.
func TestUpdateDispatchedWhenIdle(t *testing.T) {
	for _, activity := range []workspace.Activity{workspace.ActivitySleeping, workspace.ActivityOff, workspace.ActivityNew} {
		m := updateTestModel(activity)
		next, cmd := m.updateSelected()
		if cmd == nil {
			t.Fatalf("activity %q: expected an update command", activity)
		}
		if msg := next.(model).message; !strings.Contains(msg, "Updating OpenCode") {
			t.Fatalf("activity %q: message = %q, want progress message", activity, msg)
		}
	}
}

func TestActionsUseK9sBindings(t *testing.T) {
	keys := map[string]string{}
	for _, a := range actions {
		keys[a.Cmd] = a.Key
	}

	// Attach has no letter shortcut anymore; it is reached via Enter or :attach.
	if key, ok := keys["attach"]; !ok || key != "" {
		t.Fatalf("attach key = %q (present=%v), want empty", key, ok)
	}

	// s opens a shell in the container (k9s uses s for shell).
	if keys["shell"] != "s" {
		t.Fatalf("shell key = %q, want s", keys["shell"])
	}

	// Start/stop is a single status-aware toggle on its own key, off s.
	if keys["toggle"] != "t" {
		t.Fatalf("toggle key = %q, want t", keys["toggle"])
	}

	// k9s guards destructive deletes behind ctrl-d, leaving d for describe.
	if keys["delete"] != "ctrl-d" {
		t.Fatalf("delete key = %q, want ctrl-d", keys["delete"])
	}
	if keys["describe"] != "d" {
		t.Fatalf("describe key = %q, want d", keys["describe"])
	}
}

func TestVersionIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.2.0", "0.1.0", true},
		{"1.0.0", "0.9.9", true},
		{"0.1.1", "0.1.0", true},
		{"v0.2.0", "0.1.0", true},      // leading v tolerated
		{"0.1.0", "0.1.0", false},      // equal
		{"0.1.0", "0.2.0", false},      // older
		{"0.1.0-rc.1", "0.1.0", false}, // pre-release ignored, equal triple
		{"0.2.0-rc.1", "0.1.0", true},
		{"dev", "0.1.0", false}, // unparseable counts as 0.0.0
	}
	for _, c := range cases {
		if got := versionIsNewer(c.latest, c.current); got != c.want {
			t.Errorf("versionIsNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
