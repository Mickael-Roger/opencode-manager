package workspace

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/agent"
	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

func TestUpdateWorkspaceImageRefreshesBaseAndRecreatesContainer(t *testing.T) {
	driver := &updateDriver{fakeDriver: &fakeDriver{}}
	path := t.TempDir()
	home := filepath.Join(path, "home")
	summary := Summary{Manifest: Manifest{
		Name:          "demo",
		ImageName:     "ocm/demo:latest",
		Image:         ImageConfig{BaseImage: "docker.io/mroger78/ocm-base:0.7.0"},
		ContainerName: "demo",
		HomeDir:       home,
		OpenCodePort:  4096,
	}, Path: path}
	if err := SaveManifest(filepath.Join(path, ManifestFile), summary.Manifest); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	l := Lifecycle{cfg: config.Config{Runtime: config.RuntimeDocker, BaseImage: config.BaseImageConfig{Name: config.DefaultBaseImage}}, driver: driver, agents: agent.NewRegistry()}

	if err := l.UpdateWorkspaceImage(context.Background(), summary); err != nil {
		t.Fatalf("UpdateWorkspaceImage error: %v", err)
	}
	if len(driver.pulled) != 1 || driver.pulled[0] != config.DefaultBaseImage {
		t.Fatalf("pulls = %v, want forced pull of %s", driver.pulled, config.DefaultBaseImage)
	}
	if len(driver.builds) != 1 || driver.builds[0].BaseImage != config.DefaultBaseImage || !driver.builds[0].Refresh {
		t.Fatalf("workspace builds = %#v", driver.builds)
	}
	if driver.removed != 1 || driver.created != 1 || driver.started != 1 {
		t.Fatalf("replacement = remove:%d create:%d start:%d, want 1 each", driver.removed, driver.created, driver.started)
	}
	updated, err := LoadManifest(filepath.Join(path, ManifestFile))
	if err != nil {
		t.Fatalf("load updated manifest: %v", err)
	}
	if updated.Image.BaseImage != config.DefaultBaseImage {
		t.Fatalf("updated base image = %q, want %q", updated.Image.BaseImage, config.DefaultBaseImage)
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

// Podman can preserve the image ID when the rebuilt tag has the same config. An
// explicit update must still replace the container to pick up the refreshed base.
func (d *updateDriver) ContainerImageID(context.Context, string) (string, error) { return "same", nil }
func (d *updateDriver) ImageID(context.Context, string) (string, error)          { return "same", nil }
func (d *updateDriver) RemoveContainer(context.Context, string) error {
	d.removed++
	return nil
}
func (d *updateDriver) CreateContainer(context.Context, runtime.ContainerSpec) error {
	d.created++
	return nil
}
