package tui

import (
	"strings"
	"testing"

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

	m.tokens["app"] = tokenState{loaded: true, usage: workspace.TokenUsage{TotalInput: 12345, TotalOutput: 4500}}
	if got := m.workspaceTokens(ws); got != "12.3k/4.5k" {
		t.Errorf("loaded = %q, want 12.3k/4.5k", got)
	}

	m.tokens["app"] = tokenState{loaded: true, err: "boom"}
	if got := m.workspaceTokens(ws); got != "err" {
		t.Errorf("error = %q, want err", got)
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
