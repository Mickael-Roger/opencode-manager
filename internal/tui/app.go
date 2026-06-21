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

const appVersion = "v0.1.0"

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
	filter        string
	filterMode    bool
	createMode    bool
	createName    string
	confirmDelete bool
	dialogFocus   int // 0 = OK/primary button, 1 = Cancel
	showHelp      bool
	showDescribe  bool
	tokens        map[string]tokenState
}

// tokenState caches the tokscale token-usage synthesis for one workspace.
type tokenState struct {
	loading bool
	loaded  bool
	err     string
	usage   workspace.TokenUsage
}

// action mirrors a k9s menu entry: a hotkey, the command word typed after ":",
// and a human description shown in the keyboard menu and help.
type action struct {
	Key  string
	Cmd  string
	Desc string
}

var actions = []action{
	{Key: "", Cmd: "attach", Desc: "Attach"},
	{Key: "s", Cmd: "shell", Desc: "Shell"},
	{Key: "t", Cmd: "toggle", Desc: "Start/Stop"},
	{Key: "d", Cmd: "describe", Desc: "Describe"},
	{Key: "e", Cmd: "edit", Desc: "Edit"},
	{Key: "u", Cmd: "update", Desc: "Update"},
	{Key: "c", Cmd: "create", Desc: "Create"},
	{Key: "ctrl-d", Cmd: "delete", Desc: "Delete"},
}

// k9s default ("stock") skin colors.
var (
	colBorder      = lipgloss.Color("#1e90ff") // dodgerblue
	colBorderFocus = lipgloss.Color("#87cefa") // lightskyblue
	colTitle       = lipgloss.Color("#00ffff") // aqua
	colCounter     = lipgloss.Color("#ffefd5") // papayawhip
	colFilter      = lipgloss.Color("#2e8b57") // seagreen
	colMenuKey     = lipgloss.Color("#1e90ff") // dodgerblue
	colMenuText    = lipgloss.Color("#ffffff") // white
	colInfoKey     = lipgloss.Color("#ffa500") // orange
	colInfoVal     = lipgloss.Color("#ffffff") // white
	colLogo        = lipgloss.Color("#ffa500") // orange
	colBody        = lipgloss.Color("#5f9ea0") // cadetblue
	colHeader      = lipgloss.Color("#ffffff") // white
	colCursorFg    = lipgloss.Color("#000000") // black
	colCursorBg    = lipgloss.Color("#00ffff") // aqua
	colError       = lipgloss.Color("#ff5f5f") // red
	colRunning     = lipgloss.Color("#5fff87") // green
	colStopped     = lipgloss.Color("#ffaf00") // orange
	colStarting    = lipgloss.Color("#ffd75f") // yellow
	colMuted       = lipgloss.Color("#5f9ea0") // cadetblue/gray
)

var (
	infoKeyStyle  = lipgloss.NewStyle().Foreground(colInfoKey)
	infoValStyle  = lipgloss.NewStyle().Foreground(colInfoVal)
	menuKeyStyle  = lipgloss.NewStyle().Foreground(colMenuKey)
	menuTextStyle = lipgloss.NewStyle().Foreground(colMenuText)
	logoStyle     = lipgloss.NewStyle().Foreground(colLogo).Bold(true)
	titleStyle    = lipgloss.NewStyle().Foreground(colTitle)
	counterStyle  = lipgloss.NewStyle().Foreground(colCounter)
	headerStyle   = lipgloss.NewStyle().Foreground(colHeader).Bold(true)
	bodyStyle     = lipgloss.NewStyle().Foreground(colBody)
	mutedStyle    = lipgloss.NewStyle().Foreground(colMuted)
	errorStyle    = lipgloss.NewStyle().Foreground(colError)
	cursorStyle   = lipgloss.NewStyle().Foreground(colCursorFg).Background(colCursorBg).Bold(true)
	crumbStyle    = lipgloss.NewStyle().Foreground(colCursorFg).Background(colCursorBg).Bold(true).Padding(0, 1)
	filterStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(colFilter).Bold(true).Padding(0, 1)
	promptStyle   = lipgloss.NewStyle().Foreground(colTitle)
	borderStyle   = lipgloss.NewStyle().Foreground(colBorder)

	// k9s dialog ("Dialog") skin.
	dialogText      = lipgloss.NewStyle().Foreground(colBody)                                                                       // cadetblue
	dialogLabel     = lipgloss.NewStyle().Foreground(colMenuText).Bold(true)                                                        // white
	dialogButton    = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#483d8b")).Padding(0, 2) // darkslateblue
	dialogButtonHot = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(colBorder).Bold(true).Padding(0, 2)      // dodgerblue focus
)

var logoLines = []string{
	"╭──────────╮",
	"│ opencode │",
	"│  manager │",
	"╰──────────╯",
}

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

type updateActionMsg struct {
	name    string
	version string
	err     error
}

