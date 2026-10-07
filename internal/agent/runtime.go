package agent

import "fmt"

const (
	OpenCode = "opencode"
	DeepSeek = "deepseek"
	Claude   = "claude"
)

type Runtime interface {
	Name() string
	DisplayName() string
	AttachCommand() ([]string, error)
	RunCommand(prompt string) ([]string, error)
}

type Registry struct {
	runtimes map[string]Runtime
}

// All returns every known runtime in menu order.
func All() []Runtime {
	return []Runtime{openCodeRuntime{}, deepSeekRuntime{}, claudeRuntime{}}
}

func NewRegistry() Registry {
	runtimes := make(map[string]Runtime)
	for _, provider := range All() {
		runtimes[provider.Name()] = provider
	}
	return Registry{runtimes: runtimes}
}

func (r Registry) Get(name string) (Runtime, error) {
	if r.runtimes == nil {
		return NewRegistry().Get(name)
	}
	provider, ok := r.runtimes[name]
	if !ok {
		return nil, fmt.Errorf("unknown agent runtime %q", name)
	}
	return provider, nil
}

type openCodeRuntime struct{}

func (openCodeRuntime) Name() string        { return OpenCode }
func (openCodeRuntime) DisplayName() string { return "OpenCode" }
func (openCodeRuntime) AttachCommand() ([]string, error) {
	return []string{"/usr/local/bin/opencode-manager-attach"}, nil
}
func (openCodeRuntime) RunCommand(prompt string) ([]string, error) {
	return []string{"opencode", "run", "--dir", "/home/debian/workspace", prompt}, nil
}

type deepSeekRuntime struct{}

func (deepSeekRuntime) Name() string        { return DeepSeek }
func (deepSeekRuntime) DisplayName() string { return "DeepSeek Harness" }
func (deepSeekRuntime) AttachCommand() ([]string, error) {
	return []string{"/usr/local/bin/dsh-tui", "--continue"}, nil
}
func (deepSeekRuntime) RunCommand(string) ([]string, error) {
	return nil, fmt.Errorf("DeepSeek Harness does not provide a non-interactive ACP command")
}

type claudeRuntime struct{}

func (claudeRuntime) Name() string        { return Claude }
func (claudeRuntime) DisplayName() string { return "Claude Code" }
func (claudeRuntime) AttachCommand() ([]string, error) {
	return withWorkspaceEnv("/bin/sh", "-c", claudeSessionScript, "sh", ClaudeSessionSocket, "claude"), nil
}
func (claudeRuntime) RunCommand(prompt string) ([]string, error) {
	return withWorkspaceEnv("claude", "-p", prompt), nil
}

// workspaceEnvScript exports ~/.env, the module-provided environment, then
// execs its arguments. OpenCode and DeepSeek Harness clients talk to servers
// the entrypoint started with it; Claude Code runs the agent's tools itself,
// so it must load the file before starting. argv is passed as "$@", never
// interpolated into the script.
const workspaceEnvScript = `set -a; [ -f "$HOME/.env" ] && . "$HOME/.env"; set +a; exec "$@"`

// ClaudeSessionSocket is the dtach socket holding the workspace's Claude Code
// session inside the container.
const ClaudeSessionSocket = "/tmp/opencode-manager-claude.sock"

// ClaudeDetachKey is the letter that, with Ctrl, detaches from a Claude Code
// session. It must match the `-e` of claudeSessionScript.
const ClaudeDetachKey = 'q'

// claudeSessionScript runs Claude Code under dtach so it outlives the attach
// client: unlike OpenCode, Claude has no server to reconnect to, and it would
// otherwise die with the `exec` when the user leaves. `-A` reattaches to the
// live session, or starts one (clearing a stale socket left by a container
// restart); Ctrl-Q detaches, `-r winch` makes Claude repaint on reattach, and
// `-z` passes Ctrl-Z through. Ctrl-Q is unbound in Claude Code and, unlike
// dtach's default Ctrl-\, typeable on non-US layouts such as AZERTY; Ctrl-C
// stays with Claude to interrupt a turn. Claude switches capable terminals to
// the kitty keyboard protocol, where Ctrl-Q no longer arrives as the byte dtach
// matches, so the manager relays the attach through termproxy to restore it.
// Images without dtach run Claude directly.
const claudeSessionScript = `socket=$1; shift
if command -v dtach >/dev/null 2>&1; then
	exec dtach -A "$socket" -e '^q' -r winch -z "$@"
fi
exec "$@"`

func withWorkspaceEnv(argv ...string) []string {
	return append([]string{"/bin/sh", "-c", workspaceEnvScript, "sh"}, argv...)
}
