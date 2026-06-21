package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

const commandPrompt = "> "

type model struct {
	cfg           config.Config
	registry      workspace.Registry
	lifecycle     workspace.Lifecycle
	lifecycleErr  string
	workspaces    []workspace.Summary
	statuses      map[string]workspace.Status
	workspacePos  int
	width         int
	height        int
	runtimeName   string
	runtimeError  string
	loadError     string
	message       string
	command       string
	commandMode   bool
	createMode    bool
	createName    string
	confirmDelete bool
}

type commandItem struct {
	Name        string
	Shortcut    string
	Description string
}

var commands = []commandItem{
	{Name: "/help", Shortcut: "h", Description: "show available commands"},
	{Name: "/create", Shortcut: "c", Description: "create a new workspace"},
	{Name: "/attach", Shortcut: "a", Description: "attach to selected workspace"},
	{Name: "/edit", Shortcut: "e", Description: "edit selected workspace"},
	{Name: "/delete", Shortcut: "d", Description: "delete selected workspace"},
	{Name: "/stop", Shortcut: "s", Description: "stop selected workspace"},
	{Name: "/update", Shortcut: "u", Description: "update OpenCode in selected workspace"},
	{Name: "/quit", Shortcut: "q", Description: "quit opencode-manager"},
}

var (
	appStyle = lipgloss.NewStyle().Padding(1, 2)
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2)
	commandStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)
	confirmStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(1, 4)
	confirmTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("252"))
	confirmButtonStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("203"))
	cancelButtonStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245"))
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("62")).Bold(true)
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

type workspaceListMsg struct {
	workspaces []workspace.Summary
	err        error
}

type runtimeStatusMsg struct {
	name string
	err  error
}

type statusListMsg struct {
	statuses []workspace.Status
}

type lifecycleActionMsg struct {
	action string
	name   string
	err    error
}

type provisionWorkspaceMsg struct {
	name string
	err  error
}

type baseImageReadyMsg struct {
	err error
}

type attachReadyMsg struct {
	name string
	cmd  tea.Cmd
	err  error
}

func Run(cfg config.Config) error {
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		return errors.New("opencode-manager must be run from an interactive terminal")
	}

	program := tea.NewProgram(
		newModel(cfg),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
		tea.WithAltScreen(),
	)
	_, err := program.Run()
	return err
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

func newModel(cfg config.Config) model {
	lifecycle, err := workspace.NewLifecycle(cfg)
	lifecycleErr := ""
	if err != nil {
		lifecycleErr = err.Error()
	}

	return model{
		cfg:          cfg,
		registry:     workspace.NewRegistry(cfg),
		lifecycle:    lifecycle,
		lifecycleErr: lifecycleErr,
		statuses:     map[string]workspace.Status{},
		width:        100,
		height:       30,
		message:      "Creating the base image...",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadWorkspaces, m.checkRuntime, m.ensureBaseImage)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		if m.confirmDelete {
			return m.updateDeleteConfirmation(msg)
		}

		if m.createMode {
			return m.updateCreate(msg)
		}

		if m.commandMode {
			return m.updateCommand(msg)
		}

		key := msg.String()
		switch key {
		case "ctrl+c":
			return m, tea.Quit
		case "/":
			m.commandMode = true
			m.command = "/"
		case "up", "k":
			m.moveWorkspace(-1)
		case "down", "j":
			m.moveWorkspace(1)
		case "enter":
			return m.attachSelected()
		default:
			return m.executeShortcut(key)
		}
	case workspaceListMsg:
		if msg.err != nil {
			m.loadError = msg.err.Error()
			m.workspaces = nil
			return m, nil
		}

		m.loadError = ""
		m.workspaces = msg.workspaces
		if m.workspacePos >= len(m.workspaces) {
			m.workspacePos = max(0, len(m.workspaces)-1)
		}
		return m, m.loadStatuses
	case runtimeStatusMsg:
		m.runtimeName = msg.name
		if msg.err != nil {
			m.runtimeError = msg.err.Error()
		} else {
			m.runtimeError = ""
		}
	case statusListMsg:
		m.statuses = make(map[string]workspace.Status, len(msg.statuses))
		for _, status := range msg.statuses {
			m.statuses[status.Workspace.Manifest.Name] = status
		}
	case lifecycleActionMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("%s failed for %s: %v", msg.action, msg.name, msg.err)
			return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
		}
		m.message = fmt.Sprintf("%s completed for %s.", msg.action, msg.name)
		return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
	case provisionWorkspaceMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Create runtime provisioning failed for %s: %v", msg.name, msg.err)
			return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
		}
		m.message = fmt.Sprintf("Created workspace %s and provisioned its image/container.", msg.name)
		return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
	case baseImageReadyMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Base image creation failed: %v", msg.err)
			return m, nil
		}
		m.message = "Base image ready. Type / for commands. Select a workspace with up/down."
		return m, nil
	case attachReadyMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Attach failed for %s: %v", msg.name, msg.err)
			return m, m.loadStatuses
		}
		m.message = fmt.Sprintf("Attached to %s.", msg.name)
		return m, msg.cmd
	case workspace.AttachResultMsg:
		if msg.Err != nil {
			m.message = fmt.Sprintf("Attach session failed: %v", msg.Err)
		} else {
			m.message = "Attach session closed."
		}
		return m, m.loadStatuses
	}

	return m, nil
}

