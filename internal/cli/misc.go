package cli

import (
	"fmt"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the opencode-manager version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			if format == outputJSON {
				return printJSON(cmd.OutOrStdout(), map[string]string{
					"version": version,
					"os":      runtime.GOOS,
					"arch":    runtime.GOARCH,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "opencode-manager %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}

func newDoctorCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the runtime, config, and workspace environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ok := true

			path, _ := config.DefaultPath()
			fmt.Fprintf(out, "config:   %s\n", path)
			fmt.Fprintf(out, "runtime:  %s\n", cfg.Runtime)

			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				fmt.Fprintf(out, "          ✗ %v\n", err)
				ok = false
			} else {
				ctx, cancel := cmdContext(30 * time.Second)
				defer cancel()
				if aerr := lc.RuntimeAvailable(ctx); aerr != nil {
					fmt.Fprintf(out, "          ✗ %s not available: %v\n", cfg.Runtime, aerr)
					ok = false
				} else {
					fmt.Fprintf(out, "          ✓ %s available\n", cfg.Runtime)
				}
			}

			fmt.Fprintf(out, "image:    %s\n", cfg.BaseImage.Name)
			fmt.Fprintf(out, "root:     %s\n", cfg.WorkspaceRoot)

			workspaces, lerr := workspace.NewRegistry(cfg).List()
			if lerr != nil {
				fmt.Fprintf(out, "          ✗ listing workspaces: %v\n", lerr)
				ok = false
			} else {
				fmt.Fprintf(out, "          %d workspace(s)\n", len(workspaces))
			}

			if !ok {
				return fmt.Errorf("doctor found problems")
			}
			fmt.Fprintln(out, "\nAll checks passed.")
			return nil
		},
	}
}