type baseImageReadyMsg struct {
	err error
}

type attachReadyMsg struct {
	noun string // "Attach" or "Shell"
	name string
	cmd  tea.Cmd
	err  error
}

type tokenUsageMsg struct {
	name  string
	usage workspace.TokenUsage
	err   error
}

// tickMsg drives periodic refresh of container/activity statuses so the
// dashboard reflects opencode activity without any user interaction.
type tickMsg time.Time

const refreshInterval = 2 * time.Second

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// bellCmd rings the terminal bell. It writes to stderr so it does not corrupt
// the alt-screen buffer rendered on stdout.
func bellCmd() tea.Cmd {
	return func() tea.Msg {
		fmt.Fprint(os.Stderr, "\a")
		return nil
	}
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
		tokens:       map[string]tokenState{},
		width:        100,
		height:       30,
		message:      "Creating the base image...",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadWorkspaces, m.checkRuntime, m.ensureBaseImage, tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		return m.updateKey(msg)
	case workspaceListMsg:
		if msg.err != nil {
			m.loadError = msg.err.Error()
			m.workspaces = nil
			return m, nil
		}

		m.loadError = ""
		m.workspaces = msg.workspaces
		m.clampSelection()
		return m, m.loadStatuses
	case runtimeStatusMsg:
		m.runtimeName = msg.name
		if msg.err != nil {
			m.runtimeError = msg.err.Error()
		} else {
			m.runtimeError = ""
		}
	case statusListMsg:
		next := make(map[string]workspace.Status, len(msg.statuses))
		ring := false
		for _, status := range msg.statuses {
			name := status.Workspace.Manifest.Name
			// Ring the bell when a workspace newly starts needing approval.
			if status.Activity == workspace.ActivityApproval {
				if prev, ok := m.statuses[name]; !ok || prev.Activity != workspace.ActivityApproval {
					ring = true
				}
			}
			next[name] = status
		}
		m.statuses = next
		if ring {
			return m, bellCmd()
		}
	case tickMsg:
		return m, tea.Batch(m.loadStatuses, tickCmd())
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
	case updateActionMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Update failed for %s: %v", msg.name, msg.err)
			return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
		}
		m.message = fmt.Sprintf("OpenCode updated to %s in %s.", msg.version, msg.name)
		return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
	case baseImageReadyMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Base image creation failed: %v", msg.err)
			return m, nil
		}
		m.message = "Base image ready. Press : for commands, / to filter, ? for help."
		return m, nil
	case attachReadyMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("%s failed for %s: %v", msg.noun, msg.name, msg.err)
			return m, m.loadStatuses
		}
		m.message = fmt.Sprintf("%s session started for %s.", msg.noun, msg.name)
		return m, msg.cmd
	case workspace.AttachResultMsg:
		switch {
		case msg.StillRunning:
			// Detached via Ctrl-C; the attach client exits non-zero but the
			// container keeps running, so this is not a failure.
			m.message = "Detached (Ctrl-C). Container still running in the background."
		case msg.Err != nil:
			m.message = fmt.Sprintf("Attach session failed: %v", msg.Err)
		default:
			m.message = "Attach session closed; container stopped."
		}
		return m, m.loadStatuses
	case workspace.ShellResultMsg:
		if msg.Err != nil {
			m.message = fmt.Sprintf("Shell session failed: %v", msg.Err)
		} else {
			m.message = "Shell session closed."
		}
		return m, m.loadStatuses
	case tokenUsageMsg:
		state := tokenState{loaded: true}
		if msg.err != nil {
			state.err = msg.err.Error()
		} else {
			state.usage = msg.usage
		}
		m.tokens[msg.name] = state
		return m, nil
	}

	return m, nil
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmDelete {
		return m.updateDeleteConfirmation(msg)
	}
	if m.createMode {
		return m.updateCreate(msg)
	}
	if m.commandMode {
		return m.updateCommand(msg)
	}
	if m.filterMode {
		return m.updateFilter(msg)
	}
	if m.showDescribe {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc", "q":
			m.showDescribe = false
			m.message = ""
		}
		return m, nil
	}
	if m.showHelp {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		default:
			m.showHelp = false
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case ":":
		m.commandMode = true
		m.command = ""
	case "/":
		m.filterMode = true
	case "?":
		m.showHelp = true
	case "ctrl+d":
		m.requestDelete()
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.clampSelection()
			m.message = "Filter cleared."
		}
	case "up", "k":
		m.moveWorkspace(-1)
	case "down", "j":
		m.moveWorkspace(1)
	case "g", "home":
		m.workspacePos = 0
	case "G", "end":
		m.workspacePos = max(0, len(m.visibleWorkspaces())-1)
	case "ctrl+f", "pgdown":
		m.moveWorkspace(10)
	case "ctrl+b", "pgup":
		m.moveWorkspace(-10)
	case "enter":
		return m.attachSelected()
	default:
		return m.executeShortcut(msg.String())
	}

	return m, nil
}

