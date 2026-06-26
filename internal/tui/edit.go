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

	// The add row starts the add flow for a new instance: an import picker of
	// host accounts when the module exposes one, otherwise a single-page form.
	if entry.isAdd {
		return m.startEditAdd(entry.mod, m.editPos)
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
		m.addPendingInstance(mod, m.editPromptRow, vals)
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

// addPendingInstance inserts a new pending entry for a multi-instance module at
// row (or re-selects a matching existing entry), recording the collected values
// and moving the cursor to it.
func (m *model) addPendingInstance(mod module.Module, row int, vals map[string]string) {
	id := mod.InstanceID(vals)
	for i := range m.editEntries {
		if !m.editEntries[i].isAdd && m.editEntries[i].id == id {
			m.editEntries[i].selected = true
			m.editEntries[i].values = vals
			m.editPos = i
			return
		}
	}
	newEntry := editEntry{mod: mod, id: id, label: vals[mod.Key], selected: true, values: vals}
	if row < 0 || row > len(m.editEntries) {
		row = len(m.editEntries)
	}
	m.editEntries = append(m.editEntries[:row], append([]editEntry{newEntry}, m.editEntries[row:]...)...)
	m.editPos = row
}

// startEditAdd begins adding a new entry to a multi-instance module. When the
// module's key prompt declares an optionsCommand, it lists the host accounts not
// already present and opens the import picker; with no importable accounts it
// falls through to the single-page manual form.
func (m model) startEditAdd(mod module.Module, row int) (tea.Model, tea.Cmd) {
	if key := mod.PromptByName(mod.Key); key != nil && key.OptionsCommand != "" {
		opts, err := runHostOptions(mod, key.OptionsCommand)
		if err != nil {
			m.message = fmt.Sprintf("Could not list host %s accounts: %v", mod.Name, err)
		}
		opts = filterPresentInstances(m.editEntries, mod, opts)
		if len(opts) > 0 {
			m.editImporting = true
			m.editImportMod = mod
			m.editImportRow = row
			m.editImportOptions = opts
			m.editImportChosen = make([]bool, len(opts))
			m.editImportCursor = 0
			m.editImportManualIdx = len(opts)
			m.message = ""
			return m, nil
		}
	}
	return m.startEditForm(mod, row)
}

// filterPresentInstances drops the host account names already present (installed
// or pending) as instances of mod, so the import picker only offers new ones.
func filterPresentInstances(entries []editEntry, mod module.Module, names []string) []string {
	present := map[string]bool{}
	for _, e := range entries {
		if !e.isAdd && e.mod.Name == mod.Name {
			present[e.label] = true
		}
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !present[n] {
			out = append(out, n)
		}
	}
	return out
}

// updateEditImport handles the host-account import picker: toggle which accounts
// to import (each becomes its own instance), or pick the "add manually" row to
// open the form for an account not on the host.
func (m model) updateEditImport(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.editImporting = false
		m.message = "Add cancelled."
		return m, nil
	case "up", "k":
		if m.editImportCursor > 0 {
			m.editImportCursor--
		}
		return m, nil
	case "down", "j":
		if m.editImportCursor < m.editImportManualIdx {
			m.editImportCursor++
		}
		return m, nil
	case " ", "x":
		// Only the account rows are toggleable; the manual row is an action.
		if m.editImportCursor < len(m.editImportChosen) {
			m.editImportChosen[m.editImportCursor] = !m.editImportChosen[m.editImportCursor]
		}
		return m, nil
	case "enter":
		mod := m.editImportMod
		if m.editImportCursor == m.editImportManualIdx {
			m.editImporting = false
			return m.startEditForm(mod, m.editImportRow)
		}
		var names []string
		for i, ok := range m.editImportChosen {
			if ok {
				names = append(names, m.editImportOptions[i])
			}
		}
		if len(names) == 0 {
			m.message = "Select at least one to import (space), or choose Add manually."
			return m, nil
		}
		// Imported instances store only the key; the module's resolve hook pulls
		// the real credentials from the host at install time.
		row := m.editImportRow
		for _, name := range names {
			m.addPendingInstance(mod, row, map[string]string{mod.Key: name})
			row++
		}
		m.editImporting = false
		m.message = fmt.Sprintf("%d %s entr%s ready. Press a to apply.", len(names), mod.Name, plural(len(names), "y", "ies"))
		return m, nil
	}
	return m, nil
}

