package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// templateJSON is the machine-readable shape of a template for `--output json`.
type templateJSON struct {
	Name      string       `json:"name"`
	Modules   []moduleJSON `json:"modules"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
}

func templateToJSON(t workspace.Template) templateJSON {
	return templateJSON{
		Name:      t.Name,
		Modules:   moduleDetail(t.Modules),
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
}

func newTemplatesCmd(cfg config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "templates",
		Aliases: []string{"tmpl", "template"},
		Short:   "Inspect workspace templates",
	}
	cmd.AddCommand(
		newTemplatesListCmd(cfg),
		newTemplatesGetCmd(cfg),
		newTemplatesDeleteCmd(cfg),
	)
	return cmd
}

func newTemplatesListCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List templates",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			templates, err := workspace.NewTemplateRegistry(cfg).List()
			if err != nil {
				return err
			}
			if format == outputJSON {
				out := make([]templateJSON, 0, len(templates))
				for _, t := range templates {
					out = append(out, templateToJSON(t))
				}
				return printJSON(cmd.OutOrStdout(), out)
			}
			if len(templates) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No templates. Create one from the dashboard (:templates, then c).")
				return nil
			}
			tw := newTable(cmd.OutOrStdout())
			fmt.Fprintln(tw, "NAME\tMODULES\tUPDATED")
			for _, t := range templates {
				fmt.Fprintf(tw, "%s\t%d\t%s\n", t.Name, len(t.Modules), age(t.UpdatedAt)+" ago")
			}
			return tw.Flush()
		},
	}
}

func newTemplatesGetCmd(cfg config.Config) *cobra.Command {
	return &cobra.Command{
		Use:     "get <template>",
		Aliases: []string{"describe"},
		Short:   "Show a template's modules and values",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := outputFormat(cmd)
			if err != nil {
				return err
			}
			t, err := workspace.NewTemplateRegistry(cfg).Load(args[0])
			if err != nil {
				return err
			}
			if format == outputJSON {
				return printJSON(cmd.OutOrStdout(), templateToJSON(t))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Template: %s\n\nModules:\n", t.Name)
			if len(t.Modules) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "  (none)")
				return nil
			}
			tw := newTable(cmd.OutOrStdout())
			fmt.Fprintln(tw, "  ID\tCATEGORY\tVERSION")
			for _, inst := range t.Modules {
				fmt.Fprintf(tw, "  %s\t%s\t%d\n", inst.InstanceID(), inst.Category, inst.Version)
			}
			return tw.Flush()
		},
	}
}

func newTemplatesDeleteCmd(cfg config.Config) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "delete <template>",
		Aliases: []string{"rm"},
		Short:   "Delete a template",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr := workspace.NewTemplateRegistry(cfg)
			if !tr.Exists(args[0]) {
				return fmt.Errorf("template %q not found", args[0])
			}
			if !force && !confirm(cmd, fmt.Sprintf("Delete template %q?", args[0])) {
				fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}
			if err := tr.Delete(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted template %q.\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "do not prompt for confirmation")
	return cmd
}