func (m model) updateDeleteConfirmation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab", "shift+tab", "left", "right", "h", "l":
		m.dialogFocus = (m.dialogFocus + 1) % 2
	case "enter":
		m.confirmDelete = false
		if m.dialogFocus == 0 {
			return m.deleteSelected()
		}
		m.message = "Delete cancelled."
	case "esc":
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

func (m model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.filterMode = false
		m.filter = ""
		m.clampSelection()
	case "enter":
		m.filterMode = false
		m.clampSelection()
	case "backspace", "ctrl+h":
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
		}
		m.clampSelection()
	default:
		if len(msg.Runes) > 0 {
			m.filter += string(msg.Runes)
			m.clampSelection()
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
	case "tab", "shift+tab":
		m.dialogFocus = (m.dialogFocus + 1) % 2
	case "enter":
		if m.dialogFocus == 1 {
			m.createMode = false
			m.createName = ""
			m.message = "Create cancelled."
			return m, nil
		}
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
	if strings.HasPrefix(command, "create ") {
		return m.createWorkspace(strings.TrimSpace(strings.TrimPrefix(command, "create ")))
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
		return attachReadyMsg{noun: "Attach", name: selected.Manifest.Name, cmd: cmd, err: err}
	}
}

func (m model) shellSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.message = "Shell requires a selected workspace."
		return m, nil
	}
	if m.lifecycleErr != "" {
		m.message = "Shell failed: " + m.lifecycleErr
		return m, nil
	}

	m.message = "Opening shell in " + selected.Manifest.Name + "..."
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd, err := m.lifecycle.Shell(ctx, selected)
		return attachReadyMsg{noun: "Shell", name: selected.Manifest.Name, cmd: cmd, err: err}
	}
}

// toggleStartStop starts a stopped workspace or stops a running one, based on
// the current container status.
func (m model) toggleStartStop() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.message = "Start/Stop requires a selected workspace."
		return m, nil
	}

	if status, ok := m.statuses[selected.Manifest.Name]; ok && status.Container == runtime.StatusRunning {
		return m.stopSelected()
	}

	return m.startSelected()
}

func (m model) startSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.message = "Start requires a selected workspace."
		return m, nil
	}
	if m.lifecycleErr != "" {
		m.message = "Start failed: " + m.lifecycleErr
		return m, nil
	}

	m.message = "Starting " + selected.Manifest.Name + "..."
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		return lifecycleActionMsg{action: "Start", name: selected.Manifest.Name, err: m.lifecycle.EnsureStarted(ctx, selected)}
	}
}

// updateSelected upgrades OpenCode inside the selected workspace container. It
// is gated on OpenCode being idle: while a task is running (the agent is
// generating) or blocked on an approval prompt, the update is refused so the
// post-update container restart cannot interrupt active work.
func (m model) updateSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.message = "Update requires a selected workspace."
		return m, nil
	}
	if m.lifecycleErr != "" {
		m.message = "Update failed: " + m.lifecycleErr
		return m, nil
	}

	name := selected.Manifest.Name
	switch m.statuses[name].Activity {
	case workspace.ActivityWorking, workspace.ActivityApproval:
		m.message = fmt.Sprintf("Cannot update %s while a task is running in opencode. Wait until it is idle.", name)
		return m, nil
	}

	m.message = "Updating OpenCode in " + name + " (this restarts the container)..."
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		version, err := m.lifecycle.UpdateOpenCode(ctx, selected)
		return updateActionMsg{name: name, version: version, err: err}
	}
}

func (m model) executeCommandName(command string) (tea.Model, tea.Cmd) {
	switch command {
	case "":
		m.message = "No command entered. Press ? for help."
	case "q", "quit":
		return m, tea.Quit
	case "help", "?":
		m.showHelp = true
	case "delete":
		m.requestDelete()
	case "describe":
		return m.describeSelected()
	case "shell":
		return m.shellSelected()
	case "toggle":
		return m.toggleStartStop()
	case "start":
		return m.startSelected()
	case "stop":
		return m.stopSelected()
	case "attach":
		return m.attachSelected()
	case "create":
		m.createMode = true
		m.createName = ""
		m.dialogFocus = 0
		m.message = "Enter a workspace name. Press Enter to create, Esc to cancel."
	case "edit":
		m.message = m.workspaceActionMessage("Edit")
	case "update":
		return m.updateSelected()
	default:
		m.message = fmt.Sprintf("Unknown command %q. Press ? for help.", command)
	}

	return m, nil
}

func (m model) executeShortcut(key string) (tea.Model, tea.Cmd) {
	for _, a := range actions {
		if a.Key != "" && key == a.Key {
			return m.executeCommandName(a.Cmd)
		}
	}

	return m, nil
}

func (m *model) autocompleteCommand() {
	matches := m.commandSuggestions()
	if len(matches) == 1 {
		m.command = matches[0].Cmd
	}
}

