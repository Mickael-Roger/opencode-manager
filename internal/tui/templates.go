package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// showTemplates switches to the templates page (the ":templates" view) and loads
// the current templates from disk.
func (m model) showTemplates() (tea.Model, tea.Cmd) {
	m.templatesMode = true
	m.templateFilter = ""
	m.templateFilterMode = false
	m.templatePos = 0
	if err := m.reloadTemplates(); err != nil {
		m.message = "Failed to load templates: " + err.Error()
		return m, nil
	}
	m.message = "Templates — c create, e edit, ^d delete, :workspaces to go back."
	return m, nil
}

// showWorkspaces switches back to the default workspaces page.
func (m model) showWorkspaces() (tea.Model, tea.Cmd) {
	m.templatesMode = false
	m.templateFilter = ""
	m.templateFilterMode = false
	m.message = "Workspaces."
	return m, nil
}

// reloadTemplates refreshes the in-memory template list from disk, keeping the
// cursor within range.
func (m *model) reloadTemplates() error {
	templates, err := m.templateRegistry.List()
	if err != nil {
		return err
	}
	m.templates = templates
	m.clampTemplatePos()
	return nil
}

func (m model) visibleTemplates() []workspace.Template {
	if m.templateFilter == "" {
		return m.templates
	}
	query := strings.ToLower(m.templateFilter)
	out := make([]workspace.Template, 0, len(m.templates))
	for _, t := range m.templates {
		if strings.Contains(strings.ToLower(t.Name), query) {
			out = append(out, t)
		}
	}
	return out
}

func (m model) selectedTemplate() (workspace.Template, bool) {
	visible := m.visibleTemplates()
	if len(visible) == 0 || m.templatePos < 0 || m.templatePos >= len(visible) {
		return workspace.Template{}, false
	}
	return visible[m.templatePos], true
}

// selectTemplate moves the cursor onto the named template if it is visible, e.g.
// to keep it highlighted after a save.
func (m *model) selectTemplate(name string) {
	for i, t := range m.visibleTemplates() {
		if t.Name == name {
			m.templatePos = i
			return
		}
	}
}

func (m *model) moveTemplate(delta int) {
	visible := m.visibleTemplates()
	if len(visible) == 0 {
		m.templatePos = 0
		return
	}
	m.templatePos = clamp(m.templatePos+delta, 0, len(visible)-1)
}

func (m *model) clampTemplatePos() {
	visible := m.visibleTemplates()
	if len(visible) == 0 {
		m.templatePos = 0
		return
	}
	m.templatePos = clamp(m.templatePos, 0, len(visible)-1)
}

// updateTemplates handles key input on the templates list page.
func (m model) updateTemplates(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case ":":
		m.commandMode = true
		m.command = ""
	case "/":
		m.templateFilterMode = true
		m.message = "Filter templates — type to search, enter to keep, esc to clear."
	case "?":
		m.showHelp = true
	case "esc":
		if m.templateFilter != "" {
			m.templateFilter = ""
			m.clampTemplatePos()
			m.message = "Filter cleared."
		}
	case "up", "k":
		m.moveTemplate(-1)
	case "down", "j":
		m.moveTemplate(1)
	case "g", "home":
		m.templatePos = 0
	case "G", "end":
		m.templatePos = max(0, len(m.visibleTemplates())-1)
	case "ctrl+f", "pgdown":
		m.moveTemplate(10)
	case "ctrl+b", "pgup":
		m.moveTemplate(-10)
	case "c":
		return m.startTemplateCreate()
	case "e", "enter":
		return m.editSelectedTemplate()
	case "ctrl+d", "d":
		m.requestDeleteTemplate()
	}
	return m, nil
}

// updateTemplateFilter handles typing the templates-page filter (the / search).
func (m model) updateTemplateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.templateFilterMode = false
		m.templateFilter = ""
		m.clampTemplatePos()
	case "enter":
		m.templateFilterMode = false
		m.clampTemplatePos()
	case "backspace", "ctrl+h":
		if len(m.templateFilter) > 0 {
			m.templateFilter = m.templateFilter[:len(m.templateFilter)-1]
		}
		m.clampTemplatePos()
	default:
		if len(msg.Runes) > 0 {
			m.templateFilter += string(msg.Runes)
			m.clampTemplatePos()
		}
	}
	return m, nil
}

// editSelectedTemplate opens the module editor against the selected template.
func (m model) editSelectedTemplate() (tea.Model, tea.Cmd) {
	t, ok := m.selectedTemplate()
	if !ok {
		m.message = "No template selected."
		return m, nil
	}
	if m.lifecycleErr != "" {
		m.message = "Edit failed: " + m.lifecycleErr
		return m, nil
	}
	return m.openTemplateEditor(t, false)
}

