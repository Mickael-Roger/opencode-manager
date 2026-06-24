package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mickael-menu/opencode-manager/internal/module"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// editEntry is one row in the module editor. A singleton module has a single
// toggle row. A multi-instance module has one row per installed/pending entry
// plus an "add" action row (isAdd) that starts the prompt flow for a new entry.
type editEntry struct {
	mod module.Module
	// id is the instance identity (module name for singletons, "name:keyvalue"
	// for multi-instance entries). Empty on add rows.
	id string
	// label is what the row shows: the instance key value, or the module name.
	label string
	// isAdd marks the action row that creates a new instance of a multi module.
	isAdd     bool
	installed bool
	selected  bool
	// values holds the prompt values collected for a pending add.
	values map[string]string
}

// editApplyMsg reports the result of applying module add/remove operations.
type editApplyMsg struct {
	name    string
	summary string
	err     error
}

// editSelected opens the module editor for the selected workspace.
func (m model) editSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.message = "Edit requires a selected workspace."
		return m, nil
	}
	if m.lifecycleErr != "" {
		m.message = "Edit failed: " + m.lifecycleErr
		return m, nil
	}

	catalog, err := m.lifecycle.Catalog()
	if err != nil {
		m.catalogErr = err.Error()
		m.message = "Failed to load modules: " + err.Error()
		return m, nil
	}
	if len(catalog) == 0 {
		m.message = "No modules available."
		return m, nil
	}

	installedByMod := map[string][]workspace.ModuleInstance{}
	for _, inst := range selected.Manifest.Modules {
		installedByMod[inst.Name] = append(installedByMod[inst.Name], inst)
	}

	entries := make([]editEntry, 0, len(catalog))
	for _, mod := range catalog {
		insts := installedByMod[mod.Name]
		if !mod.Multi() {
			installed := len(insts) > 0
			entries = append(entries, editEntry{mod: mod, id: mod.Name, label: mod.Name, installed: installed, selected: installed})
			continue
		}
		// Multi-instance: one toggle row per installed entry, then an add row.
		for _, inst := range insts {
			entries = append(entries, editEntry{mod: mod, id: inst.InstanceID(), label: inst.Value(mod.Key), installed: true, selected: true})
		}
		entries = append(entries, editEntry{mod: mod, isAdd: true, label: mod.Name})
	}

	m.editMode = true
	m.editEntries = entries
	m.editPos = 0
	m.catalogErr = ""
	m.message = "Edit modules — space toggles, a applies, esc cancels."
	return m, nil
}

func (m model) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.editMode = false
		m.editEntries = nil
		m.message = "Edit cancelled."
	case "up", "k":
		if m.editPos > 0 {
			m.editPos--
		}
	case "down", "j":
		if m.editPos < len(m.editEntries)-1 {
			m.editPos++
		}
	case "g", "home":
		m.editPos = 0
	case "G", "end":
		m.editPos = max(0, len(m.editEntries)-1)
	case " ", "enter":
		return m.toggleEditEntry()
	case "a":
		return m.applyEdit()
	}
	return m, nil
}

func (m model) toggleEditEntry() (tea.Model, tea.Cmd) {
	if m.editPos < 0 || m.editPos >= len(m.editEntries) {
		return m, nil
	}

	entry := m.editEntries[m.editPos]

	// The add row always starts a fresh prompt flow for a new instance.
	if entry.isAdd {
		return m.startEditPrompt(entry.mod, m.editPos)
	}

	if entry.selected {
		entry.selected = false
		m.editEntries[m.editPos] = entry
		return m, nil
	}

	// Turning a not-yet-installed entry on: collect prompt values first, unless
	// they were already collected (re-selecting a pending add).
	if !entry.installed && entry.values == nil && len(entry.mod.Prompts) > 0 {
		return m.startEditPrompt(entry.mod, m.editPos)
	}

	entry.selected = true
	m.editEntries[m.editPos] = entry
	return m, nil
}

// startEditPrompt opens the prompt flow for the module triggered from the given
// row. row is the editEntries index recorded so finishEditPrompt knows where to
// place the result (the singleton row itself, or the multi module's add row).
func (m model) startEditPrompt(mod module.Module, row int) (tea.Model, tea.Cmd) {
	m.editPrompting = true
	m.editPromptMod = mod
	m.editPromptRow = row
	m.editPromptIdx = 0
	m.editPromptVals = map[string]string{}
	m.message = ""
	m.prepareEditPrompt()
	return m, nil
}

