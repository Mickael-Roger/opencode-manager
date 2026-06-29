// Package cli implements the resource-oriented `ocm` command tree built on
// cobra. The binary is TUI-first: running `ocm` with no subcommand launches the
// dashboard (see NewRootCommand.RunE), while subcommands provide a scriptable,
// kubectl-style surface — `ocm <resource> <verb>`, e.g. `ocm workspaces list`,
// `ocm templates get web`, `ocm modules list`.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/tui"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// version is the build version reported by `ocm version`. It is injected at
// build time via -ldflags "-X .../internal/cli.version=<v>", mirroring the
// TUI's own appVersion, and defaults to "dev" for local builds.
var version = "dev"

// Output formats accepted by the global --output/-o flag.
const (
	outputTable = "table"
	outputJSON  = "json"
)

// NewRootCommand builds the full `ocm` command tree for the given config. With
// no subcommand it launches the TUI dashboard; any recognized subcommand runs
// non-interactively.
func NewRootCommand(cfg config.Config) *cobra.Command {
	root := &cobra.Command{
		Use:   "ocm",
		Short: "k9s for OpenCode — isolated, per-project OpenCode sessions",
		Long: "opencode-manager (ocm) runs each project's OpenCode session in its own\n" +
			"isolated container with only the tools and credentials you pick for it.\n\n" +
			"Run `ocm` with no arguments to open the interactive dashboard, or use the\n" +
			"subcommands below to manage workspaces, templates, and modules from scripts.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// With no subcommand, launch the TUI. An unrecognized first token is a
		// usage error rather than being silently swallowed as a TUI launch.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q; run %q for usage", args[0], cmd.CommandPath(), cmd.CommandPath()+" --help")
			}
			return tui.Run(cfg)
		},
	}

	root.PersistentFlags().StringP("output", "o", outputTable, "output format: table|json")

	root.AddCommand(
		newWorkspacesCmd(cfg),
		newTemplatesCmd(cfg),
		newModulesCmd(cfg),
		newConfigCmd(cfg),
		newVersionCmd(),
		newDoctorCmd(cfg),
		newSelftestCmd(),
	)

	return root
}

// outputFormat reads and validates the inherited --output flag.
func outputFormat(cmd *cobra.Command) (string, error) {
	f, _ := cmd.Flags().GetString("output")
	switch f {
	case outputTable, outputJSON:
		return f, nil
	default:
		return "", fmt.Errorf("invalid --output %q: want %q or %q", f, outputTable, outputJSON)
	}
}

// printJSON writes v as indented JSON followed by a newline.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// newTable returns a tab-aligned writer for human-readable column output.
func newTable(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
}

// findWorkspace resolves a workspace by display name or slug, matching the
// dashboard's lookup so the CLI and TUI accept the same identifiers.
func findWorkspace(cfg config.Config, name string) (workspace.Summary, error) {
	workspaces, err := workspace.NewRegistry(cfg).List()
	if err != nil {
		return workspace.Summary{}, err
	}
	for _, s := range workspaces {
		if s.Manifest.Name == name || workspace.SafeName(s.Manifest.Name) == workspace.SafeName(name) {
			return s, nil
		}
	}
	return workspace.Summary{}, fmt.Errorf("workspace %q not found", name)
}

// cmdContext returns a context bounded by d for a single non-interactive
// operation. Interactive commands (attach/shell/exec/run) use it only to bound
// the start-up preflight, not the session itself.
func cmdContext(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// age renders a compact human duration since t (e.g. "3h", "2d"), for table
// columns.
func age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// compact renders a token count as a short magnitude string (k/M/B), matching
// the dashboard's I/O/C column style.
func compact(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
