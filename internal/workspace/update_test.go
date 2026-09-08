package workspace

import (
	"context"
	"strings"
	"testing"
)

func TestUpdateRuntimesRunsNpmAndRestarts(t *testing.T) {
	fake := &fakeDriver{output: func(args []string) []byte {
		switch args[0] {
		case "opencode":
			return []byte("0.5.7\n")
		case "dsh":
			return []byte("0.1.2\n")
		case "dsh-acp":
			return []byte("0.4.29\n")
		case "pnpm":
			return []byte("11.25.0\n")
		}
		return nil
	}}

	l := Lifecycle{driver: fake}
	versions, err := l.UpdateRuntimes(context.Background(), Summary{Manifest: Manifest{ContainerName: "c", HomeDir: t.TempDir(), OpenCodePort: 4096}})
	if err != nil {
		t.Fatalf("UpdateRuntimes error: %v", err)
	}

	if versions.OpenCode != "0.5.7" || versions.DeepSeek != "0.1.2" || versions.ACP != "0.4.29" || versions.PNPM != "11.25.0" {
		t.Errorf("versions=%#v", versions)
	}

	var sawInstall bool
	for _, args := range fake.gotArgs {
		if strings.Join(args, " ") == "npm install -g opencode-ai@latest @deepseek-ai/dsh@latest @openma/deepseek-harness-acp@latest pnpm@latest" {
			sawInstall = true
		}
	}
	if !sawInstall {
		t.Errorf("expected npm install for all agent runtimes, got calls: %v", fake.gotArgs)
	}

	// The container must be restarted once so the persistent `opencode serve`
	// process reloads the upgraded binary.
	if fake.stopped != 1 || fake.started != 1 {
		t.Errorf("restart = stop:%d start:%d, want 1 and 1", fake.stopped, fake.started)
	}
}
