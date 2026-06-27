package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/module"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
	"github.com/mickael-menu/opencode-manager/internal/workspace"
)

// appVersion is the build version shown as "Rev" in the header. It is injected
// at release time from the git tag via -ldflags (see .github/workflows/package.yml)
// and stays "dev" for local builds, so it never needs to be edited by hand.
var appVersion = "dev"

// npmPackageName is the published package queried for update checks. It must
// stay in sync with the "name" field in package.json.
const npmPackageName = "@mickaelroger78/opencode-manager"

type model struct {
	cfg              config.Config
	registry         workspace.Registry
	templateRegistry workspace.TemplateRegistry
	lifecycle        workspace.Lifecycle
	lifecycleErr     string
	workspaces       []workspace.Summary
	statuses         map[string]workspace.Status
	workspacePos     int
	width            int
	height           int
	runtimeName      string
	runtimeError     string
	loadError        string
	message          string

	// baseImageReady gates the whole dashboard: until the managed base image
	// finishes building (baseImageReadyMsg), the UI shows a blocking overlay and
	// ignores every key except quit, so a workspace can't be created against a
	// base image that does not exist yet. baseImageErr holds the build failure,
	// if any, so the overlay can surface it instead of spinning forever.
	baseImageReady bool
	baseImageErr   string
	// baseSpinnerFrame advances on baseSpinnerTickMsg to animate the building
	// indicator; it only ticks while the base image is still being built.
	baseSpinnerFrame int

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
	versions      map[string]versionState

	// templates page (the ":templates" view; see templates.go). templatesMode
	// swaps the workspace table for the template list and reroutes key handling.
	templatesMode      bool
	templates          []workspace.Template
	templatePos        int
	templateFilter     string
	templateFilterMode bool

	// template create dialog: collecting a new template's name before opening the
	// module editor against it.
	templateCreateMode bool
	templateCreateName string

	// create-workspace template picker: the optional "Pick Template" step shown
	// after the name dialog when at least one template exists. createPendingName
	// carries the entered workspace name into the picker; createTemplatePos == 0
	// means "no template".
	createPicking     bool
	createPendingName string
	createTemplates   []workspace.Template
	createTemplatePos int

	// installing holds workspaces whose module install/uninstall job is still
	// running. Interactive container access (attach/shell) is frozen for these
	// so a user can't reach OpenCode mid-install, before scripts finish and the
	// server has reloaded. Keyed by workspace name; entry added when the job is
	// kicked off and removed when its editApplyMsg arrives.
	installing map[string]bool

	// provisioning holds workspaces whose initial image build + container
	// creation is still running. Until it completes the container does not yet
	// exist, so the runtime reports it as "missing"; this set lets the STATUS
	// column show "creating" instead. Keyed by workspace name; entry added when
	// createWorkspace kicks off provisioning and removed when its
	// provisionWorkspaceMsg arrives.
	provisioning map[string]bool

	// set when the npm registry reports a newer release than appVersion; the
	// header shows an "update available" notice (see checkForUpdate).
	updateLatest string

	// module edit flow (see edit.go)
	editMode    bool
	editEntries []editEntry
	editPos     int
	// editTemplateMode makes the module editor act on editTemplate (saving it)
	// instead of a selected workspace's live modules. See editTemplate/applyTemplateEdit.
	editTemplateMode bool
	editTemplate     workspace.Template
	// editCollapsed records which categories are collapsed in the module browser.
	// Categories start collapsed: the browser opens showing only category headers,
	// and the user expands one (enter) to reveal its modules.
	editCollapsed map[string]bool
	// editFilter narrows the module browser to entries whose name, description,
	// category, or instance label match; editFilterMode is true while typing it.
	editFilter      string
	editFilterMode  bool
	catalogErr      string
	editPrompting   bool
	editPromptMod   module.Module
	editPromptRow   int // editEntries index that triggered the prompt flow
	editPromptIdx   int
	editPromptInput string
	editPromptVals  map[string]string
	// selector state for select/multiselect prompts (the active prompt's choices)
	editPromptOptions []string
	editPromptChosen  []bool
	editPromptCursor  int

	// import picker state: choosing which host accounts to import as new
	// instances of a multi-instance module (e.g. existing AWS profiles).
	editImporting       bool
	editImportMod       module.Module
	editImportRow       int // editEntries index of the triggering add row
	editImportOptions   []string
	editImportChosen    []bool
	editImportCursor    int
	editImportManualIdx int // cursor position of the "add manually" action row

	// single-page form state: collecting all of a multi-instance module's
	// prompt values at once for a manually-added entry.
	editFormMode   bool
	editFormMod    module.Module
	editFormRow    int // editEntries index of the triggering add row
	editFormVals   []string
	editFormCursor int
}

