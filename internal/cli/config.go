package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/mickael-menu/opencode-manager/internal/config"
)

func newConfigCmd(cfg config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View, locate, or edit the global config.yaml",
	}
	cmd.AddCommand(
		newConfigViewCmd(cfg),
		newConfigPathCmd(),
		newConfigEditCmd(cfg),
	)
	return cmd
}

func newConfigViewCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Print the effective configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			if format == outputJSON {
				return printJSON(cmd.OutOrStdout(), cfg)
			}
			// Prefer the on-disk file verbatim so comments are preserved; fall back
			// to the marshaled effective config when no file exists yet.
			path, err := config.DefaultPath()
			if err != nil {
				return err
			}
			if data, rerr := os.ReadFile(path); rerr == nil {
				_, werr := cmd.OutOrStdout().Write(data)
				return werr
			} else if !errors.Is(rerr, os.ErrNotExist) {
				return rerr
			}
			data, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "# no config file at %s; showing defaults\n", path)
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path to the global config.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := config.DefaultPath()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}

func newConfigEditCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the global config.yaml in $EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := config.DefaultPath()
			if err != nil {
				return err
			}
			// Seed the file from the effective config so an editor never opens a
			// blank buffer on a fresh install.
			if _, serr := os.Stat(path); errors.Is(serr, os.ErrNotExist) {
				data, merr := yaml.Marshal(cfg)
				if merr != nil {
					return merr
				}
				if werr := os.WriteFile(path, data, 0o600); werr != nil {
					return fmt.Errorf("create %s: %w", path, werr)
				}
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			c := exec.Command(editor, path)
			if err := runInteractive(c); err != nil {
				return err
			}
			// Re-validate so a typo surfaces immediately instead of at next launch.
			if _, err := config.Load(path); err != nil {
				return fmt.Errorf("saved %s but it is invalid: %w", path, err)
			}
			return nil
		},
	}
}
