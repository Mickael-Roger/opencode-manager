package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// workspaceJSON is the stable machine-readable shape of a workspace for
// `--output json`. It is decoupled from the internal Manifest so the JSON
// contract does not shift when internal fields change.
type workspaceJSON struct {
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Status    string    `json:"status"`
	Activity  string    `json:"activity"`
	Pending   int       `json:"pending"`
	Runtime   string    `json:"runtime"`
	Image     string    `json:"image"`
	Container string    `json:"container"`
	Port      int       `json:"port"`
	Modules   []string  `json:"modules"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
}

func statusToJSON(s workspace.Status) workspaceJSON {
	m := s.Workspace.Manifest
	mods := make([]string, 0, len(m.Modules))
	for _, inst := range m.Modules {
		mods = append(mods, inst.InstanceID())
	}
	return workspaceJSON{
		Name:      m.Name,
		Slug:      workspace.SafeName(m.Name),
		Status:    s.Container,
		Activity:  string(s.Activity),
		Pending:   s.Pending,
		Runtime:   m.Runtime,
		Image:     m.ImageName,
		Container: m.ContainerName,
		Port:      m.OpenCodePort,
		Modules:   mods,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
		Error:     s.Error,
	}
}

// activityLabel renders an Activity (plus any pending approvals) for a column.
func activityLabel(a workspace.Activity, pending int) string {
	if a == workspace.ActivityWaiting && pending > 0 {
		return fmt.Sprintf("waiting(%d)", pending)
	}
	if a == workspace.ActivityUnknown {
		return "-"
	}
	return string(a)
}

func newWorkspacesCmd(cfg config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspaces",
		Aliases: []string{"ws", "workspace"},
		Short:   "Manage workspaces",
	}
	cmd.AddCommand(
		newWorkspacesListCmd(cfg),
		newWorkspacesGetCmd(cfg),
		newWorkspacesCreateCmd(cfg),
		newWorkspacesDeleteCmd(cfg),
		newWorkspacesStartCmd(cfg),
		newWorkspacesStopCmd(cfg),
		newWorkspacesRestartCmd(cfg),
		newWorkspacesAttachCmd(cfg),
		newWorkspacesShellCmd(cfg),
		newWorkspacesExecCmd(cfg),
		newWorkspacesRunCmd(cfg),
		newWorkspacesUpdateCmd(cfg),
		newWorkspacesVersionCmd(cfg),
	)
	return cmd
}

func newWorkspacesListCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List workspaces and their status",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			workspaces, err := workspace.NewRegistry(cfg).List()
			if err != nil {
				return err
			}
			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := cmdContext(60 * time.Second)
			defer cancel()
			statuses := lc.Statuses(ctx, workspaces)

			if format == outputJSON {
				out := make([]workspaceJSON, 0, len(statuses))
				for _, s := range statuses {
					out = append(out, statusToJSON(s))
				}
				return printJSON(cmd.OutOrStdout(), out)
			}

			if len(statuses) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No workspaces. Create one with `ocm workspaces create <name>`.")
				return nil
			}
			tw := newTable(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tSTATUS\tACTIVITY\tMODULES\tAGE")
			for _, s := range statuses {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
					s.Workspace.Manifest.Name,
					s.Container,
					activityLabel(s.Activity, s.Pending),
					len(s.Workspace.Manifest.Modules),
					age(s.Workspace.Manifest.CreatedAt),
				)
			}
			return tw.Flush()
		},
	}
}

func newWorkspacesGetCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:     "get <workspace>",
		Aliases: []string{"describe"},
		Short:   "Show a workspace's details, status, and token usage",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			s, err := findWorkspace(cfg, args[0])
			if err != nil {
				return err
			}
			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := cmdContext(60 * time.Second)
			defer cancel()
			st := lc.Statuses(ctx, []workspace.Summary{s})[0]

			// Version and token usage need a running container; best-effort.
			var ocVersion string
			var usage *workspace.TokenUsage
			if st.Container == runtime.StatusRunning {
				ocVersion, _ = lc.OpenCodeVersion(ctx, s)
				if u, uerr := lc.TokenUsage(ctx, s); uerr == nil {
					usage = &u
				}
			}

			if format == outputJSON {
				return printJSON(cmd.OutOrStdout(), newWorkspaceDetailJSON(st, ocVersion, usage))
			}
			return printWorkspaceDetail(cmd.OutOrStdout(), st, ocVersion, usage)
		},
	}
}

func newWorkspacesCreateCmd(cfg config.Config) *cobra.Command {
	var template string
	var start bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a workspace, optionally from a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			result, err := workspace.NewRegistry(cfg).Create(name)
			if err != nil {
				return err
			}

			if template != "" {
				tmpl, err := workspace.NewTemplateRegistry(cfg).Load(template)
				if err != nil {
					return fmt.Errorf("created workspace %q but loading template %q failed: %w", result.Manifest.Name, template, err)
				}
				manifest := result.Manifest
				manifest.Modules = tmpl.Modules
				manifest.UpdatedAt = time.Now().UTC()
				if err := workspace.SaveManifest(filepath.Join(result.Path, workspace.ManifestFile), manifest); err != nil {
					return fmt.Errorf("created workspace %q but applying template %q failed: %w", result.Manifest.Name, template, err)
				}
				result.Manifest = manifest
			}

			summary := workspace.Summary{Manifest: result.Manifest, Path: result.Path}
			if start {
				lc, err := workspace.NewLifecycle(cfg)
				if err != nil {
					return err
				}
				ctx, cancel := cmdContext(15 * time.Minute)
				defer cancel()
				fmt.Fprintf(cmd.OutOrStdout(), "Created workspace %q; building image and starting container...\n", result.Manifest.Name)
				if err := lc.EnsureStarted(ctx, summary); err != nil {
					return fmt.Errorf("created workspace %q but starting it failed: %w", result.Manifest.Name, err)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created workspace %q.\n", result.Manifest.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&template, "template", "t", "", "apply a template's modules to the new workspace")
	cmd.Flags().BoolVar(&start, "start", false, "build the image and start the container after creating")
	return cmd
}

func newWorkspacesDeleteCmd(cfg config.Config) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "delete <workspace>",
		Aliases: []string{"rm"},
		Short:   "Delete a workspace, its container, and its image",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := findWorkspace(cfg, args[0])
			if err != nil {
				return err
			}
			if !force && !confirm(cmd, fmt.Sprintf("Delete workspace %q and all its data?", s.Manifest.Name)) {
				fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}
			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := cmdContext(5 * time.Minute)
			defer cancel()
			if err := lc.Delete(ctx, s); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted workspace %q.\n", s.Manifest.Name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "do not prompt for confirmation")
	return cmd
}

func newWorkspacesStartCmd(cfg config.Config) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "start [workspace]",
		Short: "Start a workspace container (building its image if needed)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return forEachTarget(cfg, cmd, args, all, 15*time.Minute, "Started",
				func(lc workspace.Lifecycle, ctx context.Context, s workspace.Summary) error {
					return lc.EnsureStarted(ctx, s)
				})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "start every workspace")
	return cmd
}

func newWorkspacesStopCmd(cfg config.Config) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "stop [workspace]",
		Short: "Stop a running workspace container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return forEachTarget(cfg, cmd, args, all, 2*time.Minute, "Stopped",
				func(lc workspace.Lifecycle, ctx context.Context, s workspace.Summary) error {
					return lc.Stop(ctx, s)
				})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "stop every running workspace")
	return cmd
}

func newWorkspacesRestartCmd(cfg config.Config) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "restart [workspace]",
		Short: "Restart a workspace container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return forEachTarget(cfg, cmd, args, all, 15*time.Minute, "Restarted",
				func(lc workspace.Lifecycle, ctx context.Context, s workspace.Summary) error {
					if err := lc.Stop(ctx, s); err != nil {
						return err
					}
					return lc.EnsureStarted(ctx, s)
				})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "restart every workspace")
	return cmd
}

func newWorkspacesUpdateCmd(cfg config.Config) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "update [workspace]",
		Short: "Update OpenCode to the latest release inside the workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			return forEachTarget(cfg, cmd, args, all, 15*time.Minute, "Updated",
				func(lc workspace.Lifecycle, ctx context.Context, s workspace.Summary) error {
					v, err := lc.UpdateOpenCode(ctx, s)
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "  %s now on OpenCode %s\n", s.Manifest.Name, v)
					return nil
				})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "update OpenCode in every workspace")
	return cmd
}

func newWorkspacesVersionCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "version <workspace>",
		Short: "Print the OpenCode version running in a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := findWorkspace(cfg, args[0])
			if err != nil {
				return err
			}
			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := cmdContext(15 * time.Minute)
			defer cancel()
			if err := lc.EnsureStarted(ctx, s); err != nil {
				return err
			}
			v, err := lc.OpenCodeVersion(ctx, s)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), v)
			return nil
		},
	}
}

func newWorkspacesAttachCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "attach <workspace>",
		Short: "Attach the terminal to a workspace's OpenCode session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := findWorkspace(cfg, args[0])
			if err != nil {
				return err
			}
			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := cmdContext(15 * time.Minute)
			defer cancel()
			c, err := lc.AttachCommand(ctx, s)
			if err != nil {
				return err
			}
			return runInteractive(c)
		},
	}
}

func newWorkspacesShellCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:     "shell <workspace>",
		Aliases: []string{"sh"},
		Short:   "Open an interactive shell inside a workspace container",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := findWorkspace(cfg, args[0])
			if err != nil {
				return err
			}
			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := cmdContext(15 * time.Minute)
			defer cancel()
			c, err := lc.ShellCommand(ctx, s)
			if err != nil {
				return err
			}
			return runInteractive(c)
		},
	}
}

func newWorkspacesExecCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <workspace> -- <command> [args...]",
		Short: "Run a one-off command inside a workspace container",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := findWorkspace(cfg, args[0])
			if err != nil {
				return err
			}
			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := cmdContext(15 * time.Minute)
			defer cancel()
			c, err := lc.ExecCommand(ctx, s, args[1:])
			if err != nil {
				return err
			}
			return runInteractive(c)
		},
	}
}

func newWorkspacesRunCmd(cfg config.Config) *cobra.Command {
	var prompt, promptFile string
	cmd := &cobra.Command{
		Use:   "run <workspace>",
		Short: "Run a non-interactive OpenCode prompt in a workspace (headless)",
		Long: "Run a single OpenCode turn non-interactively inside the workspace and\n" +
			"print its result. Provide the prompt with --prompt, --prompt-file, or on stdin.\n" +
			"Designed for scripts, CI, and pipelines.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := resolvePrompt(prompt, promptFile)
			if err != nil {
				return err
			}
			s, err := findWorkspace(cfg, args[0])
			if err != nil {
				return err
			}
			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := cmdContext(15 * time.Minute)
			defer cancel()
			c, err := lc.RunCommand(ctx, s, text)
			if err != nil {
				return err
			}
			return runInteractive(c)
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "the prompt to run")
	cmd.Flags().StringVar(&promptFile, "prompt-file", "", "read the prompt from a file (\"-\" for stdin)")
	return cmd
}

// forEachTarget runs op against a single named workspace, or every workspace
// when all is set, reporting one verb line per success. It is the shared body of
// start/stop/restart/update.
func forEachTarget(cfg config.Config, cmd *cobra.Command, args []string, all bool, timeout time.Duration, verb string, op func(workspace.Lifecycle, context.Context, workspace.Summary) error) error {
	if all == (len(args) == 1) {
		return fmt.Errorf("specify exactly one workspace, or --all")
	}
	lc, err := workspace.NewLifecycle(cfg)
	if err != nil {
		return err
	}

	var targets []workspace.Summary
	if all {
		targets, err = workspace.NewRegistry(cfg).List()
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No workspaces.")
			return nil
		}
	} else {
		s, err := findWorkspace(cfg, args[0])
		if err != nil {
			return err
		}
		targets = []workspace.Summary{s}
	}

	var failed int
	for _, s := range targets {
		ctx, cancel := cmdContext(timeout)
		err := op(lc, ctx, s)
		cancel()
		if err != nil {
			failed++
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", s.Manifest.Name, err)
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s.\n", verb, s.Manifest.Name)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d workspace(s) failed", failed, len(targets))
	}
	return nil
}

// resolvePrompt resolves the headless run prompt from the flag, a file, or stdin.
func resolvePrompt(prompt, promptFile string) (string, error) {
	switch {
	case prompt != "" && promptFile != "":
		return "", fmt.Errorf("use only one of --prompt or --prompt-file")
	case prompt != "":
		return prompt, nil
	case promptFile == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	case promptFile != "":
		data, err := os.ReadFile(promptFile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	default:
		return "", fmt.Errorf("a prompt is required (--prompt, --prompt-file, or stdin via --prompt-file -)")
	}
}
