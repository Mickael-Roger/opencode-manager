package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/config"
)

func TestInitCreatesLogFileAndFiltersByLevel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")

	closeLog, err := Init(dir, config.LogLevelWarning)
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer closeLog()

	slog.Info("info message that should be filtered out")
	slog.Warn("warning message that should be written")

	data, err := os.ReadFile(filepath.Join(dir, LogFileName))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	out := string(data)
	if strings.Contains(out, "filtered out") {
		t.Fatalf("info message should not appear at warning level: %q", out)
	}
	if !strings.Contains(out, "should be written") {
		t.Fatalf("warning message should appear: %q", out)
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		config.LogLevelDebug:   slog.LevelDebug,
		config.LogLevelInfo:    slog.LevelInfo,
		config.LogLevelWarning: slog.LevelWarn,
		config.LogLevelError:   slog.LevelError,
		"unknown":              slog.LevelWarn,
	}
	for level, want := range cases {
		if got := ParseLevel(level); got != want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", level, got, want)
		}
	}
}