// startEditForm opens the single-page form that collects every prompt value for
// one new entry of a multi-instance module at once.
func (m model) startEditForm(mod module.Module, row int) (tea.Model, tea.Cmd) {
	m.editFormMode = true
	m.editFormMod = mod
	m.editFormRow = row
	m.editFormCursor = 0
	m.editFormVals = make([]string, len(mod.Prompts))
	for i, p := range mod.Prompts {
		m.editFormVals[i] = p.Default
	}
	m.message = ""
	return m, nil
}

// updateEditForm handles the single-page form: navigate between fields, edit the
// focused one, and save the collected values as a pending entry.
func (m model) updateEditForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prompts := m.editFormMod.Prompts
	if len(prompts) == 0 || m.editFormCursor >= len(prompts) {
		m.editFormMode = false
		return m, nil
	}
	cur := prompts[m.editFormCursor]
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.editFormMode = false
		m.message = "Add cancelled."
		return m, nil
	case "up":
		if m.editFormCursor > 0 {
			m.editFormCursor--
		}
		return m, nil
	case "down", "tab":
		if m.editFormCursor < len(prompts)-1 {
			m.editFormCursor++
		}
		return m, nil
	case "enter":
		return m.submitEditForm()
	case "backspace", "ctrl+h":
		if cur.Type != module.PromptBool {
			s := m.editFormVals[m.editFormCursor]
			if len(s) > 0 {
				m.editFormVals[m.editFormCursor] = s[:len(s)-1]
			}
		}
		return m, nil
	case " ":
		if cur.Type == module.PromptBool {
			if m.editFormVals[m.editFormCursor] == "true" {
				m.editFormVals[m.editFormCursor] = "false"
			} else {
				m.editFormVals[m.editFormCursor] = "true"
			}
		} else {
			m.editFormVals[m.editFormCursor] += " "
		}
		return m, nil
	default:
		if cur.Type != module.PromptBool && len(msg.Runes) > 0 {
			m.editFormVals[m.editFormCursor] += string(msg.Runes)
		}
		return m, nil
	}
}

// submitEditForm validates the form fields and, when complete, records the
// collected values as a pending entry.
func (m model) submitEditForm() (tea.Model, tea.Cmd) {
	mod := m.editFormMod
	prompts := mod.Prompts
	vals := map[string]string{}
	for i, p := range prompts {
		v := strings.TrimSpace(m.editFormVals[i])
		if v == "" && p.Default != "" {
			v = p.Default
		}
		if p.Required && v == "" {
			m.editFormCursor = i
			m.message = fmt.Sprintf("%s is required.", p.Label)
			return m, nil
		}
		switch {
		case p.Type == module.PromptBool:
			if v == "" {
				v = "false"
			}
			vals[p.Name] = v
		case v != "":
			vals[p.Name] = v
		}
	}
	m.editFormMode = false
	m.addPendingInstance(mod, m.editFormRow, vals)
	m.message = "Module " + mod.Name + " ready. Press a to apply."
	return m, nil
}