// prepareEditPrompt initializes the input state for the prompt at the current
// index: a text field for string/secret/bool prompts, or a loaded option list
// (static or host-sourced) for select/multiselect prompts.
func (m *model) prepareEditPrompt() {
	prompts := m.editPromptMod.Prompts
	m.editPromptInput = ""
	m.editPromptOptions = nil
	m.editPromptChosen = nil
	m.editPromptCursor = 0
	if m.editPromptIdx >= len(prompts) {
		return
	}
	cur := prompts[m.editPromptIdx]
	if cur.Type == module.PromptSelect || cur.Type == module.PromptMultiSelect {
		opts, err := loadPromptOptions(m.editPromptMod, cur)
		if err != nil {
			m.message = fmt.Sprintf("Failed to load options for %s: %v", cur.Label, err)
		}
		m.editPromptOptions = opts
		m.editPromptChosen = make([]bool, len(opts))
		return
	}
	m.editPromptInput = cur.Default
}

// loadPromptOptions returns the choices for a select/multiselect prompt, from
// the static Options list or by running the prompt's host-side optionsCommand.
func loadPromptOptions(mod module.Module, p module.Prompt) ([]string, error) {
	if !p.DynamicOptions() {
		return append([]string(nil), p.Options...), nil
	}
	return runHostOptions(mod, p.OptionsCommand)
}

// runHostOptions executes a module's optionsCommand on the host and returns its
// stdout as one option per non-empty line.
func runHostOptions(mod module.Module, command string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(mod.Dir, command))
	cmd.Dir = mod.Dir
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var opts []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			opts = append(opts, line)
		}
	}
	return opts, nil
}

func (m model) updateEditPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prompts := m.editPromptMod.Prompts
	if m.editPromptIdx >= len(prompts) {
		m.editPrompting = false
		return m, nil
	}
	cur := prompts[m.editPromptIdx]

	if cur.Type == module.PromptSelect || cur.Type == module.PromptMultiSelect {
		return m.updateEditPromptSelect(msg, cur)
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.cancelEditPrompt()
	case "enter":
		val := strings.TrimSpace(m.editPromptInput)
		if val == "" && cur.Default != "" {
			val = cur.Default
		}
		if cur.Required && val == "" {
			m.message = fmt.Sprintf("%s is required.", cur.Label)
			return m, nil
		}
		return m.commitEditPromptValue(cur, val)
	case "backspace", "ctrl+h":
		if len(m.editPromptInput) > 0 {
			m.editPromptInput = m.editPromptInput[:len(m.editPromptInput)-1]
		}
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			m.editPromptInput += string(msg.Runes)
		}
		return m, nil
	}
}

// updateEditPromptSelect handles key input for select/multiselect prompts:
// move the cursor, toggle choices, and confirm.
func (m model) updateEditPromptSelect(msg tea.KeyMsg, cur module.Prompt) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.cancelEditPrompt()
	case "up", "k":
		if m.editPromptCursor > 0 {
			m.editPromptCursor--
		}
		return m, nil
	case "down", "j":
		if m.editPromptCursor < len(m.editPromptOptions)-1 {
			m.editPromptCursor++
		}
		return m, nil
	case " ", "x":
		if m.editPromptCursor < len(m.editPromptChosen) {
			if cur.Type == module.PromptSelect {
				// Single select: choosing one clears the rest.
				for i := range m.editPromptChosen {
					m.editPromptChosen[i] = false
				}
			}
			m.editPromptChosen[m.editPromptCursor] = !m.editPromptChosen[m.editPromptCursor]
		}
		return m, nil
	case "enter":
		var chosen []string
		for i, ok := range m.editPromptChosen {
			if ok {
				chosen = append(chosen, m.editPromptOptions[i])
			}
		}
		if cur.Required && len(chosen) == 0 {
			if len(m.editPromptOptions) == 0 {
				m.message = fmt.Sprintf("No options available for %s.", cur.Label)
			} else {
				m.message = fmt.Sprintf("Select at least one %s (space to toggle).", cur.Label)
			}
			return m, nil
		}
		return m.commitEditPromptValue(cur, strings.Join(chosen, ","))
	}
	return m, nil
}

// commitEditPromptValue stores the value for the current prompt and advances to
// the next one, finishing the flow when the last prompt is answered.
func (m model) commitEditPromptValue(cur module.Prompt, val string) (tea.Model, tea.Cmd) {
	m.editPromptVals[cur.Name] = val
	m.editPromptIdx++
	m.message = ""
	if m.editPromptIdx >= len(m.editPromptMod.Prompts) {
		m.finishEditPrompt()
		return m, nil
	}
	m.prepareEditPrompt()
	return m, nil
}