func (m model) updateDeleteConfirmation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "enter":
		m.confirmDelete = false
		return m.deleteSelected()
	case "n", "esc", "q":
		m.confirmDelete = false
		m.message = "Delete cancelled."
	}

	return m, nil
}

func (m model) updateCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.commandMode = false
		m.command = ""
		m.message = "Command cancelled."
	case "enter":
		return m.executeCommand()
	case "tab":
		m.autocompleteCommand()
	case "backspace", "ctrl+h":
		if len(m.command) > 0 {
			m.command = m.command[:len(m.command)-1]
		}
		if m.command == "" {
			m.commandMode = false
		}
	default:
		if len(msg.Runes) > 0 {
			m.command += string(msg.Runes)
		}
	}

	return m, nil
}

func (m model) updateCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.createMode = false
		m.createName = ""
		m.message = "Create cancelled."
	case "enter":
		return m.createWorkspace(strings.TrimSpace(m.createName))
	case "backspace", "ctrl+h":
		if len(m.createName) > 0 {
			m.createName = m.createName[:len(m.createName)-1]
		}
	default:
		if len(msg.Runes) > 0 {
			m.createName += string(msg.Runes)
		}
	}

	return m, nil
}

func (m model) executeCommand() (tea.Model, tea.Cmd) {
	command := strings.TrimSpace(m.command)
	m.commandMode = false
	m.command = ""
	if strings.HasPrefix(command, "/create ") {
		return m.createWorkspace(strings.TrimSpace(strings.TrimPrefix(command, "/create ")))
	}

	return m.executeCommandName(command)
}

func (m model) createWorkspace(name string) (tea.Model, tea.Cmd) {
	if name == "" {
		m.message = "Workspace name is required."
		return m, nil
	}

	result, err := m.registry.Create(name)
	if err != nil {
		m.message = fmt.Sprintf("Create failed: %v", err)
		return m, nil
	}

	m.createMode = false
	m.createName = ""
	m.message = fmt.Sprintf("Created workspace %s. Building image and creating container...", result.Manifest.Name)
	created := workspace.Summary{Manifest: result.Manifest, Path: result.Path}
	return m, tea.Batch(m.loadWorkspaces, m.provisionWorkspace(created))
}

func (m model) provisionWorkspace(summary workspace.Summary) tea.Cmd {
	return func() tea.Msg {
		if m.lifecycleErr != "" {
			return provisionWorkspaceMsg{name: summary.Manifest.Name, err: errors.New(m.lifecycleErr)}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, err := m.lifecycle.Provision(ctx, summary)
		return provisionWorkspaceMsg{name: summary.Manifest.Name, err: err}
	}
}

func (m model) stopSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.message = "Stop requires a selected workspace."
		return m, nil
	}
	if m.lifecycleErr != "" {
		m.message = "Stop failed: " + m.lifecycleErr
		return m, nil
	}

	m.message = "Stopping " + selected.Manifest.Name + "..."
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return lifecycleActionMsg{action: "Stop", name: selected.Manifest.Name, err: m.lifecycle.Stop(ctx, selected)}
	}
}

func (m model) deleteSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.message = "Delete requires a selected workspace."
		return m, nil
	}
	if m.lifecycleErr != "" {
		m.message = "Delete failed: " + m.lifecycleErr
		return m, nil
	}

	m.message = "Deleting " + selected.Manifest.Name + "..."
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return lifecycleActionMsg{action: "Delete", name: selected.Manifest.Name, err: m.lifecycle.Delete(ctx, selected)}
	}
}

func (m model) attachSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.message = "Attach requires a selected workspace."
		return m, nil
	}
	if m.lifecycleErr != "" {
		m.message = "Attach failed: " + m.lifecycleErr
		return m, nil
	}

	m.message = "Preparing " + selected.Manifest.Name + "..."
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd, err := m.lifecycle.Attach(ctx, selected)
		return attachReadyMsg{name: selected.Manifest.Name, cmd: cmd, err: err}
	}
}

