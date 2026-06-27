package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// runSelftest exercises the full happy path against a real container runtime:
// it makes the base image available, creates a workspace, asserts the container
// is running and OpenCode responds, then cleans up. It is the headless engine
// behind the docker/podman integration tests in CI, which run it twice with a
// config pointing at each runtime.
//
// It is intentionally not part of the public CLI (which stays `list`/`attach`)
// and is omitted from the usage message.
func runSelftest(args []string) error {
	fs := flag.NewFlagSet("selftest", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to the config.yaml to test with (required)")
	name := fs.String("name", "ocm-selftest", "workspace name to create")
	keep := fs.Bool("keep", false, "keep the workspace and image instead of cleaning up")
	reuse := fs.Bool("reuse", false, "start and verify an existing workspace instead of creating one (for upgrade tests)")
	timeout := fs.Duration("timeout", 20*time.Minute, "overall timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("selftest: --config <path> is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config %q: %w", *configPath, err)
	}

	lifecycle, err := workspace.NewLifecycle(cfg)
	if err != nil {
		return err
	}
	registry := workspace.NewRegistry(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	fmt.Printf("[selftest] runtime=%s base=%s reuse=%v\n", cfg.Runtime, cfg.BaseImage.Name, *reuse)

	var summary workspace.Summary
	if *reuse {
		// Upgrade test: the workspace was created by a previous (older) install;
		// verify the new binary can start it again.
		existing, err := findExisting(registry, *name)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("--reuse: workspace %q does not exist", *name)
		}
		fmt.Printf("[selftest] reusing existing workspace %q\n", *name)
		summary = *existing
	} else {
		// Start from a clean slate so reruns are idempotent.
		if existing, err := findExisting(registry, *name); err == nil && existing != nil {
			fmt.Printf("[selftest] removing leftover workspace %q\n", *name)
			_ = lifecycle.Delete(ctx, *existing)
		}

		fmt.Println("[selftest] ensuring base image…")
		if err := lifecycle.EnsureBaseImage(ctx); err != nil {
			return fmt.Errorf("ensure base image: %w", err)
		}

		fmt.Printf("[selftest] creating workspace %q…\n", *name)
		created, err := registry.Create(*name)
		if err != nil {
			return fmt.Errorf("create workspace: %w", err)
		}
		summary = workspace.Summary{Manifest: created.Manifest, Path: created.Path}
	}

	if !*keep {
		defer func() {
			fmt.Println("[selftest] cleaning up…")
			if err := lifecycle.Delete(ctx, summary); err != nil {
				fmt.Printf("[selftest] cleanup failed: %v\n", err)
			}
		}()
	}

	fmt.Println("[selftest] starting workspace container…")
	if err := lifecycle.EnsureStarted(ctx, summary); err != nil {
		return fmt.Errorf("start workspace: %w", err)
	}

	statuses := lifecycle.Statuses(ctx, []workspace.Summary{summary})
	if len(statuses) != 1 || statuses[0].Container != runtime.StatusRunning {
		got := "no status"
		if len(statuses) == 1 {
			got = statuses[0].Container
			if statuses[0].Error != "" {
				got += " (" + statuses[0].Error + ")"
			}
		}
		return fmt.Errorf("workspace container not running: got %s", got)
	}
	fmt.Println("[selftest] container is running")

	version, err := lifecycle.OpenCodeVersion(ctx, summary)
	if err != nil {
		return fmt.Errorf("query OpenCode: %w", err)
	}
	fmt.Printf("[selftest] OpenCode responded: %s\n", version)

	fmt.Println("[selftest] OK")
	return nil
}

// findExisting returns the workspace with the given name, or nil if absent.
func findExisting(registry workspace.Registry, name string) (*workspace.Summary, error) {
	workspaces, err := registry.List()
	if err != nil {
		return nil, err
	}
	for i := range workspaces {
		if workspaces[i].Manifest.Name == name || workspace.SafeName(workspaces[i].Manifest.Name) == workspace.SafeName(name) {
			return &workspaces[i], nil
		}
	}
	return nil, nil
}
