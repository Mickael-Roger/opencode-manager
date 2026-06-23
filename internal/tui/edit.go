package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mickael-menu/opencode-manager/internal/module"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// editEntry is one row in the module editor: a catalog module plus whether it is
// currently installed in the workspace and whether it is selected to be present
// after applying.
type editEntry struct {
	mod       module.Module
	installed bool
	selected  bool
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

	installed := map[string]bool{}
	for _, mod := range selected.Manifest.Modules {
		installed[mod.Name] = true
	}

	entries := make([]editEntry, 0, len(catalog))
	for _, mod := range catalog {
		entries = append(entries, editEntry{mod: mod, installed: installed[mod.Name], selected: installed[mod.Name]})
	}

	m.editMode = true
	m.editEntries = entries
	m.editPos = 0
	m.editValues = map[string]map[string]string{}
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
		m.editValues = nil
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
	if entry.selected {
		entry.selected = false
		m.editEntries[m.editPos] = entry
		delete(m.editValues, entry.mod.Name)
		return m, nil
	}

	// Turning a not-yet-installed module on: collect its prompt values first.
	if !entry.installed && len(entry.mod.Prompts) > 0 {
		m.editPrompting = true
		m.editPromptMod = entry.mod
		m.editPromptIdx = 0
		m.editPromptVals = map[string]string{}
		m.editPromptInput = entry.mod.Prompts[0].Default
		m.message = ""
		return m, nil
	}

	entry.selected = true
	m.editEntries[m.editPos] = entry
	if _, ok := m.editValues[entry.mod.Name]; !ok {
		m.editValues[entry.mod.Name] = map[string]string{}
	}
	return m, nil
}

func (m model) updateEditPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prompts := m.editPromptMod.Prompts
	if m.editPromptIdx >= len(prompts) {
		m.editPrompting = false
		return m, nil
	}
	cur := prompts[m.editPromptIdx]

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.editPrompting = false
		m.editPromptVals = nil
		m.editPromptInput = ""
		m.message = "Module selection cancelled."
		return m, nil
	case "enter":
		val := strings.TrimSpace(m.editPromptInput)
		if val == "" && cur.Default != "" {
			val = cur.Default
		}
		if cur.Required && val == "" {
			m.message = fmt.Sprintf("%s is required.", cur.Label)
			return m, nil
		}
		m.editPromptVals[cur.Name] = val
		m.editPromptIdx++
		if m.editPromptIdx >= len(prompts) {
			m.finishEditPrompt()
			return m, nil
		}
		m.editPromptInput = prompts[m.editPromptIdx].Default
		m.message = ""
		return m, nil
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

// finishEditPrompt records the collected values and marks the module selected.
func (m *model) finishEditPrompt() {
	name := m.editPromptMod.Name
	m.editValues[name] = m.editPromptVals
	for i := range m.editEntries {
		if m.editEntries[i].mod.Name == name {
			m.editEntries[i].selected = true
		}
	}
	m.editPrompting = false
	m.editPromptVals = nil
	m.editPromptInput = ""
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
		switch {
		case e.selected && !e.installed:
			adds = append(adds, addOp{mod: e.mod, vals: m.editValues[e.mod.Name]})
		case !e.selected && e.installed:
			removes = append(removes, e.mod.Name)
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

	text := box + " " + e.mod.Name + marker
	if e.mod.Description != "" {
		text += " — " + e.mod.Description
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
