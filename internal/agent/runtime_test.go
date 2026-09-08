package agent

import (
	"fmt"
	"testing"
)

func TestRuntimeRegistry(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{OpenCode, DeepSeek} {
		provider, err := registry.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if provider.Name() != name {
			t.Fatalf("provider name = %q, want %q", provider.Name(), name)
		}
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