func (m model) executeCommandName(command string) (tea.Model, tea.Cmd) {
	switch command {
	case "", "/":
		m.message = "No command entered. Type /help to list commands."
	case "/help":
		m.message = m.helpMessage()
	case "/quit":
		return m, tea.Quit
	case "/delete":
		m.requestDelete()
	case "/stop":
		return m.stopSelected()
	case "/attach":
		return m.attachSelected()
	case "/create":
		m.createMode = true
		m.createName = ""
		m.message = "Enter a workspace name. Press Enter to create, Esc to cancel."
	case "/edit":
		m.message = m.workspaceActionMessage("Edit")
	case "/update":
		m.message = m.workspaceActionMessage("Update OpenCode")
	default:
		m.message = fmt.Sprintf("Unknown command %q. Type /help.", command)
	}

	return m, nil
}

func (m model) executeShortcut(key string) (tea.Model, tea.Cmd) {
	for _, command := range commands {
		if key == command.Shortcut {
			return m.executeCommandName(command.Name)
		}
	}

	return m, nil
}

func (m *model) autocompleteCommand() {
	matches := m.commandSuggestions()
	if len(matches) == 1 {
		m.command = matches[0].Name
	}
}

func (m model) View() string {
	width := max(60, m.width-4)
	mainHeight := max(10, m.height-9)

	main := m.renderWorkspaceBox(width, mainHeight)
	message := mutedStyle.Width(width).Render(m.message)
	command := m.renderCommandBar(width)

	view := lipgloss.JoinVertical(lipgloss.Left, main, message, command)
	if m.createMode {
		view = overlayCentered(view, m.renderCreatePrompt(width), width, m.height-2)
	}
	if m.confirmDelete {
		view = overlayCentered(view, m.renderDeleteConfirmation(width), width, m.height-2)
	}

	return appStyle.Render(view)
}

