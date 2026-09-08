package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/agent"
	"github.com/mickael-menu/opencode-manager/internal/config"
)

func TestSyncWorkspaceOpenCodeConfigCopiesAndUpdatesSource(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := config.EnsureGlobalConfig(); err != nil {
		t.Fatal(err)
	}
	source, err := config.OpenCodeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "agents", "review.md"), []byte("first"))
	home := t.TempDir()

	if err := syncWorkspaceOpenCodeConfig(home); err != nil {
		t.Fatalf("syncWorkspaceOpenCodeConfig returned error: %v", err)
	}
	destination := filepath.Join(home, ".config", "opencode", "agents", "review.md")
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "first" {
		t.Fatalf("copied file = %q, %v; want first, nil", data, err)
	}

	writeTestFile(t, filepath.Join(source, "agents", "review.md"), []byte("second"))
	if err := syncWorkspaceOpenCodeConfig(home); err != nil {
		t.Fatalf("second sync returned error: %v", err)
	}
	data, err = os.ReadFile(destination)
	if err != nil || string(data) != "second" {
		t.Fatalf("updated file = %q, %v; want second, nil", data, err)
	}
}

func TestSyncWorkspaceOpenCodeConfigRemovesOnlyJournaledEntries(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := config.EnsureGlobalConfig(); err != nil {
		t.Fatal(err)
	}
	source, _ := config.OpenCodeDir()
	managed := filepath.Join(source, "commands", "shared.md")
	writeTestFile(t, managed, []byte("shared"))
	home := t.TempDir()
	destination := filepath.Join(home, ".config", "opencode")
	writeTestFile(t, filepath.Join(destination, "commands", "local.md"), []byte("local"))

	if err := syncWorkspaceOpenCodeConfig(home); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}
	if err := syncWorkspaceOpenCodeConfig(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "commands", "shared.md")); !os.IsNotExist(err) {
		t.Fatalf("managed file remains or stat failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "commands", "local.md"))
	if err != nil || string(data) != "local" {
		t.Fatalf("local file = %q, %v; want preserved", data, err)
	}
}

func TestSyncWorkspaceOpenCodeConfigRejectsReservedTopLevelState(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := config.EnsureGlobalConfig(); err != nil {
		t.Fatal(err)
	}
	source, _ := config.OpenCodeDir()
	writeTestFile(t, filepath.Join(source, "package.json"), []byte("{}"))

	err := syncWorkspaceOpenCodeConfig(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "package.json") || !strings.Contains(err.Error(), "generated per workspace") {
		t.Fatalf("sync error = %v, want reserved source error", err)
	}
}

func TestSyncWorkspaceOpenCodeConfigKeepsGeneratedState(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := config.EnsureGlobalConfig(); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	destination := filepath.Join(home, ".config", "opencode")
	writeTestFile(t, filepath.Join(destination, "package.json"), []byte("{\"private\":true}"))

	if err := syncWorkspaceOpenCodeConfig(home); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "package.json"))
	if err != nil || string(data) != "{\"private\":true}" {
		t.Fatalf("generated state = %q, %v; want preserved", data, err)
	}
}

func TestStartOpenCodeConfigSyncWatchesSourceChanges(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	cfg := testConfig(t)
	created, err := NewRegistry(cfg).Create("demo")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := StartOpenCodeConfigSync(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	source, err := config.OpenCodeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "skills", "watched.md"), []byte("watched"))
	destination := filepath.Join(created.Manifest.HomeDir, ".config", "opencode", "skills", "watched.md")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(destination)
		if err == nil && string(data) == "watched" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("watcher did not copy %q before deadline", destination)
}

func TestSyncWorkspaceDeepSeekConfigCopiesAndPreservesState(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := config.EnsureGlobalConfig(); err != nil {
		t.Fatal(err)
	}
	source, err := config.DeepSeekDir()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "settings.yaml"), []byte("model: openai-codex\n"))
	writeTestFile(t, filepath.Join(source, "patches", "team.yaml"), []byte("enabled: true\n"))

	home := t.TempDir()
	destination := filepath.Join(home, ".config", "deepseek")
	writeTestFile(t, filepath.Join(destination, ".credentials.yaml"), []byte("version: 1\nrefs: {}\n"))
	writeTestFile(t, filepath.Join(destination, "sessions", "session.jsonl"), []byte("private session"))
	if err := syncWorkspaceDeepSeekConfig(home); err != nil {
		t.Fatalf("syncWorkspaceDeepSeekConfig returned error: %v", err)
	}
	for path, want := range map[string]string{
		"settings.yaml":                            "model: openai-codex\n",
		filepath.Join("patches", "team.yaml"):      "enabled: true\n",
		".credentials.yaml":                        "version: 1\nrefs: {}\n",
		filepath.Join("sessions", "session.jsonl"): "private session",
	} {
		data, err := os.ReadFile(filepath.Join(destination, path))
		if err != nil || string(data) != want {
			t.Fatalf("file %q = %q, %v; want %q", path, data, err, want)
		}
	}

	if err := os.Remove(filepath.Join(source, "settings.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := syncWorkspaceDeepSeekConfig(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "settings.yaml")); !os.IsNotExist(err) {
		t.Fatalf("removed managed setting remains or stat failed: %v", err)
	}
}

func TestSyncWorkspaceDeepSeekConfigSyncsProfileManifestAndIgnoresRuntimeState(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := config.EnsureGlobalConfig(); err != nil {
		t.Fatal(err)
	}
	source, _ := config.DeepSeekDir()
	writeTestFile(t, filepath.Join(source, ".credentials.yaml"), []byte("secret"))
	writeTestFile(t, filepath.Join(source, "profiles", "web", "package.json"), []byte("{}"))
	writeTestFile(t, filepath.Join(source, "profiles", "web", "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"))
	writeTestFile(t, filepath.Join(source, "profiles", "web", "cordis.patch.yml"), []byte("plugins: []\n"))

	home := t.TempDir()
	if err := syncWorkspaceDeepSeekConfig(home); err != nil {
		t.Fatalf("sync error = %v", err)
	}
	destination := filepath.Join(home, ".config", "deepseek")
	if _, err := os.Stat(filepath.Join(destination, ".credentials.yaml")); !os.IsNotExist(err) {
		t.Fatalf("credentials were copied or stat failed: %v", err)
	}
	profileDestination := filepath.Join(destination, "profiles", "web")
	if data, err := os.ReadFile(filepath.Join(profileDestination, "package.json")); err != nil || string(data) != "{}" {
		t.Fatalf("profile manifest = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(profileDestination, "pnpm-lock.yaml")); !os.IsNotExist(err) {
		t.Fatalf("profile lockfile was copied or stat failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(profileDestination, "cordis.patch.yml"))
	if err != nil || string(data) != "plugins: []\n" {
		t.Fatalf("profile patch = %q, %v", data, err)
	}
}

func TestStartDeepSeekConfigSyncWatchesEnabledWorkspace(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	cfg := testConfig(t)
	created, err := NewRegistry(cfg).CreateWithOptions("deepseek", CreateOptions{DefaultRuntime: agent.DeepSeek})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := StartDeepSeekConfigSync(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	source, err := config.DeepSeekDir()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "settings.yaml"), []byte("provider: openai-codex\n"))
	destination := filepath.Join(created.Manifest.HomeDir, ".config", "deepseek", "settings.yaml")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(destination)
		if err == nil && string(data) == "provider: openai-codex\n" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("watcher did not copy %q before deadline", destination)
}
