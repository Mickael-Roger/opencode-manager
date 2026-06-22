// Package logging configures a file-based slog logger for opencode-manager.
//
// Because the manager runs as a TUI, log output cannot go to stderr without
// corrupting the interface. Logs are instead appended to a file under the data
// directory (~/.local/share/opencode-manager/logs/opencode-manager.log) and
// filtered by the configured level.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mickael-menu/opencode-manager/internal/config"
)

// LogFileName is the name of the log file created inside the log directory.
const LogFileName = "opencode-manager.log"

// Init creates the log directory if needed, opens the log file for appending,
// installs a slog logger filtered at the given level as the default logger, and
// returns a close function that releases the file. The level string matches the
// config.LogLevel* values; unknown values fall back to the warning level.
func Init(dir, level string) (func() error, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory %q: %w", dir, err)
	}

	path := filepath.Join(dir, LogFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file %q: %w", path, err)
	}

	handler := slog.NewTextHandler(file, &slog.HandlerOptions{Level: ParseLevel(level)})
	slog.SetDefault(slog.New(handler))

	return file.Close, nil
}

// ParseLevel maps a config.LogLevel* value to a slog.Level. Unknown values fall
// back to the warning level, matching the configuration default.
func ParseLevel(level string) slog.Level {
	switch level {
	case config.LogLevelDebug:
		return slog.LevelDebug
	case config.LogLevelInfo:
		return slog.LevelInfo
	case config.LogLevelError:
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}