// plural returns the singular or plural suffix for n.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
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
	needsRestart := false
	for _, e := range m.editEntries {
		if e.isAdd {
			continue
		}
		switch {
		case e.selected && !e.installed:
			adds = append(adds, addOp{mod: e.mod, vals: e.values})
			needsRestart = needsRestart || e.mod.RestartServer
		case !e.selected && e.installed:
			removes = append(removes, e.id)
			needsRestart = needsRestart || e.mod.RestartServer
		}
	}

	if len(adds) == 0 && len(removes) == 0 {
		m.editMode = false
		m.message = "No module changes."
		return m, nil
	}

	// Idle guard: applying a restart-requiring module bounces the OpenCode server
	// to reload ~/.env, which would interrupt an in-flight task. Modules that only
	// write their own config files (RestartServer == false) never bounce the
	// server, so they can be installed or removed even while a task is running.
	if needsRestart {
		switch m.statuses[selected.Manifest.Name].Activity {
		case workspace.ActivityWorking, workspace.ActivityApproval:
			m.message = fmt.Sprintf("Cannot edit modules while a task is running in %s. Wait until it is idle.", selected.Manifest.Name)
			return m, nil
		}
	}

	name := selected.Manifest.Name
	m.editMode = false
	m.message = fmt.Sprintf("Applying module changes to %s (+%d/-%d)...", name, len(adds), len(removes))

	// Freeze interactive access to this workspace until the job finishes: the
	// install/uninstall scripts run inside the container and may bounce the
	// OpenCode server, so attaching mid-install would land in a half-configured
	// session. Cleared when editApplyMsg arrives.
	m.installing[name] = true

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

	// Multi-instance modules are introduced by a group header; their instances
	// and add row are indented beneath it. Singletons render as a single row.
	prevMod := ""
	for i, e := range m.editEntries {
		if len(rows) >= height-3 {
			break
		}
		if e.mod.Multi() && e.mod.Name != prevMod {
			if prevMod != "" {
				rows = append(rows, blank)
			}
			rows = append(rows, m.renderEditGroupHeader(e.mod, contentWidth))
		}
		prevMod = e.mod.Name
		rows = append(rows, m.renderEditRow(e, i == m.editPos, contentWidth))
	}

	rows = append(rows, blank)
	if len(rows) < height-1 {
		hint := "↑/↓ move · space toggle · a apply · esc cancel"
		rows = append(rows, " "+mutedStyle.Render(fit(hint, contentWidth))+" ")
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

// renderEditGroupHeader renders the section heading for a multi-instance module:
// an accented name followed by its dimmed description.
func (m model) renderEditGroupHeader(mod module.Module, contentWidth int) string {
	head := "▸ " + mod.Name
	full := head
	if mod.Description != "" {
		full += "  " + mod.Description
	}
	fitted := []rune(fit(full, contentWidth))

	n := len([]rune(head))
	if n > len(fitted) {
		n = len(fitted)
	}
	name := editGroupStyle.Render(string(fitted[:n]))
	desc := editDescStyle.Render(string(fitted[n:]))
	return " " + name + desc + " "
}

func (m model) renderEditRow(e editEntry, selectedRow bool, contentWidth int) string {
	// Add-entry action row for multi-instance modules.
	if e.isAdd {
		text := "   + add " + e.mod.Name + " entry…"
		line := fit(text, contentWidth)
		if selectedRow {
			return cursorStyle.Render(" " + line + " ")
		}
		return " " + editAddStyle.Render(line) + " "
	}

	box := "◯"
	if e.selected {
		box = "◉"
	}

	indent := ""
	if e.mod.Multi() {
		indent = "   "
	}

	left := indent + box + " " + e.label
	if !e.mod.Multi() && e.mod.Description != "" {
		left += "  " + e.mod.Description
	}

	badge, badgeColor := editStateBadge(e)
	badgeW := len([]rune(badge))
	gap := 0
	if badge != "" {
		gap = 1
	}

	leftW := contentWidth - badgeW - gap
	if leftW < 1 {
		leftW = contentWidth
		badge, badgeW, gap = "", 0, 0
	}
	leftFitted := fit(left, leftW)

	if selectedRow {
		row := leftFitted + strings.Repeat(" ", gap) + badge
		return cursorStyle.Render(" " + fit(row, contentWidth) + " ")
	}

	// Recolor the checkbox glyph and (for singletons) the trailing description
	// without disturbing the fixed column widths.
	colored := colorizeEditLeft(leftFitted, len([]rune(indent)), e)
	row := colored
	if badge != "" {
		row += strings.Repeat(" ", gap) + lipgloss.NewStyle().Foreground(badgeColor).Render(badge)
	}
	return " " + row + " "
}

// colorizeEditLeft repaints the checkbox glyph at index boxAt according to the
// entry state and renders the remaining text in the body color, preserving the
// already-computed column width.
func colorizeEditLeft(leftFitted string, boxAt int, e editEntry) string {
	r := []rune(leftFitted)
	if boxAt >= len(r) {
		return bodyStyle.Render(leftFitted)
	}

	boxColor := colMuted
	if e.selected {
		boxColor = colRunning
	}

	pre := string(r[:boxAt])
	boxChar := lipgloss.NewStyle().Foreground(boxColor).Render(string(r[boxAt]))
	rest := bodyStyle.Render(string(r[boxAt+1:]))
	return pre + boxChar + rest
}

// editStateBadge returns the right-aligned status badge and its color for a
// module row: the pending change (install/remove) or the current state.
func editStateBadge(e editEntry) (string, lipgloss.Color) {
	switch {
	case e.selected && !e.installed:
		return "+ install", colRunning
	case !e.selected && e.installed:
		return "- remove", colError
	case e.installed:
		return "✓ installed", colMuted
	default:
		return "", colMuted
	}
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

// renderEditImport renders the host-account import picker: a checklist of
// importable accounts plus an "add manually" action for accounts not on the
// host.
func (m model) renderEditImport() string {
	mod := m.editImportMod
	lines := []string{
		dialogText.Render(fmt.Sprintf("Add %s — import from host", mod.Name)),
		"",
		dialogLabel.Render("Existing accounts on this host"),
		"",
	}
	for i, opt := range m.editImportOptions {
		box := "[ ]"
		if i < len(m.editImportChosen) && m.editImportChosen[i] {
			box = "[x]"
		}
		row := box + " " + opt
		if i == m.editImportCursor {
			lines = append(lines, cursorStyle.Render(row))
		} else {
			lines = append(lines, bodyStyle.Render(row))
		}
	}

	manual := "＋ Add manually (account not on this host)…"
	if m.editImportCursor == m.editImportManualIdx {
		lines = append(lines, cursorStyle.Render(manual))
	} else {
		lines = append(lines, mutedStyle.Render(manual))
	}

	lines = append(lines, "", mutedStyle.Render("↑/↓ move · space toggle · Enter import/confirm · Esc cancel"))
	return k9sDialog("Add "+mod.Name, strings.Join(lines, "\n"), colBorder)
}

// renderEditForm renders the single-page form collecting every prompt value for
// a manually-added entry, with the focused field highlighted.
func (m model) renderEditForm() string {
	mod := m.editFormMod
	prompts := mod.Prompts
	lines := []string{
		dialogText.Render(fmt.Sprintf("Add %s — new entry", mod.Name)),
		"",
	}
	for i, p := range prompts {
		val := ""
		if i < len(m.editFormVals) {
			val = m.editFormVals[i]
		}
		display := val
		switch {
		case p.Secret():
			display = strings.Repeat("•", len([]rune(val)))
		case p.Type == module.PromptBool:
			if val == "true" {
				display = "true"
			} else {
				display = "false"
			}
		}

		focused := i == m.editFormCursor
		var field string
		if display == "" {
			placeholder := "value"
			if p.Default != "" {
				placeholder = p.Default
			}
			field = mutedStyle.Render(placeholder)
		} else {
			field = dialogLabel.Render(display)
		}
		if focused && p.Type != module.PromptBool {
			field += "▏"
		}

		label := p.Label
		if p.Required {
			label += " *"
		}
		if focused {
			lines = append(lines, cursorStyle.Render("› "+label)+"  "+field)
		} else {
			lines = append(lines, bodyStyle.Render("  "+label)+"  "+field)
		}
	}

	lines = append(lines, "", mutedStyle.Render("↑/↓ or Tab move · type to edit · Enter save · Esc cancel"))
	return k9sDialog("Add "+mod.Name, strings.Join(lines, "\n"), colBorder)
}
