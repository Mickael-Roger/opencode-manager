package tui

import "testing"

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