func (m model) View() string {
	width := max(40, m.width)
	height := max(12, m.height)

	header := m.renderHeader(width)
	headerHeight := lipgloss.Height(header)

	crumbs := m.renderCrumbs()
	prompt := m.renderPrompt(width)

	bodyHeight := height - headerHeight - lipgloss.Height(crumbs) - lipgloss.Height(prompt) - 1
	bodyHeight = max(3, bodyHeight)

	var body string
	if m.showDescribe {
		body = m.renderDescribePage(width, bodyHeight)
	} else {
		body = m.renderTable(width, bodyHeight)
	}

	view := lipgloss.JoinVertical(lipgloss.Left, header, body, crumbs, prompt)

	if m.createMode {
		view = overlayCentered(view, m.renderCreatePrompt(), width, height)
	}
	if m.confirmDelete {
		view = overlayCentered(view, m.renderDeleteConfirmation(), width, height)
	}
	if m.showHelp {
		view = overlayCentered(view, m.renderHelp(), width, height)
	}

	return view
}

func (m model) renderHeader(width int) string {
	info := m.renderInfo()
	menu := m.renderMenu()
	left := lipgloss.JoinHorizontal(lipgloss.Top, info, "    ", menu)

	logo := logoStyle.Render(strings.Join(logoLines, "\n"))
	gap := width - lipgloss.Width(left) - lipgloss.Width(logo)
	if width >= 84 && gap >= 2 {
		spacer := strings.Repeat(" ", gap)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, spacer, logo)
	}

	return left
}

func (m model) renderInfo() string {
	rows := [][2]string{
		{"Context", "opencode-manager"},
		{"Runtime", m.runtimeStatus()},
		{"Root", m.cfg.WorkspaceRoot},
		{"Workspaces", fmt.Sprintf("%d", len(m.workspaces))},
		{"Attention", m.attentionSummary()},
		{"Rev", appVersion},
	}

	keyWidth := 0
	for _, row := range rows {
		if len(row[0]) > keyWidth {
			keyWidth = len(row[0])
		}
	}

	lines := make([]string, len(rows))
	for i, row := range rows {
		key := infoKeyStyle.Render(fit(row[0]+":", keyWidth+1))
		valStyle := infoValStyle
		if row[0] == "Attention" {
			valStyle = lipgloss.NewStyle().Foreground(m.attentionColor())
		}
		lines[i] = key + " " + valStyle.Render(row[1])
	}

	return strings.Join(lines, "\n")
}

// attentionSummary describes how many running workspaces need the user: those
// blocked on a permission prompt and those finished and waiting for input.
func (m model) attentionSummary() string {
	approval, waiting := m.attentionCounts()
	if approval == 0 && waiting == 0 {
		return "all clear"
	}

	parts := make([]string, 0, 2)
	if approval > 0 {
		parts = append(parts, fmt.Sprintf("%d need approval", approval))
	}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", waiting))
	}
	return strings.Join(parts, ", ")
}

func (m model) attentionColor() lipgloss.Color {
	approval, waiting := m.attentionCounts()
	switch {
	case approval > 0:
		return colStopped
	case waiting > 0:
		return colStarting
	default:
		return colMuted
	}
}

func (m model) renderMenu() string {
	type entry struct{ key, desc string }
	entries := []entry{
		{":", "Command"},
		{"/", "Filter"},
		{"?", "Help"},
		{"↵", "Attach"},
		{"s", "Shell"},
		{"t", "Start/Stop"},
		{"d", "Describe"},
		{"e", "Edit"},
		{"u", "Update"},
		{"c", "Create"},
		{"^d", "Delete"},
		{"q", "Quit"},
	}

	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = menuKeyStyle.Render(fmt.Sprintf("<%s>", e.key)) + " " + menuTextStyle.Render(e.desc)
	}

	half := (len(lines) + 1) / 2
	col1 := lipgloss.JoinVertical(lipgloss.Left, lines[:half]...)
	col2 := lipgloss.JoinVertical(lipgloss.Left, lines[half:]...)

	return lipgloss.JoinHorizontal(lipgloss.Top, col1, "   ", col2)
}

