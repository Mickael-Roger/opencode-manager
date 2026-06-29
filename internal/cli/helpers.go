package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// runInteractive wires a prepared command to the real terminal and runs it,
// used for attach/shell/exec/run. The command's own exit status propagates as
// the error.
func runInteractive(c *exec.Cmd) error {
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// confirm prints a yes/no prompt and reads a line from stdin, returning true
// only on an explicit "y"/"yes". It defaults to No, matching the dashboard's
// destructive-action default.
func confirm(cmd *cobra.Command, question string) bool {
	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", question)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// tokenUsageJSON is the machine-readable token breakdown for `workspaces get`.
type tokenUsageJSON struct {
	TotalTokens    int64   `json:"totalTokens"`
	TotalInput     int64   `json:"totalInput"`
	TotalOutput    int64   `json:"totalOutput"`
	TotalCacheRead int64   `json:"totalCacheRead"`
	TotalCost      float64 `json:"totalCost"`
	TotalMsgs      int     `json:"totalMessages"`
	TodayTokens    int64   `json:"todayTokens"`
	TodayCost      float64 `json:"todayCost"`
	TodayMsgs      int     `json:"todayMessages"`
}

// moduleJSON is the machine-readable shape of an installed module instance.
type moduleJSON struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Category string            `json:"category"`
	Version  int               `json:"version"`
	Values   map[string]string `json:"values,omitempty"`
}

// workspaceDetailJSON is the `workspaces get --output json` document: the list
// summary plus the OpenCode version, installed modules with values, and token
// usage (the last two only populated when the container is running).
type workspaceDetailJSON struct {
	workspaceJSON
	OpenCodeVersion string          `json:"opencodeVersion,omitempty"`
	HomeDir         string          `json:"homeDir"`
	ModuleDetail    []moduleJSON    `json:"moduleDetail"`
	TokenUsage      *tokenUsageJSON `json:"tokenUsage,omitempty"`
}

func moduleDetail(insts []workspace.ModuleInstance) []moduleJSON {
	out := make([]moduleJSON, 0, len(insts))
	for _, inst := range insts {
		out = append(out, moduleJSON{
			ID:       inst.InstanceID(),
			Name:     inst.Name,
			Category: inst.Category,
			Version:  inst.Version,
			Values:   inst.ValuesMap(),
		})
	}
	return out
}

func newWorkspaceDetailJSON(st workspace.Status, ocVersion string, usage *workspace.TokenUsage) workspaceDetailJSON {
	d := workspaceDetailJSON{
		workspaceJSON:   statusToJSON(st),
		OpenCodeVersion: ocVersion,
		HomeDir:         st.Workspace.Manifest.HomeDir,
		ModuleDetail:    moduleDetail(st.Workspace.Manifest.Modules),
	}
	if usage != nil {
		d.TokenUsage = &tokenUsageJSON{
			TotalTokens:    usage.TotalTokens,
			TotalInput:     usage.TotalInput,
			TotalOutput:    usage.TotalOutput,
			TotalCacheRead: usage.TotalCacheRead,
			TotalCost:      usage.TotalCost,
			TotalMsgs:      usage.TotalMsgs,
			TodayTokens:    usage.TodayTokens,
			TodayCost:      usage.TodayCost,
			TodayMsgs:      usage.TodayMsgs,
		}
	}
	return d
}

// printWorkspaceDetail renders the human-readable `workspaces get` view.
func printWorkspaceDetail(w io.Writer, st workspace.Status, ocVersion string, usage *workspace.TokenUsage) error {
	m := st.Workspace.Manifest
	tw := newTable(w)
	row := func(k, v string) { fmt.Fprintf(tw, "%s\t%s\n", k, v) }
	row("Name:", m.Name)
	row("Slug:", workspace.SafeName(m.Name))
	row("Status:", st.Container)
	row("Activity:", activityLabel(st.Activity, st.Pending))
	if ocVersion != "" {
		row("OpenCode:", ocVersion)
	}
	row("Runtime:", m.Runtime)
	row("Image:", m.ImageName)
	row("Container:", m.ContainerName)
	row("Port:", fmt.Sprintf("%d", m.OpenCodePort))
	row("Home:", m.HomeDir)
	row("Created:", age(m.CreatedAt)+" ago")
	if st.Error != "" {
		row("Error:", st.Error)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w, "\nModules:")
	if len(m.Modules) == 0 {
		fmt.Fprintln(w, "  (none)")
	} else {
		mt := newTable(w)
		fmt.Fprintln(mt, "  ID\tCATEGORY\tVERSION")
		for _, inst := range m.Modules {
			fmt.Fprintf(mt, "  %s\t%s\t%d\n", inst.InstanceID(), inst.Category, inst.Version)
		}
		if err := mt.Flush(); err != nil {
			return err
		}
	}

	if usage != nil {
		fmt.Fprintln(w, "\nTokens (all-time I/O/C):")
		fmt.Fprintf(w, "  input %s / output %s / cache-read %s\n", compact(usage.TotalInput), compact(usage.TotalOutput), compact(usage.TotalCacheRead))
		fmt.Fprintf(w, "  total %s across %d messages ($%.2f)\n", compact(usage.TotalTokens), usage.TotalMsgs, usage.TotalCost)
		fmt.Fprintf(w, "  today %s across %d messages ($%.2f)\n", compact(usage.TodayTokens), usage.TodayMsgs, usage.TodayCost)
	}
	return nil
}