func (m model) cancelEditPrompt() (tea.Model, tea.Cmd) {
	m.editPrompting = false
	m.editPromptVals = nil
	m.editPromptInput = ""
	m.editPromptOptions = nil
	m.editPromptChosen = nil
	m.message = "Module selection cancelled."
	return m, nil
}

// finishEditPrompt records the collected values. For a singleton it marks the
// triggering row selected; for a multi-instance module it inserts a new pending
// entry above the add row (or re-selects a matching existing row).
func (m *model) finishEditPrompt() {
	mod := m.editPromptMod
	vals := m.editPromptVals

	if mod.Multi() {
		id := mod.InstanceID(vals)
		for i := range m.editEntries {
			if !m.editEntries[i].isAdd && m.editEntries[i].id == id {
				m.editEntries[i].selected = true
				m.editEntries[i].values = vals
				m.finishPromptReset(mod.Name)
				return
			}
		}
		newEntry := editEntry{mod: mod, id: id, label: vals[mod.Key], selected: true, values: vals}
		idx := m.editPromptRow
		if idx < 0 || idx > len(m.editEntries) {
			idx = len(m.editEntries)
		}
		m.editEntries = append(m.editEntries[:idx], append([]editEntry{newEntry}, m.editEntries[idx:]...)...)
		m.editPos = idx
		m.finishPromptReset(mod.Name)
		return
	}

	if i := m.editPromptRow; i >= 0 && i < len(m.editEntries) {
		m.editEntries[i].selected = true
		m.editEntries[i].values = vals
	}
	m.finishPromptReset(mod.Name)
}

func (m *model) finishPromptReset(name string) {
	m.editPrompting = false
	m.editPromptVals = nil
	m.editPromptInput = ""
	m.editPromptOptions = nil
	m.editPromptChosen = nil
	m.message = "Module " + name + " ready. Press a to apply."
}

func (m model) applyEdit() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.editMode = false
		m.message = "Edit requires a selected workspace."
		return m, nil
	}

	type addOp struct {
		mod  module.Module
		vals map[string]string
	}
	var adds []addOp
	var removes []string
	for _, e := range m.editEntries {
		if e.isAdd {
			continue
		}
		switch {
		case e.selected && !e.installed:
			adds = append(adds, addOp{mod: e.mod, vals: e.values})
		case !e.selected && e.installed:
			removes = append(removes, e.id)
		}
	}

	if len(adds) == 0 && len(removes) == 0 {
		m.editMode = false
		m.message = "No module changes."
		return m, nil
	}

	// Idle guard: applying restarts the OpenCode server when a module changes
	// the environment, which would interrupt an in-flight task.
	switch m.statuses[selected.Manifest.Name].Activity {
	case workspace.ActivityWorking, workspace.ActivityApproval:
		m.message = fmt.Sprintf("Cannot edit modules while a task is running in %s. Wait until it is idle.", selected.Manifest.Name)
		return m, nil
	}

	name := selected.Manifest.Name
	m.editMode = false
	m.message = fmt.Sprintf("Applying module changes to %s (+%d/-%d)...", name, len(adds), len(removes))

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		for _, a := range adds {
			if err := m.lifecycle.AddModule(ctx, selected, a.mod, a.vals); err != nil {
				return editApplyMsg{name: name, err: fmt.Errorf("install %s: %w", a.mod.Name, err)}
			}
		}
		for _, rn := range removes {
			if err := m.lifecycle.RemoveModule(ctx, selected, rn); err != nil {
				return editApplyMsg{name: name, err: fmt.Errorf("remove %s: %w", rn, err)}
			}
		}
		return editApplyMsg{name: name, summary: fmt.Sprintf("+%d/-%d", len(adds), len(removes))}
	}
}