func (m model) renderTable(width, height int) string {
	inner := width - 2
	contentWidth := inner - 2
	visible := m.visibleWorkspaces()

	widths := columnWidths(contentWidth)
	headers := []string{"NAME↑", "STATUS", "ACTIVITY", "RUNTIME", "CONTAINER", "IMAGE"}
	headerCells := make([]string, len(headers))
	for i, h := range headers {
		headerCells[i] = headerStyle.Render(fit(h, widths[i]))
	}
	headerRow := " " + strings.Join(headerCells, "  ") + " "

	bodyRows := make([]string, 0, height-1)
	bodyRows = append(bodyRows, headerRow)

	rowCapacity := height - 1
	switch {
	case m.loadError != "":
		bodyRows = append(bodyRows, " "+errorStyle.Render(fit(m.loadError, contentWidth)))
	case len(visible) == 0:
		empty := "No workspaces match the filter."
		if m.filter == "" {
			empty = "No workspaces yet. Press c to create one."
		}
		bodyRows = append(bodyRows, " "+mutedStyle.Render(fit(empty, contentWidth)))
	default:
		for i, ws := range visible {
			if len(bodyRows) >= rowCapacity {
				break
			}
			bodyRows = append(bodyRows, m.renderRow(ws, widths, contentWidth, i == m.workspacePos))
			_ = i
		}
	}

	for len(bodyRows) < height-1 {
		bodyRows = append(bodyRows, " "+strings.Repeat(" ", contentWidth)+" ")
	}

	return m.boxWithTitle(m.tableTitle(len(visible)), bodyRows, width)
}

func (m model) renderRow(ws workspace.Summary, widths []int, contentWidth int, selected bool) string {
	name := ws.Manifest.Name
	statusText, statusColor := m.workspaceStatus(ws)
	activityText, activityColor := m.workspaceActivity(ws)
	rt := ws.Manifest.Runtime
	container := ws.Manifest.ContainerName
	image := ws.Manifest.ImageName

	if selected {
		cells := []string{
			fit(name, widths[0]),
			fit(statusText, widths[1]),
			fit(activityText, widths[2]),
			fit(rt, widths[3]),
			fit(container, widths[4]),
			fit(image, widths[5]),
		}
		return cursorStyle.Render(" " + strings.Join(cells, "  ") + " ")
	}

	cells := []string{
		bodyStyle.Render(fit(name, widths[0])),
		lipgloss.NewStyle().Foreground(statusColor).Render(fit(statusText, widths[1])),
		lipgloss.NewStyle().Foreground(activityColor).Render(fit(activityText, widths[2])),
		mutedStyle.Render(fit(rt, widths[3])),
		mutedStyle.Render(fit(container, widths[4])),
		mutedStyle.Render(fit(image, widths[5])),
	}
	return " " + strings.Join(cells, "  ") + " "
}

func (m model) tableTitle(count int) string {
	scope := "all"
	if m.filter != "" {
		scope = "/" + m.filter
	}
	return titleStyle.Render("Workspaces") + counterStyle.Render(fmt.Sprintf("(%s)[%d]", scope, count))
}

// boxWithTitle draws a k9s-style bordered box with the title embedded in the
// top border line: ┌ Title ──────┐
func (m model) boxWithTitle(title string, rows []string, width int) string {
	titleWidth := lipgloss.Width(title)
	dashes := width - 4 - titleWidth
	if dashes < 0 {
		dashes = 0
	}

	top := borderStyle.Render("┌ ") + title + borderStyle.Render(" "+strings.Repeat("─", dashes)+"┐")
	bottom := borderStyle.Render("└" + strings.Repeat("─", width-2) + "┘")

	var b strings.Builder
	b.WriteString(top)
	b.WriteString("\n")
	for _, row := range rows {
		b.WriteString(borderStyle.Render("│"))
		b.WriteString(row)
		b.WriteString(borderStyle.Render("│"))
		b.WriteString("\n")
	}
	b.WriteString(bottom)

	return b.String()
}

func (m model) renderCrumbs() string {
	crumbs := crumbStyle.Render("workspaces")
	if m.showDescribe {
		crumbs += " " + crumbStyle.Render("describe")
	}
	if m.filter != "" {
		crumbs += " " + filterStyle.Render("/"+m.filter)
	}
	return crumbs
}

func (m model) renderPrompt(width int) string {
	switch {
	case m.commandMode:
		line := promptStyle.Render(":"+m.command) + "▏"
		if s := m.renderSuggestions(); s != "" {
			line += "  " + s
		}
		return line
	case m.filterMode:
		return filterStyle.Render("/"+m.filter) + "▏"
	default:
		style := mutedStyle
		if strings.Contains(strings.ToLower(m.message), "fail") || strings.Contains(strings.ToLower(m.message), "error") {
			style = errorStyle
		}
		return style.Render(fit(m.message, width))
	}
}

func (m model) renderSuggestions() string {
	matches := m.commandSuggestions()
	if len(matches) == 0 {
		return mutedStyle.Render("no matching command")
	}

	parts := make([]string, 0, len(matches))
	for _, match := range matches {
		parts = append(parts, menuKeyStyle.Render(match.Cmd))
	}

	return mutedStyle.Render("(" + strings.Join(parts, " ") + ")")
}

