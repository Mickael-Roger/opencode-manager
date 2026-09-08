package agent

import "fmt"

const (
	OpenCode = "opencode"
	DeepSeek = "deepseek"
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
	return []Runtime{openCodeRuntime{}, deepSeekRuntime{}}
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
