package workspace

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

// fakeDriver implements runtime.Driver, returning canned tokscale JSON.
type fakeDriver struct {
	gotArgs [][]string
	output  func(args []string) []byte
	stopped int
	started int
}

func (f *fakeDriver) Name() string                                                { return "fake" }
func (f *fakeDriver) Available(context.Context) error                             { return nil }
func (f *fakeDriver) PullImage(context.Context, string) error                     { return nil }
func (f *fakeDriver) BuildBaseImage(context.Context, runtime.BaseBuildSpec) error { return nil }
func (f *fakeDriver) BuildImage(context.Context, runtime.BuildSpec) error         { return nil }
func (f *fakeDriver) ContainerStatus(context.Context, string) (string, error) {
	return runtime.StatusRunning, nil
}
func (f *fakeDriver) ContainerImageID(context.Context, string) (string, error)     { return "", nil }
func (f *fakeDriver) ImageID(context.Context, string) (string, error)              { return "", nil }
func (f *fakeDriver) CreateContainer(context.Context, runtime.ContainerSpec) error { return nil }
func (f *fakeDriver) StartContainer(context.Context, string) error                 { f.started++; return nil }
func (f *fakeDriver) StopContainer(context.Context, string) error                  { f.stopped++; return nil }
func (f *fakeDriver) RemoveContainer(context.Context, string) error                { return nil }
func (f *fakeDriver) RemoveImage(context.Context, string) error                    { return nil }
func (f *fakeDriver) ExecCommand(string, []string) *exec.Cmd                       { return nil }
func (f *fakeDriver) ExecOutput(_ context.Context, _ string, args []string) ([]byte, error) {
	f.gotArgs = append(f.gotArgs, args)
	return f.output(args), nil
}
func (f *fakeDriver) ExecOutputAs(ctx context.Context, name, _ string, args []string) ([]byte, error) {
	return f.ExecOutput(ctx, name, args)
}
func (f *fakeDriver) Exec(ctx context.Context, spec runtime.ExecSpec) ([]byte, error) {
	return f.ExecOutput(ctx, spec.Container, spec.Args)
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestTokenUsageAggregates(t *testing.T) {
	fake := &fakeDriver{output: func(args []string) []byte {
		if contains(args, "--today") {
			return []byte(`{"entries":[{"input":100,"output":50,"cacheRead":10,"cacheWrite":0,"reasoning":5,"messageCount":3,"cost":0.01}]}`)
		}
		return []byte(`{"entries":[
			{"input":1000,"output":500,"cacheRead":100,"cacheWrite":20,"reasoning":50,"messageCount":12,"cost":0.25},
			{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"reasoning":0,"messageCount":1,"cost":0.001}]}`)
	}}

	l := Lifecycle{driver: fake}
	usage, err := l.TokenUsage(context.Background(), Summary{Manifest: Manifest{ContainerName: "c", HomeDir: "/h"}})
	if err != nil {
		t.Fatalf("TokenUsage error: %v", err)
	}

	if usage.TotalTokens != 1685 {
		t.Errorf("TotalTokens=%d want 1685", usage.TotalTokens)
	}
	if usage.TotalMsgs != 13 {
		t.Errorf("TotalMsgs=%d want 13", usage.TotalMsgs)
	}
	if usage.TotalInput != 1010 {
		t.Errorf("TotalInput=%d want 1010", usage.TotalInput)
	}
	if usage.TotalOutput != 505 {
		t.Errorf("TotalOutput=%d want 505", usage.TotalOutput)
	}
	if usage.TodayTokens != 165 {
		t.Errorf("TodayTokens=%d want 165", usage.TodayTokens)
	}
	if usage.TodayMsgs != 3 {
		t.Errorf("TodayMsgs=%d want 3", usage.TodayMsgs)
	}

	// Both calls must scope tokscale to opencode JSON output.
	for _, args := range fake.gotArgs {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "tokscale --json --client opencode") {
			t.Errorf("unexpected tokscale args: %q", joined)
		}
	}
	if len(fake.gotArgs) != 2 {
		t.Fatalf("expected 2 tokscale calls, got %d", len(fake.gotArgs))
	}
}
