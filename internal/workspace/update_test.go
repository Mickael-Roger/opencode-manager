package workspace

import (
	"context"
	"strings"
	"testing"
)

func TestUpdateOpenCodeRunsNpmAndRestarts(t *testing.T) {
	fake := &fakeDriver{output: func(args []string) []byte {
		if contains(args, "--version") {
			return []byte("0.5.7\n")
		}
		return nil
	}}

	l := Lifecycle{driver: fake}
	version, err := l.UpdateOpenCode(context.Background(), Summary{Manifest: Manifest{ContainerName: "c", HomeDir: t.TempDir(), OpenCodePort: 4096}})
	if err != nil {
		t.Fatalf("UpdateOpenCode error: %v", err)
	}

	if version != "0.5.7" {
		t.Errorf("version=%q want %q", version, "0.5.7")
	}

	var sawInstall, sawPatch, sawPatchDirectory bool
	for _, args := range fake.gotArgs {
		if strings.Join(args, " ") == "npm install -g opencode-ai@latest" {
			sawInstall = true
		}
		if strings.Join(args, " ") == "opencode-manager-patch-pending-prompts --force" {
			sawPatch = true
		}
		if strings.Join(args, " ") == "mkdir -p /usr/local/share/opencode-manager" {
			sawPatchDirectory = true
		}
	}
	if !sawInstall {
		t.Errorf("expected npm install -g opencode-ai@latest, got calls: %v", fake.gotArgs)
	}
	if !sawPatch {
		t.Errorf("expected pending-prompt patch, got calls: %v", fake.gotArgs)
	}
	if !sawPatchDirectory {
		t.Errorf("expected pending-prompt patch directory, got calls: %v", fake.gotArgs)
	}
	if len(fake.copies) != 2 {
		t.Errorf("copied patch assets=%d, want 2", len(fake.copies))
	}

	// The container must be restarted once so the persistent `opencode serve`
	// process reloads the upgraded binary.
	if fake.stopped != 1 || fake.started != 1 {
		t.Errorf("restart = stop:%d start:%d, want 1 and 1", fake.stopped, fake.started)
	}
}