func (m model) renderHelp() string {
	rows := [][2]string{
		{":", "command mode"},
		{"/", "filter workspaces"},
		{"?", "toggle this help"},
		{"j / ↓", "down"},
		{"k / ↑", "up"},
		{"g / G", "top / bottom"},
		{"^f / ^b", "page down / up"},
		{"↵", "attach to workspace"},
		{"s", "shell into container"},
		{"t", "start / stop container"},
		{"d", "describe"},
		{"e", "edit"},
		{"u", "update OpenCode"},
		{"c", "create"},
		{"^d", "delete"},
		{"q / ^c", "quit"},
	}

	keyWidth := 0
	for _, row := range rows {
		if len(row[0]) > keyWidth {
			keyWidth = len(row[0])
		}
	}

	lines := make([]string, 0, len(rows)+2)
	for _, row := range rows {
		lines = append(lines, menuKeyStyle.Render(fit(row[0], keyWidth))+"  "+menuTextStyle.Render(row[1]))
	}
	lines = append(lines, "")
	lines = append(lines, mutedStyle.Render("Press any key to close."))

	return k9sDialog("Help", strings.Join(lines, "\n"), colBorder)
}

type describeField struct {
	key   string
	value string
	color lipgloss.Color // empty -> default value color
}

func (m model) describeFields(selected workspace.Summary) []describeField {
	manifest := selected.Manifest
	statusText, statusColor := m.workspaceStatus(selected)
	activityText, activityColor := m.workspaceActivity(selected)

	modules := "none"
	if len(manifest.Modules) > 0 {
		names := make([]string, 0, len(manifest.Modules))
		for _, mod := range manifest.Modules {
			names = append(names, mod.Name)
		}
		modules = strings.Join(names, ", ")
	}

	fields := []describeField{
		{key: "Name", value: manifest.Name},
		{key: "Status", value: statusText, color: statusColor},
		{key: "Activity", value: activityText, color: activityColor},
		{key: "Runtime", value: manifest.Runtime},
		{key: "Image", value: manifest.ImageName},
		{key: "Container", value: manifest.ContainerName},
		{key: "Home", value: manifest.HomeDir},
		{key: "Base image", value: manifest.Image.BaseImage},
		{key: "Modules", value: modules},
		{key: "Created", value: manifest.CreatedAt.Format(time.RFC3339)},
	}

	return append(fields, m.tokenFields(manifest.Name)...)
}

// tokenFields renders the tokscale token-usage synthesis (total and today) for
// the workspace, reflecting the current fetch state.
func (m model) tokenFields(name string) []describeField {
	if !m.isRunning(name) {
		return []describeField{{key: "Tokens", value: "start the container to measure usage", color: colMuted}}
	}

	state, ok := m.tokens[name]
	switch {
	case !ok || state.loading || !state.loaded:
		return []describeField{{key: "Tokens", value: "measuring with tokscale…", color: colMuted}}
	case state.err != "":
		return []describeField{{key: "Tokens", value: "tokscale error: " + state.err, color: colError}}
	}

	usage := state.usage
	return []describeField{
		{key: "Tokens total", value: fmt.Sprintf("%s tok   %s   %d msg", humanCount(usage.TotalTokens), money(usage.TotalCost), usage.TotalMsgs), color: colRunning},
		{key: "Tokens today", value: fmt.Sprintf("%s tok   %s   %d msg", humanCount(usage.TodayTokens), money(usage.TodayCost), usage.TodayMsgs)},
	}
}

// humanCount formats an integer with thousands separators, e.g. 1234567 -> 1,234,567.
func humanCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}

	var out strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(r)
	}

	if neg {
		return "-" + out.String()
	}
	return out.String()
}

func money(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}

// renderDescribePage renders the describe view as a full page (like k9s pushes
// a describe view), replacing the workspace table while it is open.
func (m model) renderDescribePage(width, height int) string {
	inner := width - 2
	contentWidth := inner - 2

	blank := " " + strings.Repeat(" ", contentWidth) + " "

	selected, ok := m.selectedWorkspace()
	if !ok {
		rows := []string{" " + mutedStyle.Render(fit("No workspace selected.", contentWidth)) + " "}
		for len(rows) < height-1 {
			rows = append(rows, blank)
		}
		return m.boxWithTitle(titleStyle.Render("Describe"), rows, width)
	}

	fields := m.describeFields(selected)
	labelWidth := 0
	for _, f := range fields {
		if len(f.key)+1 > labelWidth {
			labelWidth = len(f.key) + 1
		}
	}

	rows := make([]string, 0, height-1)
	rows = append(rows, blank)
	for _, f := range fields {
		key := infoKeyStyle.Render(fit(f.key+":", labelWidth))
		valWidth := contentWidth - labelWidth - 1
		valRaw := fit(f.value, valWidth)
		val := infoValStyle.Render(valRaw)
		if f.color != "" {
			val = lipgloss.NewStyle().Foreground(f.color).Render(valRaw)
		}
		rows = append(rows, " "+key+" "+val+" ")
	}
	for len(rows) < height-1 {
		rows = append(rows, blank)
	}

	title := titleStyle.Render("Describe") + counterStyle.Render("("+selected.Manifest.Name+")")
	return m.boxWithTitle(title, rows, width)
}

