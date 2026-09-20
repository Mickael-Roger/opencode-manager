package workspace

import (
	"context"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/agent"
	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

func TestUpdateWorkspaceImageRefreshesBaseAndRecreatesContainer(t *testing.T) {
	driver := &updateDriver{fakeDriver: &fakeDriver{}}
	home := t.TempDir()
	summary := Summary{Manifest: Manifest{
		Name:          "demo",
		ImageName:     "ocm/demo:latest",
		Image:         ImageConfig{BaseImage: config.DefaultBaseImage},
		ContainerName: "demo",
		HomeDir:       home,
		OpenCodePort:  4096,
	}}
	l := Lifecycle{cfg: config.Config{Runtime: config.RuntimeDocker}, driver: driver, agents: agent.NewRegistry()}

	if err := l.UpdateWorkspaceImage(context.Background(), summary); err != nil {
		t.Fatalf("UpdateWorkspaceImage error: %v", err)
	}
	if len(driver.pulled) != 1 || driver.pulled[0] != config.DefaultBaseImage {
		t.Fatalf("pulls = %v, want forced pull of %s", driver.pulled, config.DefaultBaseImage)
	}
	if len(driver.builds) != 1 || driver.builds[0].BaseImage != config.DefaultBaseImage {
		t.Fatalf("workspace builds = %#v", driver.builds)
	}
	if driver.removed != 1 || driver.created != 1 || driver.started != 1 {
		t.Fatalf("replacement = remove:%d create:%d start:%d, want 1 each", driver.removed, driver.created, driver.started)
	}
}

type updateDriver struct {
	*fakeDriver
	pulled  []string
	builds  []runtime.BuildSpec
	removed int
	created int
}

func (d *updateDriver) PullImage(_ context.Context, ref string) error {
	d.pulled = append(d.pulled, ref)
	return nil
}

func (d *updateDriver) BuildImage(_ context.Context, spec runtime.BuildSpec) error {
	d.builds = append(d.builds, spec)
	return nil
}

func (d *updateDriver) ContainerStatus(context.Context, string) (string, error) {
	return runtime.StatusRunning, nil
}

func (d *updateDriver) ContainerImageID(context.Context, string) (string, error) { return "old", nil }
func (d *updateDriver) ImageID(context.Context, string) (string, error)          { return "new", nil }
func (d *updateDriver) RemoveContainer(context.Context, string) error {
	d.removed++
	return nil
}
func (d *updateDriver) CreateContainer(context.Context, runtime.ContainerSpec) error {
	d.created++
	return nil
}
