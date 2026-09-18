package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// newBashAutocompleteCmd emits a sourceable Bash integration. The shell
// function is necessary because a child process cannot change its parent's cwd.
func newBashAutocompleteCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "bash-autocomplete",
		Short: "Generate Bash completion and the `ocm cd` shell integration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, `# Load with: source <(ocm bash-autocomplete)`)
			fmt.Fprintln(out, `if [[ -z "${__ocm_binary:-}" ]]; then`)
			fmt.Fprintln(out, `  __ocm_binary="$(type -P ocm)"`)
			fmt.Fprintln(out, `fi`)
			fmt.Fprintln(out, `ocm() {`)
			fmt.Fprintln(out, `  if [[ "$1" == "cd" ]]; then`)
			fmt.Fprintln(out, `    shift`)
			fmt.Fprintln(out, `    local target`)
			fmt.Fprintln(out, `    target="$(command "$__ocm_binary" cd "$@")" || return`)
			fmt.Fprintln(out, `    builtin cd "$target"`)
			fmt.Fprintln(out, `  else`)
			fmt.Fprintln(out, `    command "$__ocm_binary" "$@"`)
			fmt.Fprintln(out, `  fi`)
			fmt.Fprintln(out, `}`)
			return cmd.Root().GenBashCompletionV2(out, true)
		},
	}
}

func newCDCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:               "cd <workspace>",
		Short:             "Print a workspace's local project directory",
		Long:              "Print a workspace's local project directory. Load `ocm bash-autocomplete` to make `ocm cd <workspace>` change the current Bash directory.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkspaceNames(cfg),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := findWorkspace(cfg, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(s.Path, "home", "workspace"))
			return nil
		},
	}
}

func completeWorkspaceNames(cfg config.Config) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		workspaces, err := workspace.NewRegistry(cfg).List()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		names := make([]cobra.Completion, 0, len(workspaces))
		for _, s := range workspaces {
			if strings.HasPrefix(strings.ToLower(s.Manifest.Name), strings.ToLower(toComplete)) {
				names = append(names, cobra.Completion(s.Manifest.Name))
			}
		}
		sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
