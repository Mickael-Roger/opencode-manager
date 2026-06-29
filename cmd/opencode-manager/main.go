package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mickael-menu/opencode-manager/internal/cli"
	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/logging"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logDir, err := config.LogDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve log directory: %v\n", err)
		os.Exit(1)
	}
	closeLog, err := logging.Init(logDir, cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logging: %v\n", err)
		os.Exit(1)
	}
	defer closeLog()

	slog.Info("starting opencode-manager", "logLevel", cfg.LogLevel, "runtime", cfg.Runtime)

	if err := config.EnsureGlobalConfig(); err != nil {
		slog.Error("failed to prepare global config", "error", err)
		fmt.Fprintf(os.Stderr, "failed to prepare global config: %v\n", err)
		os.Exit(1)
	}

	if err := workspace.SeedStatusPlugin(); err != nil {
		slog.Error("failed to seed status plugin", "error", err)
		fmt.Fprintf(os.Stderr, "failed to seed status plugin: %v\n", err)
		os.Exit(1)
	}

	if err := cli.NewRootCommand(cfg).Execute(); err != nil {
		slog.Error("command failed", "args", os.Args[1:], "error", err)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