func (m model) renderDeleteConfirmation() string {
	name := m.selectedWorkspaceName()
	if name == "" {
		name = "selected workspace"
	}

	content := lipgloss.JoinVertical(
		lipgloss.Center,
		dialogText.Render("Delete workspace ")+dialogLabel.Render(name)+dialogText.Render("?"),
		dialogText.Render("This action cannot be undone."),
		"",
		dialogButtons([]string{"OK", "Cancel"}, m.dialogFocus),
	)

	return k9sDialog("Confirm Delete", content, colBorder)
}

func (m model) renderCreatePrompt() string {
	display := dialogLabel.Render(m.createName) + "▏"
	if m.createName == "" {
		display = mutedStyle.Render("workspace name") + "▏"
	}

	field := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(34).
		Render(display)

	content := lipgloss.JoinVertical(
		lipgloss.Center,
		dialogText.Render("Enter a name for the new workspace."),
		"",
		field,
		"",
		dialogButtons([]string{"OK", "Cancel"}, m.dialogFocus),
	)

	return k9sDialog("New Workspace", content, colBorder)
}

// k9sDialog renders a k9s-style popup: a bordered box with the title centered
// in the top border and the content block padded inside. The caller controls
// the internal alignment of content (center it with JoinVertical(Center) or
// keep it left-aligned with JoinVertical(Left)).
func k9sDialog(title, content string, border lipgloss.Color) string {
	lines := strings.Split(content, "\n")
	blockWidth := 0
	for _, line := range lines {
		if w := lipgloss.Width(line); w > blockWidth {
			blockWidth = w
		}
	}

	const padX = 3
	titleText := " " + title + " "
	inner := blockWidth + padX*2
	if w := lipgloss.Width(titleText) + 2; w > inner {
		inner = w
	}

	bs := lipgloss.NewStyle().Foreground(border)
	titleWidth := lipgloss.Width(titleText)
	leftDash := (inner - titleWidth) / 2
	rightDash := inner - titleWidth - leftDash

	top := bs.Render("┌"+strings.Repeat("─", leftDash)) + dialogLabel.Render(titleText) + bs.Render(strings.Repeat("─", rightDash)+"┐")
	padRow := bs.Render("│") + strings.Repeat(" ", inner) + bs.Render("│")
	bottom := bs.Render("└" + strings.Repeat("─", inner) + "┘")

	var b strings.Builder
	b.WriteString(top + "\n")
	b.WriteString(padRow + "\n")
	for _, line := range lines {
		right := inner - padX - lipgloss.Width(line)
		if right < 0 {
			right = 0
		}
		b.WriteString(bs.Render("│") + strings.Repeat(" ", padX) + line + strings.Repeat(" ", right) + bs.Render("│") + "\n")
	}
	b.WriteString(padRow + "\n")
	b.WriteString(bottom)

	return b.String()
}