func (m model) renderWorkspaceBox(width int, height int) string {
	var b strings.Builder

	topLine := titleStyle.Render("Workspaces") + "    " + helpStyle.Render(shortcutSummary())
	b.WriteString(topLine)
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(fmt.Sprintf("runtime: %s | root: %s", m.runtimeStatus(), m.cfg.WorkspaceRoot)))
	b.WriteString("\n\n")

	if m.loadError != "" {
		b.WriteString(errorStyle.Render(m.loadError))
	} else if len(m.workspaces) == 0 {
		b.WriteString("No workspaces yet. Type /create to create one.")
	} else {
		for i, ws := range m.workspaces {
			line := fmt.Sprintf("%s  %s  container:%s", ws.Manifest.Name, m.renderWorkspaceStatus(ws), ws.Manifest.ContainerName)
			if i == m.workspacePos {
				line = selectedStyle.Render(" > " + line)
			} else {
				line = "   " + line
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return boxStyle.Width(width).Height(height).Render(b.String())
}

func (m model) renderCommandBar(width int) string {
	command := m.command
	if command == "" && !m.commandMode {
		command = "/"
	}

	line := commandPrompt + command
	if m.commandMode {
		line += "_"
	} else {
		line += "  " + helpStyle.Render("type / for commands, tab to autocomplete, or press a shortcut")
	}

	suggestions := m.renderSuggestions()
	if suggestions != "" {
		line += "\n" + suggestions
	}

	return commandStyle.Width(width).Render(line)
}

func (m model) renderSuggestions() string {
	if !m.commandMode {
		return ""
	}

	matches := m.commandSuggestions()
	if len(matches) == 0 {
		return helpStyle.Render("no matching command")
	}

	parts := make([]string, 0, len(matches))
	for _, match := range matches {
		parts = append(parts, fmt.Sprintf("%s [%s] %s", match.Name, match.Shortcut, mutedStyle.Render(match.Description)))
	}

	return strings.Join(parts, "\n")
}

func (m model) helpMessage() string {
	parts := make([]string, 0, len(commands))
	for _, command := range commands {
		parts = append(parts, fmt.Sprintf("%s=%s", command.Shortcut, command.Name))
	}

	return "Commands: " + strings.Join(parts, ", ") + "."
}

func shortcutSummary() string {
	parts := make([]string, 0, len(commands))
	for _, command := range commands {
		parts = append(parts, fmt.Sprintf("%s %s", command.Shortcut, strings.TrimPrefix(command.Name, "/")))
	}

	return strings.Join(parts, "  ")
}

func (m model) renderDeleteConfirmation(width int) string {
	name := m.selectedWorkspaceName()
	if name == "" {
		name = "selected workspace"
	}

	body := lipgloss.JoinVertical(
		lipgloss.Center,
		confirmTitleStyle.Render("Delete workspace?"),
		"",
		titleStyle.Render(name),
		"",
		mutedStyle.Render("This action cannot be undone."),
		"",
		confirmButtonStyle.Render("y delete")+mutedStyle.Render("  /  ")+cancelButtonStyle.Render("esc cancel"),
	)

	return confirmStyle.Width(min(width-12, 46)).Render(body)
}

func overlayCentered(base string, popup string, width int, height int) string {
	baseLines := strings.Split(base, "\n")
	popupLines := strings.Split(popup, "\n")
	canvasHeight := max(height, len(baseLines))
	lines := make([]string, canvasHeight)

	for i := range lines {
		if i < len(baseLines) {
			lines[i] = baseLines[i]
		} else {
			lines[i] = ""
		}
	}

	start := max(0, (canvasHeight-len(popupLines))/2)
	for i, line := range popupLines {
		row := start + i
		if row >= len(lines) {
			break
		}
		lines[row] = lipgloss.PlaceHorizontal(width, lipgloss.Center, line)
	}

	return strings.Join(lines, "\n")
}

func (m model) renderCreatePrompt(width int) string {
	name := m.createName
	if name == "" {
		name = mutedStyle.Render("workspace name")
	}

	body := lipgloss.JoinVertical(
		lipgloss.Center,
		confirmTitleStyle.Render("Create workspace"),
		"",
		commandStyle.Width(min(width-20, 36)).Render(name+"_"),
		"",
		confirmButtonStyle.Foreground(lipgloss.Color("114")).Render("enter create")+
			mutedStyle.Render("  /  ")+
			cancelButtonStyle.Render("esc cancel"),
	)

	return confirmStyle.Width(min(width-12, 46)).BorderForeground(lipgloss.Color("240")).Render(body)
}

func (m model) commandSuggestions() []commandItem {
	if m.command == "" {
		return nil
	}

	matches := make([]commandItem, 0, len(commands))
	for _, command := range commands {
		if strings.HasPrefix(command.Name, m.command) {
			matches = append(matches, command)
		}
	}

	return matches
}

func (m model) runtimeStatus() string {
	if m.runtimeError != "" {
		return errorStyle.Render(m.cfg.Runtime + " unavailable")
	}

	if m.runtimeName == "" {
		return mutedStyle.Render(m.cfg.Runtime + " checking")
	}

	return m.runtimeName + " available"
}

func (m model) renderWorkspaceStatus(ws workspace.Summary) string {
	status, ok := m.statuses[ws.Manifest.Name]
	if !ok || status.Container == "" {
		return mutedStyle.Render("checking")
	}
	if status.Error != "" {
		return errorStyle.Render("error")
	}

	switch status.Container {
	case runtime.StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Render("running")
	case runtime.StatusMissing:
		return mutedStyle.Render("missing")
	case runtime.StatusExited:
		return mutedStyle.Render("stopped")
	default:
		return mutedStyle.Render(status.Container)
	}
}

func (m *model) requestDelete() {
	if len(m.workspaces) == 0 {
		m.message = "No workspace selected."
		return
	}

	m.confirmDelete = true
}

func (m model) workspaceActionMessage(action string) string {
	name := m.selectedWorkspaceName()
	if name == "" {
		return fmt.Sprintf("%s requires a selected workspace.", action)
	}

	return fmt.Sprintf("%s selected for %s. This action is not wired yet.", action, name)
}

func (m model) selectedWorkspaceName() string {
	if len(m.workspaces) == 0 {
		return ""
	}

	return m.workspaces[m.workspacePos].Manifest.Name
}

func (m model) selectedWorkspace() (workspace.Summary, bool) {
	if len(m.workspaces) == 0 {
		return workspace.Summary{}, false
	}

	return m.workspaces[m.workspacePos], true
}

func (m *model) moveWorkspace(delta int) {
	if len(m.workspaces) == 0 {
		m.workspacePos = 0
		return
	}

	m.workspacePos = clamp(m.workspacePos+delta, 0, len(m.workspaces)-1)
}

func clamp(value, minValue, maxValue int) int {
	if maxValue < minValue {
		return minValue
	}

	if value < minValue {
		return minValue
	}

	if value > maxValue {
		return maxValue
	}

	return value
}

func (m model) loadWorkspaces() tea.Msg {
	workspaces, err := m.registry.List()
	return workspaceListMsg{workspaces: workspaces, err: err}
}

func (m model) loadStatuses() tea.Msg {
	if m.lifecycleErr != "" || len(m.workspaces) == 0 {
		return statusListMsg{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return statusListMsg{statuses: m.lifecycle.Statuses(ctx, m.workspaces)}
}

func (m model) checkRuntime() tea.Msg {
	driver, err := runtime.NewDriver(m.cfg.Runtime)
	if err != nil {
		return runtimeStatusMsg{name: m.cfg.Runtime, err: err}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return runtimeStatusMsg{name: driver.Name(), err: driver.Available(ctx)}
}

func (m model) ensureBaseImage() tea.Msg {
	if m.lifecycleErr != "" {
		return baseImageReadyMsg{err: errors.New(m.lifecycleErr)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	return baseImageReadyMsg{err: m.lifecycle.EnsureBaseImage(ctx)}
}
