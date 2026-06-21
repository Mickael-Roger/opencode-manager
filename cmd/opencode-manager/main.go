package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/tui"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	if len(os.Args) > 1 {
		if err := runCLI(cfg, os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := tui.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "failed to run TUI: %v\n", err)
		os.Exit(1)
	}
}

func runCLI(cfg config.Config, args []string) error {
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return usageError()
		}
		return listWorkspaces(cfg)
	case "attach":
		if len(args) != 2 {
			return usageError()
		}
		return attachWorkspace(cfg, args[1])
	default:
		return usageError()
	}
}

func listWorkspaces(cfg config.Config) error {
	workspaces, err := workspace.NewRegistry(cfg).List()
	if err != nil {
		return err
	}
	for _, summary := range workspaces {
		fmt.Println(summary.Manifest.Name)
	}
	return nil
}

func attachWorkspace(cfg config.Config, name string) error {
	selected, err := findWorkspace(cfg, name)
	if err != nil {
		return err
	}
	lifecycle, err := workspace.NewLifecycle(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd, err := lifecycle.AttachCommand(ctx, selected)
	if err != nil {
		return err
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func findWorkspace(cfg config.Config, name string) (workspace.Summary, error) {
	workspaces, err := workspace.NewRegistry(cfg).List()
	if err != nil {
		return workspace.Summary{}, err
	}
	for _, summary := range workspaces {
		if summary.Manifest.Name == name || workspace.SafeName(summary.Manifest.Name) == name {
			return summary, nil
		}
	}
	return workspace.Summary{}, fmt.Errorf("workspace %q not found", name)
}

func usageError() error {
	return fmt.Errorf("usage:\n  opencode-manager list\n  opencode-manager attach <workspace>")
}
