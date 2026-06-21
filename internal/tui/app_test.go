package tui

import "testing"

func TestCommandSurfaceUsesAttachShortcut(t *testing.T) {
	shortcuts := map[string]string{}
	for _, command := range commands {
		shortcuts[command.Name] = command.Shortcut
		if command.Name == "/open" || command.Name == "/start" {
			t.Fatalf("commands contains removed command %s", command.Name)
		}
	}

	if shortcuts["/attach"] != "a" {
		t.Fatalf("/attach shortcut = %q, want a", shortcuts["/attach"])
	}
}