func (m model) renderEditPage(width, height int) string {
	inner := width - 2
	contentWidth := inner - 2
	blank := " " + strings.Repeat(" ", contentWidth) + " "

	rows := make([]string, 0, height-1)
	rows = append(rows, blank)
	if len(m.editEntries) == 0 {
		rows = append(rows, " "+mutedStyle.Render(fit("No modules available.", contentWidth))+" ")
	}
	for i, e := range m.editEntries {
		if len(rows) >= height-2 {
			break
		}
		rows = append(rows, m.renderEditRow(e, i == m.editPos, contentWidth))
	}
	rows = append(rows, blank)
	if len(rows) < height-1 {
		rows = append(rows, " "+mutedStyle.Render(fit("space toggle · a apply · esc cancel", contentWidth))+" ")
	}
	for len(rows) < height-1 {
		rows = append(rows, blank)
	}

	title := titleStyle.Render("Edit modules")
	if selected, ok := m.selectedWorkspace(); ok {
		title += counterStyle.Render("(" + selected.Manifest.Name + ")")
	}
	return m.boxWithTitle(title, rows, width)
}

func (m model) renderEditRow(e editEntry, selectedRow bool, contentWidth int) string {
	var text string
	if e.isAdd {
		text = "[+] " + e.mod.Name + " — add entry…"
	} else {
		box := "[ ]"
		if e.selected {
			box = "[x]"
		}

		marker := ""
		switch {
		case e.selected && !e.installed:
			marker = " (will install)"
		case !e.selected && e.installed:
			marker = " (will remove)"
		case e.installed:
			marker = " (installed)"
		}

		name := e.label
		if e.mod.Multi() {
			name = e.mod.Name + " (" + e.label + ")"
		}
		text = box + " " + name + marker
		if !e.mod.Multi() && e.mod.Description != "" {
			text += " — " + e.mod.Description
		}
	}
	line := fit(text, contentWidth)

	if selectedRow {
		return cursorStyle.Render(" " + line + " ")
	}
	return " " + bodyStyle.Render(line) + " "
}

func (m model) renderEditPrompt() string {
	prompts := m.editPromptMod.Prompts
	if m.editPromptIdx >= len(prompts) {
		return ""
	}
	cur := prompts[m.editPromptIdx]

	if cur.Type == module.PromptSelect || cur.Type == module.PromptMultiSelect {
		return m.renderEditPromptSelect(cur)
	}

	display := m.editPromptInput
	if cur.Secret() {
		display = strings.Repeat("•", len([]rune(m.editPromptInput)))
	}
	shown := dialogLabel.Render(display) + "▏"
	if m.editPromptInput == "" {
		placeholder := "value"
		if cur.Default != "" {
			placeholder = cur.Default
		}
		shown = mutedStyle.Render(placeholder) + "▏"
	}

	field := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(40).
		Render(shown)

	lines := []string{
		dialogText.Render(fmt.Sprintf("%s  (%d/%d)", m.editPromptMod.Name, m.editPromptIdx+1, len(prompts))),
		"",
		dialogLabel.Render(cur.Label),
	}
	if len(cur.Options) > 0 {
		lines = append(lines, mutedStyle.Render("options: "+strings.Join(cur.Options, ", ")))
	}
	if cur.Required {
		lines = append(lines, mutedStyle.Render("(required)"))
	}
	lines = append(lines, "", field, "", mutedStyle.Render("Enter to continue, Esc to cancel."))

	return k9sDialog("Module value", strings.Join(lines, "\n"), colBorder)
}

// renderEditPromptSelect renders the option list for a select/multiselect
// prompt, with the cursor highlighted and chosen options checked.
func (m model) renderEditPromptSelect(cur module.Prompt) string {
	lines := []string{
		dialogText.Render(fmt.Sprintf("%s  (%d/%d)", m.editPromptMod.Name, m.editPromptIdx+1, len(m.editPromptMod.Prompts))),
		"",
		dialogLabel.Render(cur.Label),
	}
	if cur.Required {
		lines = append(lines, mutedStyle.Render("(required)"))
	}
	lines = append(lines, "")

	if len(m.editPromptOptions) == 0 {
		lines = append(lines, mutedStyle.Render("No options available."))
	}
	for i, opt := range m.editPromptOptions {
		box := "[ ]"
		if i < len(m.editPromptChosen) && m.editPromptChosen[i] {
			box = "[x]"
		}
		row := box + " " + opt
		if i == m.editPromptCursor {
			lines = append(lines, cursorStyle.Render(row))
		} else {
			lines = append(lines, bodyStyle.Render(row))
		}
	}

	hint := "↑/↓ move · space toggle · Enter confirm · Esc cancel"
	if cur.Type == module.PromptSelect {
		hint = "↑/↓ move · space select · Enter confirm · Esc cancel"
	}
	lines = append(lines, "", mutedStyle.Render(hint))

	return k9sDialog("Module value", strings.Join(lines, "\n"), colBorder)
}