func (m *model) requestDeleteTemplate() {
	if len(m.visibleTemplates()) == 0 {
		m.message = "No template selected."
		return
	}
	m.dialogFocus = 0
	m.confirmDelete = true
}

func (m model) deleteSelectedTemplate() (tea.Model, tea.Cmd) {
	t, ok := m.selectedTemplate()
	if !ok {
		m.message = "Delete requires a selected template."
		return m, nil
	}
	if err := m.templateRegistry.Delete(t.Name); err != nil {
		m.message = fmt.Sprintf("Delete failed: %v", err)
		return m, nil
	}
	if err := m.reloadTemplates(); err != nil {
		m.message = "Deleted, but reloading templates failed: " + err.Error()
		return m, nil
	}
	m.message = fmt.Sprintf("Deleted template %q.", t.Name)
	return m, nil
}

// startTemplateCreate opens the name dialog for a new template; on confirm it
// opens the module editor against an empty template.
func (m model) startTemplateCreate() (tea.Model, tea.Cmd) {
	m.templateCreateMode = true
	m.templateCreateName = ""
	m.dialogFocus = 0
	m.message = "Enter a template name. Press Enter to continue, Esc to cancel."
	return m, nil
}

func (m model) updateTemplateCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.templateCreateMode = false
		m.templateCreateName = ""
		m.message = "Create cancelled."
	case "tab", "shift+tab":
		m.dialogFocus = (m.dialogFocus + 1) % 2
	case "enter":
		if m.dialogFocus == 1 {
			m.templateCreateMode = false
			m.templateCreateName = ""
			m.message = "Create cancelled."
			return m, nil
		}
		if _, ok := m.validateTemplateName(); !ok {
			return m, nil
		}
		name := strings.TrimSpace(m.templateCreateName)
		m.templateCreateMode = false
		m.templateCreateName = ""
		return m.openTemplateEditor(workspace.Template{Name: name}, true)
	case "backspace", "ctrl+h":
		if len(m.templateCreateName) > 0 {
			m.templateCreateName = m.templateCreateName[:len(m.templateCreateName)-1]
		}
	default:
		if len(msg.Runes) > 0 {
			m.templateCreateName += string(msg.Runes)
		}
	}
	return m, nil
}

// validateTemplateName mirrors validateCreateName: a name is valid when it has at
// least one allowed character and is not already used by another template (by
// display name or by the slug its file would use).
func (m model) validateTemplateName() (string, bool) {
	name := strings.TrimSpace(m.templateCreateName)
	if name == "" {
		return "", false
	}

	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return "Use only letters, digits, '-' or '_'.", false
	}

	slug := workspace.SafeName(name)
	if slug == "" {
		return "Use only letters, digits, '-' or '_'.", false
	}
	for _, t := range m.templates {
		if strings.EqualFold(t.Name, name) || workspace.SafeName(t.Name) == slug {
			return "That name is already taken.", false
		}
	}

	return "", true
}

// startCreatePick is invoked after the New Workspace name dialog. When templates
// exist it opens the "Pick Template" step; otherwise it creates the workspace
// directly (no point showing a picker with only "None").
func (m model) startCreatePick(name string) (tea.Model, tea.Cmd) {
	templates, err := m.templateRegistry.List()
	if err != nil {
		m.message = "Failed to load templates: " + err.Error()
		return m.createWorkspace(name, nil)
	}
	if len(templates) == 0 {
		return m.createWorkspace(name, nil)
	}

	m.createMode = false
	m.createName = ""
	m.createPicking = true
	m.createPendingName = name
	m.createTemplates = templates
	m.createTemplatePos = 0
	m.message = "Pick a template for the new workspace, or choose None."
	return m, nil
}

func (m model) updateCreatePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	count := len(m.createTemplates) + 1 // +1 for the "None" row
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.createPicking = false
		m.createPendingName = ""
		m.createTemplates = nil
		m.message = "Create cancelled."
	case "up", "k":
		if m.createTemplatePos > 0 {
			m.createTemplatePos--
		}
	case "down", "j":
		if m.createTemplatePos < count-1 {
			m.createTemplatePos++
		}
	case "g", "home":
		m.createTemplatePos = 0
	case "G", "end":
		m.createTemplatePos = count - 1
	case "enter":
		name := m.createPendingName
		if m.createTemplatePos == 0 {
			return m.createWorkspace(name, nil)
		}
		tmpl := m.createTemplates[m.createTemplatePos-1]
		return m.createWorkspace(name, &tmpl)
	}
	return m, nil
}

