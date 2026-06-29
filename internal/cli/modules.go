package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// catalogModuleJSON is the machine-readable shape of an available catalog module.
type catalogModuleJSON struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Version     int    `json:"version"`
	Multi       bool   `json:"multi"`
	Description string `json:"description"`
}

func newModulesCmd(cfg config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "modules",
		Aliases: []string{"mod", "module"},
		Short:   "Inspect the module catalog and a workspace's installed modules",
	}
	cmd.AddCommand(
		newModulesListCmd(cfg),
		newModulesRemoveCmd(cfg),
	)
	return cmd
}

func newModulesListCmd(cfg config.Config) *cobra.Command {
	var ws string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available modules, or installed modules with --workspace",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			if ws != "" {
				return listInstalledModules(cfg, cmd, ws, format)
			}
			return listCatalogModules(cfg, cmd, format)
		},
	}
	cmd.Flags().StringVarP(&ws, "workspace", "w", "", "list modules installed in this workspace instead of the catalog")
	return cmd
}

func listCatalogModules(cfg config.Config, cmd *cobra.Command, format string) error {
	lc, err := workspace.NewLifecycle(cfg)
	if err != nil {
		return err
	}
	catalog, err := lc.Catalog()
	if err != nil {
		return err
	}
	if format == outputJSON {
		out := make([]catalogModuleJSON, 0, len(catalog))
		for _, m := range catalog {
			out = append(out, catalogModuleJSON{
				Name:        m.Name,
				Category:    m.Category,
				Version:     m.Version,
				Multi:       m.Multi(),
				Description: m.Description,
			})
		}
		return printJSON(cmd.OutOrStdout(), out)
	}
	if len(catalog) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No modules found in the configured module directories.")
		return nil
	}
	tw := newTable(cmd.OutOrStdout())
	fmt.Fprintln(tw, "CATEGORY\tNAME\tVERSION\tMULTI")
	for _, m := range catalog {
		multi := ""
		if m.Multi() {
			multi = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", m.Category, m.Name, m.Version, multi)
	}
	return tw.Flush()
}

func listInstalledModules(cfg config.Config, cmd *cobra.Command, ws, format string) error {
	s, err := findWorkspace(cfg, ws)
	if err != nil {
		return err
	}
	mods := s.Manifest.Modules
	if format == outputJSON {
		return printJSON(cmd.OutOrStdout(), moduleDetail(mods))
	}
	if len(mods) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No modules installed in %q.\n", s.Manifest.Name)
		return nil
	}
	tw := newTable(cmd.OutOrStdout())
	fmt.Fprintln(tw, "ID\tNAME\tCATEGORY\tVERSION")
	for _, inst := range mods {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", inst.InstanceID(), inst.Name, inst.Category, inst.Version)
	}
	return tw.Flush()
}

func newModulesRemoveCmd(cfg config.Config) *cobra.Command {
	var ws string
	var force bool
	cmd := &cobra.Command{
		Use:     "remove <module-id>",
		Aliases: []string{"rm"},
		Short:   "Remove an installed module from a workspace",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ws == "" {
				return fmt.Errorf("--workspace/-w is required to identify which workspace to remove the module from")
			}
			s, err := findWorkspace(cfg, ws)
			if err != nil {
				return err
			}
			id := args[0]
			found := false
			for _, inst := range s.Manifest.Modules {
				if inst.InstanceID() == id {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("module %q is not installed in workspace %q", id, s.Manifest.Name)
			}
			if !force && !confirm(cmd, fmt.Sprintf("Remove module %q from workspace %q?", id, s.Manifest.Name)) {
				fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}
			lc, err := workspace.NewLifecycle(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := cmdContext(15 * time.Minute)
			defer cancel()
			if err := lc.RemoveModule(ctx, s, id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed module %q from %q.\n", id, s.Manifest.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&ws, "workspace", "w", "", "the workspace to remove the module from (required)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "do not prompt for confirmation")
	return cmd
}