// versionState caches the OpenCode version reported by a running workspace
// container so the dashboard column does not re-exec on every refresh.
type versionState struct {
	loading bool
	value   string
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

// resourceKind is a view the ":" prompt can switch to, k9s-style: ":" is no
// longer a general command line, only a way to jump between kinds. Right now the
// only kinds are the workspaces and templates pages.
type resourceKind struct {
	name    string
	aliases []string
}

var kinds = []resourceKind{
	{name: "workspaces", aliases: []string{"workspace", "ws"}},
	{name: "templates", aliases: []string{"template", "tmpl"}},
}

// resolveKind matches typed text against a kind's canonical name or an alias and
// returns the canonical name, or "" when nothing matches.
func resolveKind(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, k := range kinds {
		if text == k.name {
			return k.name
		}
		for _, a := range k.aliases {
			if text == a {
				return k.name
			}
		}
	}
	return ""
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
	updateStyle   = lipgloss.NewStyle().Foreground(colStarting).Bold(true)
	bodyStyle     = lipgloss.NewStyle().Foreground(colBody)
	mutedStyle    = lipgloss.NewStyle().Foreground(colMuted)
	errorStyle    = lipgloss.NewStyle().Foreground(colError)
	cursorStyle   = lipgloss.NewStyle().Foreground(colCursorFg).Background(colCursorBg).Bold(true)
	crumbStyle    = lipgloss.NewStyle().Foreground(colCursorFg).Background(colCursorBg).Bold(true).Padding(0, 1)
	filterStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(colFilter).Bold(true).Padding(0, 1)
	promptStyle   = lipgloss.NewStyle().Foreground(colTitle)
	borderStyle   = lipgloss.NewStyle().Foreground(colBorder)

	// module editor
	editCategoryStyle = lipgloss.NewStyle().Foreground(colFilter).Bold(true)
	editGroupStyle    = lipgloss.NewStyle().Foreground(colTitle).Bold(true)
	editAddStyle      = lipgloss.NewStyle().Foreground(colInfoKey)
	editDescStyle     = lipgloss.NewStyle().Foreground(colMuted).Italic(true)

	// k9s dialog ("Dialog") skin.
	dialogText      = lipgloss.NewStyle().Foreground(colBody)                                                                       // cadetblue
	dialogLabel     = lipgloss.NewStyle().Foreground(colMenuText).Bold(true)                                                        // white
	dialogButton    = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#483d8b")).Padding(0, 2) // darkslateblue
	dialogButtonHot = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(colBorder).Bold(true).Padding(0, 2)      // dodgerblue focus
	dialogButtonOff = lipgloss.NewStyle().Foreground(lipgloss.Color("#777777")).Background(lipgloss.Color("#333333")).Padding(0, 2) // disabled/greyed
)

var logoLines = []string{
	" ██████╗  ██████╗███╗   ███╗",
	"██╔═══██╗██╔════╝████╗ ████║",
	"██║   ██║██║     ██╔████╔██║",
	"██║   ██║██║     ██║╚██╔╝██║",
	"╚██████╔╝╚██████╗██║ ╚═╝ ██║",
	" ╚═════╝  ╚═════╝╚═╝     ╚═╝",
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

type versionMsg struct {
	name    string
	version string
	err     error
}

// updateAvailableMsg is emitted by checkForUpdate when the npm registry
// advertises a release newer than the running appVersion.
type updateAvailableMsg struct {
	latest string
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

// baseSpinnerTickMsg drives the base-image building animation. It runs on its
// own fast interval (the 2s refresh tick is far too slow to look alive) and
// only while the image is still being built.
type baseSpinnerTickMsg time.Time

const baseSpinnerInterval = 110 * time.Millisecond

func baseSpinnerCmd() tea.Cmd {
	return tea.Tick(baseSpinnerInterval, func(t time.Time) tea.Msg {
		return baseSpinnerTickMsg(t)
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

	slog.Info("starting TUI", "runtime", cfg.Runtime)

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
		slog.Error("failed to initialize workspace lifecycle", "error", err)
		lifecycleErr = err.Error()
	}

	return model{
		cfg:              cfg,
		registry:         workspace.NewRegistry(cfg),
		templateRegistry: workspace.NewTemplateRegistry(cfg),
		lifecycle:        lifecycle,
		lifecycleErr:     lifecycleErr,
		statuses:         map[string]workspace.Status{},
		tokens:           map[string]tokenState{},
		versions:         map[string]versionState{},
		installing:       map[string]bool{},
		provisioning:     map[string]bool{},
		width:            100,
		height:           30,
		message:          "Creating the base image...",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadWorkspaces, m.checkRuntime, m.ensureBaseImage, checkForUpdate, tickCmd(), baseSpinnerCmd())
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
			slog.Error("failed to load workspaces", "error", msg.err)
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
			slog.Warn("container runtime unavailable", "runtime", msg.name, "error", msg.err)
			m.runtimeError = msg.err.Error()
		} else {
			m.runtimeError = ""
		}
	case statusListMsg:
		next := make(map[string]workspace.Status, len(msg.statuses))
		ring := false
		var cmds []tea.Cmd
		for _, status := range msg.statuses {
			name := status.Workspace.Manifest.Name
			// Ring the bell when a workspace newly starts needing a human
			// (blocked on an approval prompt).
			if status.Activity == workspace.ActivityWaiting {
				if prev, ok := m.statuses[name]; !ok || prev.Activity != workspace.ActivityWaiting {
					ring = true
				}
			}
			next[name] = status

			// Fetch the running container's OpenCode version once and cache it;
			// drop the cache when it stops so a fresh value is read on restart.
			if status.Container == runtime.StatusRunning {
				if st, ok := m.versions[name]; !ok || (!st.loading && st.value == "") {
					m.versions[name] = versionState{loading: true}
					cmds = append(cmds, m.fetchVersion(status.Workspace))
				}
			} else {
				delete(m.versions, name)
			}

			// Refresh token usage via tokscale at launch (the first time we see the
			// container running) and whenever a workspace finishes a working turn
			// (working -> anything else), so the TOKENS column stays current without
			// running tokscale on every 2s tick. The last-known totals are kept when
			// the container stops, so the column still shows a value while idle.
			if status.Container == runtime.StatusRunning {
				prev, hadPrev := m.statuses[name]
				startedRunning := !hadPrev || prev.Container != runtime.StatusRunning
				finishedWorking := hadPrev && prev.Activity == workspace.ActivityWorking && status.Activity != workspace.ActivityWorking
				st, tracked := m.tokens[name]
				if !(tracked && st.loading) && (startedRunning || finishedWorking || !st.loaded) {
					m.tokens[name] = tokenState{loading: true}
					cmds = append(cmds, m.fetchTokenUsage(status.Workspace))
				}
			}
		}
		m.statuses = next
		if ring {
			cmds = append(cmds, bellCmd())
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
	case tickMsg:
		return m, tea.Batch(m.loadStatuses, tickCmd())
	case baseSpinnerTickMsg:
		// Stop animating (and stop rescheduling) once the build settles either
		// way; the overlay disappears on success and freezes on error.
		if m.baseImageReady || m.baseImageErr != "" {
			return m, nil
		}
		m.baseSpinnerFrame++
		return m, baseSpinnerCmd()
	case lifecycleActionMsg:
		if msg.err != nil {
			slog.Error("lifecycle action failed", "action", msg.action, "workspace", msg.name, "error", msg.err)
			m.message = fmt.Sprintf("%s failed for %s: %v", msg.action, msg.name, msg.err)
			return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
		}
		slog.Info("lifecycle action completed", "action", msg.action, "workspace", msg.name)
		m.message = fmt.Sprintf("%s completed for %s.", msg.action, msg.name)
		return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
	case provisionWorkspaceMsg:
		delete(m.provisioning, msg.name)
		if msg.err != nil {
			slog.Error("workspace provisioning failed", "workspace", msg.name, "error", msg.err)
			m.message = fmt.Sprintf("Create runtime provisioning failed for %s: %v", msg.name, msg.err)
			return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
		}
		slog.Info("workspace provisioned", "workspace", msg.name)
		m.message = fmt.Sprintf("Created workspace %s — container is up and running.", msg.name)
		return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
	case updateActionMsg:
		if msg.err != nil {
			slog.Error("OpenCode update failed", "workspace", msg.name, "error", msg.err)
			m.message = fmt.Sprintf("Update failed for %s: %v", msg.name, msg.err)
			return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
		}
		slog.Info("OpenCode updated", "workspace", msg.name, "version", msg.version)
		m.message = fmt.Sprintf("OpenCode updated to %s in %s.", msg.version, msg.name)
		delete(m.versions, msg.name)
		return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
	case editApplyMsg:
		delete(m.installing, msg.name)
		if msg.err != nil {
			slog.Error("module edit failed", "workspace", msg.name, "error", msg.err)
			m.message = fmt.Sprintf("Module edit failed for %s: %v", msg.name, msg.err)
			return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
		}
		slog.Info("module edit completed", "workspace", msg.name, "summary", msg.summary)
		m.message = fmt.Sprintf("Modules updated for %s: %s.", msg.name, msg.summary)
		return m, tea.Batch(m.loadWorkspaces, m.loadStatuses)
	case baseImageReadyMsg:
		if msg.err != nil {
			slog.Error("base image creation failed", "error", msg.err)
			m.baseImageErr = msg.err.Error()
			m.message = fmt.Sprintf("Base image creation failed: %v", msg.err)
			return m, nil
		}
		slog.Info("base image ready")
		m.baseImageReady = true
		m.baseImageErr = ""
		m.message = "Base image ready. Press : for commands, / to filter, ? for help."
		return m, nil
	case attachReadyMsg:
		if msg.err != nil {
			slog.Error("session start failed", "kind", msg.noun, "workspace", msg.name, "error", msg.err)
			m.message = fmt.Sprintf("%s failed for %s: %v", msg.noun, msg.name, msg.err)
			return m, m.loadStatuses
		}
		slog.Info("session started", "kind", msg.noun, "workspace", msg.name)
		m.message = fmt.Sprintf("%s session started for %s.", msg.noun, msg.name)
		return m, msg.cmd
	case workspace.AttachResultMsg:
		switch {
		case msg.StillRunning:
			// Detached via Ctrl-C; the attach client exits non-zero but the
			// container keeps running, so this is not a failure.
			slog.Debug("detached from workspace, container still running")
			m.message = "Detached (Ctrl-C). Container still running in the background."
		case msg.Err != nil:
			slog.Error("attach session failed", "error", msg.Err)
			m.message = fmt.Sprintf("Attach session failed: %v", msg.Err)
		default:
			slog.Debug("attach session closed, container stopped")
			m.message = "Attach session closed; container stopped."
		}
		return m, m.loadStatuses
	case workspace.ShellResultMsg:
		if msg.Err != nil {
			slog.Error("shell session failed", "error", msg.Err)
			m.message = fmt.Sprintf("Shell session failed: %v", msg.Err)
		} else {
			slog.Debug("shell session closed")
			m.message = "Shell session closed."
		}
		return m, m.loadStatuses
	case tokenUsageMsg:
		state := tokenState{loaded: true}
		if msg.err != nil {
			slog.Warn("failed to read token usage", "workspace", msg.name, "error", msg.err)
			state.err = msg.err.Error()
		} else {
			state.usage = msg.usage
		}
		m.tokens[msg.name] = state
		return m, nil
	case versionMsg:
		value := msg.version
		if msg.err != nil || value == "" {
			value = "unknown"
		}
		m.versions[msg.name] = versionState{value: value}
		return m, nil
	case updateAvailableMsg:
		slog.Info("update available", "current", appVersion, "latest", msg.latest)
		m.updateLatest = msg.latest
		return m, nil
	}

	return m, nil
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Until the managed base image exists, the dashboard is frozen behind a
	// blocking overlay: only quitting is allowed so nothing can act on a base
	// image that is still being built (or failed to build).
	if !m.baseImageReady {
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
		return m, nil
	}

	if m.confirmDelete {
		return m.updateDeleteConfirmation(msg)
	}
	if m.createPicking {
		return m.updateCreatePick(msg)
	}
	if m.createMode {
		return m.updateCreate(msg)
	}
	if m.templateCreateMode {
		return m.updateTemplateCreate(msg)
	}
	if m.commandMode {
		return m.updateCommand(msg)
	}
	if m.filterMode {
		return m.updateFilter(msg)
	}
	if m.templateFilterMode {
		return m.updateTemplateFilter(msg)
	}
	if m.editFormMode {
		return m.updateEditForm(msg)
	}
	if m.editImporting {
		return m.updateEditImport(msg)
	}
	if m.editPrompting {
		return m.updateEditPrompt(msg)
	}
	if m.editFilterMode {
		return m.updateEditFilter(msg)
	}
	if m.editMode {
		return m.updateEdit(msg)
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
	if m.templatesMode {
		return m.updateTemplates(msg)
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
			if m.templatesMode {
				return m.deleteSelectedTemplate()
			}
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
		// OK is disabled while the name is invalid; ignore Enter on it.
		if _, ok := m.validateCreateName(); !ok {
			return m, nil
		}
		return m.startCreatePick(strings.TrimSpace(m.createName))
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

// executeCommand handles the ":" prompt. k9s-style, it only switches the visible
// kind (workspaces/templates); it is not a general command line. "q"/"quit" are
// kept because they are muscle memory in k9s.
func (m model) executeCommand() (tea.Model, tea.Cmd) {
	command := strings.TrimSpace(m.command)
	m.commandMode = false
	m.command = ""

	switch strings.ToLower(command) {
	case "":
		return m, nil
	case "q", "quit":
		return m, tea.Quit
	}

	switch resolveKind(command) {
	case "workspaces":
		return m.showWorkspaces()
	case "templates":
		return m.showTemplates()
	default:
		m.message = fmt.Sprintf("No such view %q. Available: workspaces, templates.", command)
		return m, nil
	}
}

// validateCreateName checks the in-progress workspace name entered in the
// create dialog. It returns a short human reason and whether the name is
// acceptable; an empty reason with ok=false means simply "too short / empty"
// (no error to show yet, just a disabled OK button). A name is valid when it
// has at least one character, contains only letters (accents included), digits,
// '-' or '_', and is not already used by another workspace.
func (m model) validateCreateName() (string, bool) {
	name := strings.TrimSpace(m.createName)
	if name == "" {
		return "", false
	}

	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return "Use only letters, digits, '-' or '_'.", false
	}

	for _, ws := range m.workspaces {
		if strings.EqualFold(ws.Manifest.Name, name) {
			return "That name is already taken.", false
		}
	}

	return "", true
}

func (m model) createWorkspace(name string, tmpl *workspace.Template) (tea.Model, tea.Cmd) {
	if name == "" {
		m.message = "Workspace name is required."
		return m, nil
	}

	result, err := m.registry.Create(name)
	if err != nil {
		slog.Error("failed to create workspace", "name", name, "error", err)
		m.message = fmt.Sprintf("Create failed: %v", err)
		return m, nil
	}

	// Applying a template seeds the new workspace's manifest with its modules so
	// reconcile (run during provisioning's EnsureStarted) installs them on first
	// start — no per-module install call is needed here.
	if tmpl != nil && len(tmpl.Modules) > 0 {
		manifest := result.Manifest
		manifest.Modules = cloneModuleInstances(tmpl.Modules)
		manifest.UpdatedAt = time.Now().UTC()
		if err := workspace.SaveManifest(filepath.Join(result.Path, workspace.ManifestFile), manifest); err != nil {
			slog.Error("failed to apply template to workspace", "name", name, "template", tmpl.Name, "error", err)
			m.message = fmt.Sprintf("Created workspace %s but applying template %q failed: %v", result.Manifest.Name, tmpl.Name, err)
			return m, m.loadWorkspaces
		}
		result.Manifest = manifest
	}

	m.createMode = false
	m.createPicking = false
	m.createName = ""
	m.createPendingName = ""
	if tmpl != nil && len(tmpl.Modules) > 0 {
		m.message = fmt.Sprintf("Created workspace %s from template %q. Building image, starting container, installing modules...", result.Manifest.Name, tmpl.Name)
	} else {
		m.message = fmt.Sprintf("Created workspace %s. Building image and starting container...", result.Manifest.Name)
	}
	m.provisioning[result.Manifest.Name] = true
	created := workspace.Summary{Manifest: result.Manifest, Path: result.Path}
	return m, tea.Batch(m.loadWorkspaces, m.provisionWorkspace(created))
}

// cloneModuleInstances returns a deep-enough copy of a template's modules so the
// new workspace manifest does not share the template's value maps in memory.
func cloneModuleInstances(src []workspace.ModuleInstance) []workspace.ModuleInstance {
	out := make([]workspace.ModuleInstance, len(src))
	for i, inst := range src {
		out[i] = inst
		if inst.Values != nil {
			values := make(map[string]any, len(inst.Values))
			for k, v := range inst.Values {
				values[k] = v
			}
			out[i].Values = values
		}
	}
	return out
}

func (m model) provisionWorkspace(summary workspace.Summary) tea.Cmd {
	return func() tea.Msg {
		if m.lifecycleErr != "" {
			return provisionWorkspaceMsg{name: summary.Manifest.Name, err: errors.New(m.lifecycleErr)}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		// Provision the image/container and start it so a freshly created
		// workspace is ready to attach to immediately.
		err := m.lifecycle.EnsureStarted(ctx, summary)
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
	if m.installing[selected.Manifest.Name] {
		m.message = fmt.Sprintf("Installing modules in %s. Wait until it finishes before attaching.", selected.Manifest.Name)
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
	if m.installing[selected.Manifest.Name] {
		m.message = fmt.Sprintf("Installing modules in %s. Wait until it finishes before opening a shell.", selected.Manifest.Name)
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
	case workspace.ActivityWorking, workspace.ActivityWaiting:
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
		return m.editSelected()
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
		m.command = matches[0]
	}
}

func (m model) View() string {
	width := max(40, m.width)
	height := max(12, m.height)

	header := m.renderHeader(width)
	headerHeight := lipgloss.Height(header)

	crumbs := m.renderCrumbs()
	prompt := m.renderPrompt(width)

	// k9s-style: the ":" command prompt is a small box wedged between the top bar
	// and the main view, shrinking the body by its height while it is open.
	commandBox := ""
	commandHeight := 0
	if m.commandMode {
		commandBox = m.renderCommandBox(width)
		commandHeight = lipgloss.Height(commandBox)
	}

	bodyHeight := height - headerHeight - lipgloss.Height(crumbs) - lipgloss.Height(prompt) - commandHeight - 1
	bodyHeight = max(3, bodyHeight)

	var body string
	switch {
	case m.editMode:
		body = m.renderEditPage(width, bodyHeight)
	case m.templatesMode:
		body = m.renderTemplatesPage(width, bodyHeight)
	case m.showDescribe:
		body = m.renderDescribePage(width, bodyHeight)
	default:
		body = m.renderTable(width, bodyHeight)
	}

	var view string
	if m.commandMode {
		view = lipgloss.JoinVertical(lipgloss.Left, header, commandBox, body, crumbs, prompt)
	} else {
		view = lipgloss.JoinVertical(lipgloss.Left, header, body, crumbs, prompt)
	}

	if m.editPrompting {
		view = overlayCentered(view, m.renderEditPrompt(), width, height)
	}
	if m.editImporting {
		view = overlayCentered(view, m.renderEditImport(), width, height)
	}
	if m.editFormMode {
		view = overlayCentered(view, m.renderEditForm(), width, height)
	}
	if m.createMode {
		view = overlayCentered(view, m.renderCreatePrompt(), width, height)
	}
	if m.createPicking {
		view = overlayCentered(view, m.renderCreatePick(), width, height)
	}
	if m.templateCreateMode {
		view = overlayCentered(view, m.renderTemplateCreatePrompt(), width, height)
	}
	if m.confirmDelete {
		view = overlayCentered(view, m.renderDeleteConfirmation(), width, height)
	}
	if m.showHelp {
		view = overlayCentered(view, m.renderHelp(), width, height)
	}

	// The base-image gate sits on top of everything: while it is up the rest of
	// the UI is frozen, so it must always win the overlay stack.
	if !m.baseImageReady {
		view = overlayCentered(view, m.renderBaseImageBuilding(), width, height)
	}

	return view
}

// renderBaseImageBuilding is the blocking dialog shown until the managed base
// image is built. It freezes the dashboard so no workspace can be created
// before its base image exists, and surfaces the build error if one occurred.
func (m model) renderBaseImageBuilding() string {
	if m.baseImageErr != "" {
		content := lipgloss.JoinVertical(
			lipgloss.Center,
			errorStyle.Render("Base image creation failed."),
			"",
			dialogText.Render(m.baseImageErr),
			"",
			dialogText.Render("Press ")+dialogLabel.Render("q")+dialogText.Render(" to quit."),
		)
		return k9sDialog("Base Image", content, colError)
	}

	content := lipgloss.JoinVertical(
		lipgloss.Center,
		dialogLabel.Render("Creating the base image…"),
		"",
		snakeBar(m.baseSpinnerFrame, 30, 5),
		"",
		dialogText.Render("Setting up the shared workspace image."),
		dialogText.Render("This can take a minute on first run."),
		"",
		mutedStyle.Render("Please wait — the dashboard is locked until this finishes."),
	)
	return k9sDialog("Base Image", content, colBorder)
}

// snakeBar renders a fixed-width "knight rider" track with a lit segment that
// bounces back and forth, animated by frame. It signals real, ongoing work
// (each frame advances on a base-image build tick) rather than a static spinner.
func snakeBar(frame, width, snake int) string {
	if snake > width {
		snake = width
	}
	travel := width - snake
	if travel <= 0 {
		return titleStyle.Render(strings.Repeat("█", width))
	}

	// A full cycle slides right (travel steps) then back left (travel steps).
	pos := frame % (2 * travel)
	if pos > travel {
		pos = 2*travel - pos
	}

	left := mutedStyle.Render(strings.Repeat("░", pos))
	lit := titleStyle.Render(strings.Repeat("█", snake))
	right := mutedStyle.Render(strings.Repeat("░", travel-pos))
	return left + lit + right
}

func (m model) renderHeader(width int) string {
	info := m.renderInfo()
	menu := m.renderMenu()
	left := lipgloss.JoinHorizontal(lipgloss.Top, info, "    ", menu)

	logo := logoStyle.Render(strings.Join(logoLines, "\n"))
	gap := width - lipgloss.Width(left) - lipgloss.Width(logo)
	if width >= 100 && gap >= 2 {
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
		if row[0] == "Rev" && m.updateLatest != "" {
			notice := updateStyle.Render(fmt.Sprintf("  ⬆ update available: %s", m.updateLatest))
			lines[i] = key + " " + infoValStyle.Render(row[1]) + notice
			continue
		}
		valStyle := infoValStyle
		if row[0] == "Attention" {
			valStyle = lipgloss.NewStyle().Foreground(m.attentionColor())
		}
		lines[i] = key + " " + valStyle.Render(row[1])
	}

	return strings.Join(lines, "\n")
}

// attentionSummary describes running workspaces that need the user: those
// blocked on a permission prompt (waiting), plus those that have finished their
// turn and gone idle (sleeping).
func (m model) attentionSummary() string {
	waiting, sleeping := m.attentionCounts()
	if waiting == 0 && sleeping == 0 {
		return "all clear"
	}

	parts := make([]string, 0, 2)
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", waiting))
	}
	if sleeping > 0 {
		parts = append(parts, fmt.Sprintf("%d sleeping", sleeping))
	}
	return strings.Join(parts, ", ")
}

func (m model) attentionColor() lipgloss.Color {
	waiting, sleeping := m.attentionCounts()
	switch {
	case waiting > 0:
		return colStopped
	case sleeping > 0:
		return colMuted
	default:
		return colMuted
	}
}

func (m model) renderMenu() string {
	type entry struct{ key, desc string }
	entries := []entry{
		{":", "Switch view"},
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
	if m.templatesMode {
		// The templates page supports a smaller action set; surface it instead of
		// the workspace actions that do not apply here.
		entries = []entry{
			{":", "Switch view"},
			{"/", "Filter"},
			{"?", "Help"},
			{"↵", "Edit"},
			{"e", "Edit"},
			{"c", "Create"},
			{"^d", "Delete"},
			{"q", "Quit"},
		}
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
	headers := []string{"NAME↑", "STATUS", "ACTIVITY", "RUNTIME", "OPENCODE", "TOKENS I/O/C", "CONTAINER", "AGE"}
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
	version := m.workspaceVersion(ws)
	tokens := m.workspaceTokens(ws)
	container := ws.Manifest.ContainerName
	age := m.workspaceAge(ws)

	if selected {
		cells := []string{
			fit(name, widths[0]),
			fit(statusText, widths[1]),
			fit(activityText, widths[2]),
			fit(rt, widths[3]),
			fit(version, widths[4]),
			fit(tokens, widths[5]),
			fit(container, widths[6]),
			fit(age, widths[7]),
		}
		return cursorStyle.Render(" " + strings.Join(cells, "  ") + " ")
	}

	cells := []string{
		bodyStyle.Render(fit(name, widths[0])),
		lipgloss.NewStyle().Foreground(statusColor).Render(fit(statusText, widths[1])),
		lipgloss.NewStyle().Foreground(activityColor).Render(fit(activityText, widths[2])),
		mutedStyle.Render(fit(rt, widths[3])),
		mutedStyle.Render(fit(version, widths[4])),
		mutedStyle.Render(fit(tokens, widths[5])),
		mutedStyle.Render(fit(container, widths[6])),
		mutedStyle.Render(fit(age, widths[7])),
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
	base := "workspaces"
	if m.templatesMode {
		base = "templates"
	}
	crumbs := crumbStyle.Render(base)
	if m.showDescribe {
		crumbs += " " + crumbStyle.Render("describe")
	}
	if m.editMode {
		crumbs += " " + crumbStyle.Render("edit")
	}
	if m.templatesMode && m.templateFilter != "" {
		crumbs += " " + filterStyle.Render("/"+m.templateFilter)
	}
	if !m.templatesMode && m.filter != "" {
		crumbs += " " + filterStyle.Render("/"+m.filter)
	}
	return crumbs
}

func (m model) renderPrompt(width int) string {
	switch {
	case m.filterMode:
		return filterStyle.Render("/"+m.filter) + "▏"
	case m.templateFilterMode:
		return filterStyle.Render("/"+m.templateFilter) + "▏"
	default:
		style := mutedStyle
		if strings.Contains(strings.ToLower(m.message), "fail") || strings.Contains(strings.ToLower(m.message), "error") {
			style = errorStyle
		}
		return style.Render(fit(m.message, width))
	}
}

// renderCommandBox draws the k9s-style command prompt: a small rounded box shown
// between the top bar and the main view while ":" is active. ":" only switches
// the visible kind (workspaces/templates), so the box shows the typed text with
// an inline ghost completion plus the matching kinds.
func (m model) renderCommandBox(width int) string {
	bs := lipgloss.NewStyle().Foreground(colBorderFocus)

	typed := m.command
	matches := m.commandSuggestions()

	// Ghost completion: grey out the remainder of the single best match, k9s-style.
	ghost := ""
	if len(matches) > 0 && strings.HasPrefix(matches[0], strings.ToLower(typed)) {
		ghost = matches[0][len(typed):]
	}

	full := func(withHint bool) string {
		s := titleStyle.Render("> ") + dialogLabel.Render(typed) + mutedStyle.Render(ghost) + "▏"
		if !withHint {
			return s
		}
		if len(matches) > 0 {
			return s + mutedStyle.Render("  "+strings.Join(matches, "  "))
		}
		return s + errorStyle.Render("  no matching view")
	}

	cw := width - 4 // content area: width minus borders (2) and side padding (2)
	content := full(true)
	if lipgloss.Width(content) > cw {
		content = full(false) // drop the hint when it would overflow the box
	}
	pad := cw - lipgloss.Width(content)
	if pad < 0 {
		pad = 0
	}

	top := bs.Render("╭" + strings.Repeat("─", width-2) + "╮")
	mid := bs.Render("│") + " " + content + strings.Repeat(" ", pad) + " " + bs.Render("│")
	bottom := bs.Render("╰" + strings.Repeat("─", width-2) + "╯")
	return top + "\n" + mid + "\n" + bottom
}

func (m model) renderHelp() string {
	rows := [][2]string{
		{":", "switch view (:workspaces, :templates)"},
		{"/", "filter"},
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
			names = append(names, mod.InstanceID())
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
		{key: "Tokens total", value: fmt.Sprintf("%s tok (%s in / %s out / %s cache)   %s   %d msg", humanCount(usage.TotalTokens), humanCount(usage.TotalInput), humanCount(usage.TotalOutput), humanCount(usage.TotalCacheRead), money(usage.TotalCost), usage.TotalMsgs), color: colRunning},
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
	noun := "workspace"
	name := m.selectedWorkspaceName()
	if m.templatesMode {
		noun = "template"
		name = ""
		if t, ok := m.selectedTemplate(); ok {
			name = t.Name
		}
	}
	if name == "" {
		name = "selected " + noun
	}

	content := lipgloss.JoinVertical(
		lipgloss.Center,
		dialogText.Render("Delete "+noun+" ")+dialogLabel.Render(name)+dialogText.Render("?"),
		dialogText.Render("This action cannot be undone."),
		"",
		dialogButtons([]string{"OK", "Cancel"}, m.dialogFocus),
	)

	return k9sDialog("Confirm Delete", content, colBorder)
}

func (m model) renderCreatePrompt() string {
	display := dialogLabel.Render(m.createName) + "▏"

	field := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(34).
		Render(display)

	reason, ok := m.validateCreateName()
	// Keep a blank line where the reason goes so the dialog height is stable.
	hint := " "
	if reason != "" {
		hint = errorStyle.Render(reason)
	}

	content := lipgloss.JoinVertical(
		lipgloss.Center,
		dialogText.Render("Enter a name for the new workspace."),
		"",
		field,
		hint,
		"",
		createDialogButtons(m.dialogFocus, ok),
	)

	return k9sDialog("New Workspace", content, colBorder)
}

// createDialogButtons renders the create dialog's OK/Cancel row. OK is greyed
// out and unfocusable-looking when okEnabled is false (invalid name).
func createDialogButtons(focused int, okEnabled bool) string {
	var okBtn string
	switch {
	case !okEnabled:
		okBtn = dialogButtonOff.Render("OK")
	case focused == 0:
		okBtn = dialogButtonHot.Render("OK")
	default:
		okBtn = dialogButton.Render("OK")
	}

	cancelBtn := dialogButton.Render("Cancel")
	if focused == 1 {
		cancelBtn = dialogButtonHot.Render("Cancel")
	}

	return okBtn + "  " + cancelBtn
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

// commandSuggestions returns the canonical kind names whose name or one of their
// aliases starts with the text typed at the ":" prompt.
func (m model) commandSuggestions() []string {
	q := strings.ToLower(strings.TrimSpace(m.command))
	matches := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if kindMatchesPrefix(k, q) {
			matches = append(matches, k.name)
		}
	}
	return matches
}

func kindMatchesPrefix(k resourceKind, q string) bool {
	if q == "" || strings.HasPrefix(k.name, q) {
		return true
	}
	for _, a := range k.aliases {
		if strings.HasPrefix(a, q) {
			return true
		}
	}
	return false
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
	// A freshly created workspace has no container yet (the runtime reports it
	// "missing") while its image builds and the container is created. Surface
	// that in-progress work as "creating" until provisioning completes.
	if m.provisioning[ws.Manifest.Name] {
		return "creating", colStarting
	}

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
// the lifecycle STATUS column already conveys that the container is not up.
func (m model) workspaceActivity(ws workspace.Summary) (string, lipgloss.Color) {
	// A running install/uninstall job freezes interactive access (see
	// attachSelected/shellSelected), so surface it ahead of the plugin-reported
	// activity to make clear why attaching is refused.
	if m.installing[ws.Manifest.Name] {
		return "installing", colStarting
	}

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
		return "waiting", colStopped
	case workspace.ActivitySleeping:
		return "sleeping", colMuted
	case workspace.ActivityError:
		return "error", colError
	case workspace.ActivityOff:
		return "off", colMuted
	default:
		if status.Container == runtime.StatusRunning {
			return "starting", colMuted
		}
		return "—", colMuted
	}
}

// attentionCounts returns how many running workspaces are blocked on a
// permission prompt (waiting) and how many have finished their turn and gone
// idle (sleeping).
func (m model) attentionCounts() (waiting, sleeping int) {
	for _, status := range m.statuses {
		if status.Container != runtime.StatusRunning {
			continue
		}
		switch status.Activity {
		case workspace.ActivityWaiting:
			waiting++
		case workspace.ActivitySleeping:
			sleeping++
		}
	}
	return waiting, sleeping
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

func (m model) fetchVersion(summary workspace.Summary) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		version, err := m.lifecycle.OpenCodeVersion(ctx, summary)
		return versionMsg{name: summary.Manifest.Name, version: version, err: err}
	}
}

// checkForUpdate queries the npm registry for the latest published release and,
// when it is newer than the running build, returns an updateAvailableMsg so the
// header can advertise it. It is best-effort: development builds and any network
// or parse failure simply yield no message (the dashboard works offline).
func checkForUpdate() tea.Msg {
	if appVersion == "dev" {
		return nil
	}

	endpoint := "https://registry.npmjs.org/-/package/" + url.PathEscape(npmPackageName) + "/dist-tags"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		slog.Debug("update check: build request failed", "error", err)
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Debug("update check: request failed", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Debug("update check: unexpected status", "status", resp.StatusCode)
		return nil
	}

	var tags struct {
		Latest string `json:"latest"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		slog.Debug("update check: decode failed", "error", err)
		return nil
	}
	if tags.Latest == "" || !versionIsNewer(tags.Latest, appVersion) {
		return nil
	}
	return updateAvailableMsg{latest: tags.Latest}
}

// versionIsNewer reports whether semantic version latest is strictly greater
// than current. It compares the major.minor.patch triple and ignores any
// pre-release or build metadata; unparseable parts count as 0.
func versionIsNewer(latest, current string) bool {
	l := parseVersion(latest)
	c := parseVersion(current)
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseVersion(s string) [3]int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(s, ".", 3) {
		if i > 2 {
			break
		}
		out[i], _ = strconv.Atoi(part)
	}
	return out
}

// workspaceVersion returns the OpenCode version display text for a workspace:
// a dash when the container is stopped, an ellipsis while it is being read, and
// the cached version once known.
func (m model) workspaceVersion(ws workspace.Summary) string {
	if !m.isRunning(ws.Manifest.Name) {
		return "—"
	}
	st, ok := m.versions[ws.Manifest.Name]
	if !ok || st.loading || st.value == "" {
		return "…"
	}
	return st.value
}

// workspaceTokens returns the input/output token display for a workspace's
// TOKENS column: a dash when never measured, an ellipsis while the first
// measurement is in flight, "err" when tokscale failed, otherwise the compact
// all-time "input/output" totals reported by tokscale. The last-known totals are
// shown even when the container is stopped (tokscale can only run while it is up).
func (m model) workspaceTokens(ws workspace.Summary) string {
	st, ok := m.tokens[ws.Manifest.Name]
	switch {
	case !ok || (!st.loaded && !st.loading):
		return "—"
	case st.loading && !st.loaded:
		return "…"
	case st.err != "":
		return "err"
	default:
		u := st.usage
		return compactCount(u.TotalInput) + "/" + compactCount(u.TotalOutput) + "/" + compactCount(u.TotalCacheRead)
	}
}

// compactCount formats a token count for the narrow TOKENS column: 1234 -> "1.2k",
// 2_500_000 -> "2.5M". Values below 1000 are shown as-is. The ".0" suffix is
// trimmed so e.g. 2000 renders as "2k" rather than "2.0k".
func compactCount(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return trimDotZero(fmt.Sprintf("%.1f", float64(n)/1000)) + "k"
	case n < 1_000_000_000:
		return trimDotZero(fmt.Sprintf("%.1f", float64(n)/1_000_000)) + "M"
	default:
		return trimDotZero(fmt.Sprintf("%.1f", float64(n)/1_000_000_000)) + "B"
	}
}

func trimDotZero(s string) string {
	return strings.TrimSuffix(s, ".0")
}

// workspaceAge returns the k9s-style AGE for a workspace: the time elapsed since
// it was created, formatted compactly (e.g. "45s", "3m20s", "2d5h").
func (m model) workspaceAge(ws workspace.Summary) string {
	created := ws.Manifest.CreatedAt
	if created.IsZero() {
		return "—"
	}
	return humanDuration(time.Since(created))
}

// humanDuration formats a duration the way k9s (via kubectl's
// k8s.io/apimachinery/pkg/util/duration.HumanDuration) renders the AGE column,
// scaling the unit and number of components to the magnitude.
func humanDuration(d time.Duration) string {
	if seconds := int(d.Seconds()); seconds < 0 {
		return "0s"
	} else if seconds < 60*2 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := int(d / time.Minute)
	if minutes < 10 {
		s := int(d/time.Second) % 60
		if s == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm%ds", minutes, s)
	} else if minutes < 60*3 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := int(d / time.Hour)
	if hours < 8 {
		m := int(d/time.Minute) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, m)
	} else if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	} else if hours < 24*8 {
		h := hours % 24
		if h == 0 {
			return fmt.Sprintf("%dd", hours/24)
		}
		return fmt.Sprintf("%dd%dh", hours/24, h)
	} else if hours < 24*365*2 {
		return fmt.Sprintf("%dd", hours/24)
	} else if hours < 24*365*8 {
		dy := int(d/(time.Hour*24)) % 365
		if dy == 0 {
			return fmt.Sprintf("%dy", hours/24/365)
		}
		return fmt.Sprintf("%dy%dd", hours/24/365, dy)
	}
	return fmt.Sprintf("%dy", int(d/(time.Hour*24))/365)
}

func (m model) isRunning(name string) bool {
	status, ok := m.statuses[name]
	return ok && status.Container == runtime.StatusRunning
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

// columnWidths splits the available content width across the eight columns,
// keeping STATUS, ACTIVITY, RUNTIME, OPENCODE, TOKENS, and AGE fixed and sharing
// the rest between NAME and CONTAINER.
func columnWidths(contentWidth int) []int {
	const gaps = 14 // seven 2-space separators
	avail := contentWidth - gaps
	if avail < 20 {
		avail = 20
	}

	wStatus := 8
	wActivity := 9
	wRuntime := 7
	wVersion := 9
	wTokens := 18
	wAge := 6
	rest := avail - wStatus - wActivity - wRuntime - wVersion - wTokens - wAge
	if rest < 16 {
		rest = 16
	}

	wName := max(8, rest*45/100)
	wContainer := max(8, rest-wName)

	return []int{wName, wStatus, wActivity, wRuntime, wVersion, wTokens, wContainer, wAge}
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
