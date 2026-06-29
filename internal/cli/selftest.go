package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// newSelftestCmd builds the hidden `selftest` command: the headless engine
// behind the docker/podman integration tests in CI. It exercises the full happy
// path against a real container runtime (make the base image available, create a
// workspace, assert the container runs and OpenCode responds, then clean up). It
// is intentionally hidden from help and loads its own --config rather than the
// ambient one.
func newSelftestCmd() *cobra.Command {
	var configPath, name string
	var keep, reuse bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:    "selftest",
		Short:  "Headless integration self-test (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if configPath == "" {
				return fmt.Errorf("selftest: --config <path> is required")
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config %q: %w", configPath, err)
			}

			lifecycle, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			registry := workspace.NewRegistry(cfg)

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "[selftest] runtime=%s base=%s reuse=%v\n", cfg.Runtime, cfg.BaseImage.Name, reuse)

			var summary workspace.Summary
			if reuse {
				existing, err := findExisting(registry, name)
				if err != nil {
					return err
				}
				if existing == nil {
					return fmt.Errorf("--reuse: workspace %q does not exist", name)
				}
				fmt.Fprintf(out, "[selftest] reusing existing workspace %q\n", name)
				summary = *existing
			} else {
				if existing, err := findExisting(registry, name); err == nil && existing != nil {
					fmt.Fprintf(out, "[selftest] removing leftover workspace %q\n", name)
					_ = lifecycle.Delete(ctx, *existing)
				}

				fmt.Fprintln(out, "[selftest] ensuring base image…")
				if err := lifecycle.EnsureBaseImage(ctx); err != nil {
					return fmt.Errorf("ensure base image: %w", err)
				}

				fmt.Fprintf(out, "[selftest] creating workspace %q…\n", name)
				created, err := registry.Create(name)
				if err != nil {
					return fmt.Errorf("create workspace: %w", err)
				}
				summary = workspace.Summary{Manifest: created.Manifest, Path: created.Path}
			}

			if !keep {
				defer func() {
					fmt.Fprintln(out, "[selftest] cleaning up…")
					if err := lifecycle.Delete(ctx, summary); err != nil {
						fmt.Fprintf(out, "[selftest] cleanup failed: %v\n", err)
					}
				}()
			}

			fmt.Fprintln(out, "[selftest] starting workspace container…")
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
			fmt.Fprintln(out, "[selftest] container is running")

			version, err := lifecycle.OpenCodeVersion(ctx, summary)
			if err != nil {
				return fmt.Errorf("query OpenCode: %w", err)
			}
			fmt.Fprintf(out, "[selftest] OpenCode responded: %s\n", version)

			fmt.Fprintln(out, "[selftest] OK")
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "path to the config.yaml to test with (required)")
	cmd.Flags().StringVar(&name, "name", "ocm-selftest", "workspace name to create")
	cmd.Flags().BoolVar(&keep, "keep", false, "keep the workspace and image instead of cleaning up")
	cmd.Flags().BoolVar(&reuse, "reuse", false, "start and verify an existing workspace instead of creating one")
	cmd.Flags().DurationVar(&timeout, "timeout", 20*time.Minute, "overall timeout")
	return cmd
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
