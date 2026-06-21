package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

type Lifecycle struct {
	cfg      config.Config
	registry Registry
	driver   runtime.Driver
}

type Status struct {
	Workspace Summary
	Container string
	Error     string
}

type AttachResultMsg struct {
	Err error
}

func NewLifecycle(cfg config.Config) (Lifecycle, error) {
	driver, err := runtime.NewDriver(cfg.Runtime)
	if err != nil {
		return Lifecycle{}, err
	}

	return Lifecycle{cfg: cfg, registry: NewRegistry(cfg), driver: driver}, nil
}

func (l Lifecycle) EnsureBaseImage(ctx context.Context) error {
	if err := l.driver.Available(ctx); err != nil {
		return err
	}

	image := imageConfigFromConfig(l.cfg)
	baseImageName, err := managedBaseImageName(image)
	if err != nil {
		return err
	}

	return l.driver.BuildBaseImage(ctx, runtime.BaseBuildSpec{
		ImageName: baseImageName,
		FromImage: image.BaseImage,
		Packages:  image.Packages,
		Commands:  image.Commands,
	})
}

func (l Lifecycle) Statuses(ctx context.Context, workspaces []Summary) []Status {
	statuses := make([]Status, 0, len(workspaces))
	for _, ws := range workspaces {
		containerStatus, err := l.driver.ContainerStatus(ctx, ws.Manifest.ContainerName)
		status := Status{Workspace: ws, Container: containerStatus}
		if err != nil {
			status.Error = err.Error()
		}
		statuses = append(statuses, status)
	}

	return statuses
}

func (l Lifecycle) EnsureStarted(ctx context.Context, summary Summary) error {
	status, spec, err := l.provision(ctx, summary)
	if err != nil {
		return err
	}

	if status != runtime.StatusRunning {
		if err := l.driver.StartContainer(ctx, summary.Manifest.ContainerName); err != nil {
			if recreateErr := l.recreateAndStart(ctx, summary, spec); recreateErr != nil {
				return fmt.Errorf("start failed: %w; recreate failed: %v", err, recreateErr)
			}
		}
	}

	return nil
}

func (l Lifecycle) Provision(ctx context.Context, summary Summary) (string, error) {
	status, _, err := l.provision(ctx, summary)
	return status, err
}

func (l Lifecycle) provision(ctx context.Context, summary Summary) (string, runtime.ContainerSpec, error) {
	if err := l.driver.Available(ctx); err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	uid, gid, err := currentUserIDs()
	if err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	manifest := summary.Manifest
	baseImageName, err := managedBaseImageName(manifest.Image)
	if err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}
	if err := l.driver.BuildBaseImage(ctx, runtime.BaseBuildSpec{
		ImageName: baseImageName,
		FromImage: manifest.Image.BaseImage,
		Packages:  manifest.Image.Packages,
		Commands:  manifest.Image.Commands,
	}); err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	if err := l.driver.BuildImage(ctx, runtime.BuildSpec{
		ImageName: manifest.ImageName,
		BaseImage: baseImageName,
		UID:       uid,
		GID:       gid,
	}); err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	spec := runtime.ContainerSpec{
		Name:      manifest.ContainerName,
		ImageName: manifest.ImageName,
		HomeDir:   manifest.HomeDir,
		UID:       uid,
		GID:       gid,
		Env:       manifest.Env,
		Command:   interactiveOpenCodeCommand(),
	}

	status, err := l.driver.ContainerStatus(ctx, manifest.ContainerName)
	if err != nil {
		return runtime.StatusUnknown, runtime.ContainerSpec{}, err
	}

	if status == runtime.StatusMissing {
		if err := l.driver.CreateContainer(ctx, spec); err != nil {
			return runtime.StatusUnknown, runtime.ContainerSpec{}, err
		}
		status = runtime.StatusCreated
	}

	return status, spec, nil
}

func (l Lifecycle) recreateAndStart(ctx context.Context, summary Summary, spec runtime.ContainerSpec) error {
	if err := l.driver.RemoveContainer(ctx, summary.Manifest.ContainerName); err != nil {
		return err
	}
	if err := l.driver.CreateContainer(ctx, spec); err != nil {
		return err
	}
	return l.driver.StartContainer(ctx, summary.Manifest.ContainerName)
}

func (l Lifecycle) Stop(ctx context.Context, summary Summary) error {
	status, err := l.driver.ContainerStatus(ctx, summary.Manifest.ContainerName)
	if err != nil {
		return err
	}
	if status == runtime.StatusMissing {
		return fmt.Errorf("container %s does not exist", summary.Manifest.ContainerName)
	}
	if status != runtime.StatusRunning {
		return nil
	}

	return l.driver.StopContainer(ctx, summary.Manifest.ContainerName)
}

func (l Lifecycle) Delete(ctx context.Context, summary Summary) error {
	if err := l.driver.RemoveContainer(ctx, summary.Manifest.ContainerName); err != nil {
		return err
	}
	if err := l.driver.RemoveImage(ctx, summary.Manifest.ImageName); err != nil {
		return err
	}
	if err := l.registry.Delete(summary); err != nil {
		return err
	}

	return nil
}

func (l Lifecycle) AttachCommand(ctx context.Context, summary Summary) (*exec.Cmd, error) {
	status, err := l.driver.ContainerStatus(ctx, summary.Manifest.ContainerName)
	if err != nil {
		return nil, err
	}
	if shouldStartForAttach(status) {
		if err := l.EnsureStarted(ctx, summary); err != nil {
			return nil, err
		}
	}

	return l.driver.AttachCommand(summary.Manifest.ContainerName), nil
}

func (l Lifecycle) Attach(ctx context.Context, summary Summary) (tea.Cmd, error) {
	cmd, err := l.AttachCommand(ctx, summary)
	if err != nil {
		return nil, err
	}

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return AttachResultMsg{Err: err}
	}), nil
}

func shouldStartForAttach(status string) bool {
	return status != runtime.StatusRunning
}

func interactiveOpenCodeCommand() []string {
	return []string{"/usr/local/bin/opencode-manager-entrypoint"}
}

func imageConfigFromConfig(cfg config.Config) ImageConfig {
	return ImageConfig{
		BaseImage: cfg.BaseImage.Name,
		Packages:  append([]string(nil), cfg.BaseImage.Packages...),
		Commands:  append([]string(nil), cfg.BaseImage.Commands...),
	}
}

func managedBaseImageName(image ImageConfig) (string, error) {
	data, err := json.Marshal(image)
	if err != nil {
		return "", fmt.Errorf("encode base image definition: %w", err)
	}

	sum := sha256.Sum256(data)
	return "opencode-manager/base:" + hex.EncodeToString(sum[:])[:16], nil
}

func currentUserIDs() (int, int, error) {
	uid := os.Getuid()
	gid := os.Getgid()
	if uid <= 0 || gid <= 0 {
		return 0, 0, fmt.Errorf("current uid/gid must be positive, got %d:%d", uid, gid)
	}

	return uid, gid, nil
}

func lifecycleContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}