// dialogButtons renders k9s-style buttons; the button at focused index is
// highlighted with the focus colors.
func dialogButtons(labels []string, focused int) string {
	parts := make([]string, len(labels))
	for i, label := range labels {
		if i == focused {
			parts[i] = dialogButtonHot.Render(label)
		} else {
			parts[i] = dialogButton.Render(label)
		}
	}

	return strings.Join(parts, "  ")
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

func (m model) commandSuggestions() []action {
	matches := make([]action, 0, len(actions))
	for _, a := range actions {
		if strings.HasPrefix(a.Cmd, m.command) {
			matches = append(matches, a)
		}
	}

	return matches
}

func (m model) runtimeStatus() string {
	if m.runtimeError != "" {
		return m.cfg.Runtime + " unavailable"
	}

	if m.runtimeName == "" {
		return m.cfg.Runtime + " checking"
	}

	return m.runtimeName + " available"
}

// workspaceStatus returns the display text and color for a workspace container.
func (m model) workspaceStatus(ws workspace.Summary) (string, lipgloss.Color) {
	status, ok := m.statuses[ws.Manifest.Name]
	if !ok || status.Container == "" {
		return "checking", colMuted
	}
	if status.Error != "" {
		return "error", colError
	}

	switch status.Container {
	case runtime.StatusRunning:
		return "running", colRunning
	case runtime.StatusCreated:
		return "created", colStarting
	case "restarting":
		return "starting", colStarting
	case "paused":
		return "paused", colStopped
	case runtime.StatusExited:
		return "stopped", colStopped
	case "dead":
		return "dead", colError
	case "removing":
		return "removing", colMuted
	case runtime.StatusMissing:
		return "missing", colMuted
	default:
		return status.Container, colMuted
	}
}

// workspaceActivity returns the display text and color for what opencode is
// doing inside a workspace. It only applies to a running container; otherwise
// the lifecycle STATUS column already conveys that the workspace is asleep.
func (m model) workspaceActivity(ws workspace.Summary) (string, lipgloss.Color) {
	status, ok := m.statuses[ws.Manifest.Name]
	if !ok {
		return "—", colMuted
	}

	switch status.Activity {
	case workspace.ActivityNew:
		return "unused", colTitle
	case workspace.ActivityWorking:
		return "working", colRunning
	case workspace.ActivityWaiting:
		return "waiting", colStarting
	case workspace.ActivityApproval:
		return "approval", colStopped
	case workspace.ActivityError:
		return "error", colError
	case workspace.ActivityAsleep:
		return "asleep", colMuted
	default:
		if status.Container == runtime.StatusRunning {
			return "starting", colMuted
		}
		return "—", colMuted
	}
}

// attentionCounts returns how many running workspaces are blocked on a
// permission prompt (approval) and how many have finished and are waiting for
// human input (waiting).
func (m model) attentionCounts() (approval, waiting int) {
	for _, status := range m.statuses {
		if status.Container != runtime.StatusRunning {
			continue
		}
		switch status.Activity {
		case workspace.ActivityApproval:
			approval++
		case workspace.ActivityWaiting:
			waiting++
		}
	}
	return approval, waiting
}

func (m *model) requestDelete() {
	if len(m.visibleWorkspaces()) == 0 {
		m.message = "No workspace selected."
		return
	}

	m.dialogFocus = 0
	m.confirmDelete = true
}

func (m model) describeSelected() (tea.Model, tea.Cmd) {
	selected, ok := m.selectedWorkspace()
	if !ok {
		m.message = "Describe requires a selected workspace."
		return m, nil
	}

	m.showDescribe = true
	m.message = "Describe — press esc to go back."

	// Refresh token usage if the container is running; tokscale runs inside it.
	name := selected.Manifest.Name
	if m.lifecycleErr == "" && m.isRunning(name) {
		m.tokens[name] = tokenState{loading: true}
		return m, m.fetchTokenUsage(selected)
	}

	return m, nil
}

func (m model) fetchTokenUsage(summary workspace.Summary) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		usage, err := m.lifecycle.TokenUsage(ctx, summary)
		return tokenUsageMsg{name: summary.Manifest.Name, usage: usage, err: err}
	}
}

func (m model) isRunning(name string) bool {
	status, ok := m.statuses[name]
	return ok && status.Container == runtime.StatusRunning
}

func (m model) workspaceActionMessage(action string) string {
	name := m.selectedWorkspaceName()
	if name == "" {
		return fmt.Sprintf("%s requires a selected workspace.", action)
	}

	return fmt.Sprintf("%s selected for %s. This action is not wired yet.", action, name)
}

func (m model) visibleWorkspaces() []workspace.Summary {
	if m.filter == "" {
		return m.workspaces
	}

	query := strings.ToLower(m.filter)
	out := make([]workspace.Summary, 0, len(m.workspaces))
	for _, ws := range m.workspaces {
		if strings.Contains(strings.ToLower(ws.Manifest.Name), query) {
			out = append(out, ws)
		}
	}

	return out
}

func (m model) selectedWorkspaceName() string {
	selected, ok := m.selectedWorkspace()
	if !ok {
		return ""
	}

	return selected.Manifest.Name
}

func (m model) selectedWorkspace() (workspace.Summary, bool) {
	visible := m.visibleWorkspaces()
	if len(visible) == 0 || m.workspacePos < 0 || m.workspacePos >= len(visible) {
		return workspace.Summary{}, false
	}

	return visible[m.workspacePos], true
}

func (m *model) moveWorkspace(delta int) {
	visible := m.visibleWorkspaces()
	if len(visible) == 0 {
		m.workspacePos = 0
		return
	}

	m.workspacePos = clamp(m.workspacePos+delta, 0, len(visible)-1)
}

func (m *model) clampSelection() {
	visible := m.visibleWorkspaces()
	if len(visible) == 0 {
		m.workspacePos = 0
		return
	}

	m.workspacePos = clamp(m.workspacePos, 0, len(visible)-1)
}

// columnWidths splits the available content width across the six columns,
// keeping STATUS, ACTIVITY, and RUNTIME fixed and sharing the rest.
func columnWidths(contentWidth int) []int {
	const gaps = 10 // five 2-space separators
	avail := contentWidth - gaps
	if avail < 20 {
		avail = 20
	}

	wStatus := 8
	wActivity := 9
	wRuntime := 7
	rest := avail - wStatus - wActivity - wRuntime
	if rest < 12 {
		rest = 12
	}

	wName := max(8, rest*30/100)
	wContainer := max(8, rest*35/100)
	wImage := max(6, rest-wName-wContainer)

	return []int{wName, wStatus, wActivity, wRuntime, wContainer, wImage}
}

// fit truncates or right-pads s to exactly w display columns.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}

	r := []rune(s)
	if len(r) > w {
		if w == 1 {
			return "…"
		}
		return string(r[:w-1]) + "…"
	}

	return s + strings.Repeat(" ", w-len(r))
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
