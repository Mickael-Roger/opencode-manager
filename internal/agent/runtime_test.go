package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRuntimeRegistry(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{OpenCode, DeepSeek, Claude} {
		provider, err := registry.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if provider.Name() != name {
			t.Fatalf("provider name = %q, want %q", provider.Name(), name)
		}
	}
}

func TestClaudeCommands(t *testing.T) {
	provider, _ := NewRegistry().Get(Claude)
	attach, err := provider.AttachCommand()
	if err != nil || fmt.Sprint(attach[len(attach)-2:]) != "["+ClaudeSessionSocket+" claude]" {
		t.Fatalf("AttachCommand = %v, %v", attach, err)
	}
	run, err := provider.RunCommand("hello")
	if err != nil || fmt.Sprint(run[4:]) != "[claude -p hello]" {
		t.Fatalf("RunCommand = %v, %v", run, err)
	}
}

// Claude Code runs tools itself: it must start with ~/.env loaded, and the
// prompt must reach it verbatim (not through the shell).
func TestClaudeCommandsLoadWorkspaceEnv(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("export OCM_TEST_VAR=from-env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider, _ := NewRegistry().Get(Claude)
	run, _ := provider.RunCommand(`it's "$HOME" ; $(false)`)
	// Swap the claude binary for printf to see what it would get.
	argv := append(append([]string{}, run[:4]...), "sh", "-c", `printf '%s|%s' "$OCM_TEST_VAR" "$2"`, "claude")
	argv = append(argv, run[5:]...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %v: %v", argv, err)
	}
	if got, want := string(out), `from-env|it's "$HOME" ; $(false)`; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// runClaudeAttach runs the Claude attach command with fake dtach/claude
// binaries from bin and returns what they printed.
func runClaudeAttach(t *testing.T, bin string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("export OCM_TEST_VAR=from-env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider, _ := NewRegistry().Get(Claude)
	attach, _ := provider.AttachCommand()
	cmd := exec.Command(attach[0], attach[1:]...)
	// Only the fakes on PATH, so a host dtach cannot leak into the fallback case.
	cmd.Env = []string{"HOME=" + home, "PATH=" + bin}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %v: %v", attach, err)
	}
	return string(out)
}

func writeFakeBinary(t *testing.T, dir, name string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s:%s|' \"$OCM_TEST_VAR\" " + name + "\nprintf '%s ' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Claude runs under dtach so detaching (Ctrl-Q) leaves it running and the next
// attach resumes the same live session.
func TestClaudeAttachRunsUnderDtach(t *testing.T) {
	bin := t.TempDir()
	writeFakeBinary(t, bin, "dtach")
	writeFakeBinary(t, bin, "claude")
	want := "from-env:dtach|-A " + ClaudeSessionSocket + " -e ^" + string(rune(ClaudeDetachKey)) + " -r winch -z claude "
	if got := runClaudeAttach(t, bin); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// Images built before dtach was added still attach, running Claude directly.
func TestClaudeAttachWithoutDtachRunsClaude(t *testing.T) {
	bin := t.TempDir()
	writeFakeBinary(t, bin, "claude")
	if got, want := runClaudeAttach(t, bin), "from-env:claude| "; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestDeepSeekAttachCommandStartsDshTui(t *testing.T) {
	provider, _ := NewRegistry().Get(DeepSeek)
	command, err := provider.AttachCommand()
	if err != nil {
		t.Fatalf("AttachCommand: %v", err)
	}
	if got, want := fmt.Sprint(command), "[/usr/local/bin/dsh-tui --continue]"; got != want {
		t.Errorf("AttachCommand = %s, want %s", got, want)
	}
}

func TestDeepSeekDoesNotAdvertiseHeadlessRun(t *testing.T) {
	provider, _ := NewRegistry().Get(DeepSeek)
	if _, err := provider.RunCommand("hello"); err == nil {
		t.Fatal("RunCommand returned nil error")
	}
}