func (m model) renderCreatePick() string {
	lines := []string{
		dialogText.Render("New workspace ") + dialogLabel.Render(m.createPendingName),
		dialogText.Render("Choose a template (optional):"),
		"",
	}

	none := "‹ None ›"
	if m.createTemplatePos == 0 {
		lines = append(lines, cursorStyle.Render(none))
	} else {
		lines = append(lines, bodyStyle.Render(none))
	}

	for i, t := range m.createTemplates {
		label := fmt.Sprintf("%s  (%d module%s)", t.Name, len(t.Modules), plural(len(t.Modules), "", "s"))
		if i+1 == m.createTemplatePos {
			lines = append(lines, cursorStyle.Render(label))
		} else {
			lines = append(lines, bodyStyle.Render(label))
		}
	}

	lines = append(lines, "", mutedStyle.Render("↑/↓ move · Enter create · Esc cancel"))
	return k9sDialog("Pick Template", strings.Join(lines, "\n"), colBorder)
}

func (m model) renderTemplateCreatePrompt() string {
	display := dialogLabel.Render(m.templateCreateName) + "▏"

	field := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(34).
		Render(display)

	reason, ok := m.validateTemplateName()
	hint := " "
	if reason != "" {
		hint = errorStyle.Render(reason)
	}

	content := lipgloss.JoinVertical(
		lipgloss.Center,
		dialogText.Render("Enter a name for the new template."),
		"",
		field,
		hint,
		"",
		createDialogButtons(m.dialogFocus, ok),
	)

	return k9sDialog("New Template", content, colBorder)
}

func (m model) renderTemplatesPage(width, height int) string {
	inner := width - 2
	contentWidth := inner - 2
	visible := m.visibleTemplates()

	widths := templateColumnWidths(contentWidth)
	headers := []string{"NAME↑", "MODULES", "CREATED"}
	headerCells := make([]string, len(headers))
	for i, h := range headers {
		headerCells[i] = headerStyle.Render(fit(h, widths[i]))
	}
	headerRow := " " + strings.Join(headerCells, "  ") + " "

	bodyRows := make([]string, 0, height-1)
	bodyRows = append(bodyRows, headerRow)

	rowCapacity := height - 1
	if len(visible) == 0 {
		empty := "No templates match the filter."
		if m.templateFilter == "" {
			empty = "No templates yet. Press c to create one."
		}
		bodyRows = append(bodyRows, " "+mutedStyle.Render(fit(empty, contentWidth))+" ")
	} else {
		for i, t := range visible {
			if len(bodyRows) >= rowCapacity {
				break
			}
			bodyRows = append(bodyRows, m.renderTemplateRow(t, widths, i == m.templatePos))
		}
	}

	for len(bodyRows) < height-1 {
		bodyRows = append(bodyRows, " "+strings.Repeat(" ", contentWidth)+" ")
	}

	return m.boxWithTitle(m.templatesTitle(len(visible)), bodyRows, width)
}

func (m model) renderTemplateRow(t workspace.Template, widths []int, selected bool) string {
	created := "—"
	if !t.CreatedAt.IsZero() {
		created = t.CreatedAt.Local().Format("2006-01-02 15:04")
	}
	modules := fmt.Sprintf("%d", len(t.Modules))

	cells := []string{
		fit(t.Name, widths[0]),
		fit(modules, widths[1]),
		fit(created, widths[2]),
	}

	if selected {
		return cursorStyle.Render(" " + strings.Join(cells, "  ") + " ")
	}

	plain := []string{
		bodyStyle.Render(cells[0]),
		mutedStyle.Render(cells[1]),
		mutedStyle.Render(cells[2]),
	}
	return " " + strings.Join(plain, "  ") + " "
}

func (m model) templatesTitle(count int) string {
	scope := "all"
	if m.templateFilter != "" {
		scope = "/" + m.templateFilter
	}
	return titleStyle.Render("Templates") + counterStyle.Render(fmt.Sprintf("(%s)[%d]", scope, count))
}

// templateColumnWidths splits the content width across NAME / MODULES / CREATED,
// keeping the two trailing columns fixed and giving the rest to NAME.
func templateColumnWidths(contentWidth int) []int {
	const gaps = 4 // two 2-space separators
	avail := contentWidth - gaps
	if avail < 20 {
		avail = 20
	}

	wModules := 8
	wCreated := 16
	wName := max(8, avail-wModules-wCreated)

	return []int{wName, wModules, wCreated}
}
