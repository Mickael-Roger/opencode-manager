package workspace

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

// recordingDriver records pull/build calls and reports image presence, so the
// base-image resolution logic can be tested without a real runtime.
type recordingDriver struct {
	*fakeDriver
	present bool
	pulled  []string
	builds  []runtime.BaseBuildSpec
}

func (d *recordingDriver) ImageID(_ context.Context, _ string) (string, error) {
	if d.present {
		return "sha256:present", nil
	}
	return "", nil
}

func (d *recordingDriver) PullImage(_ context.Context, ref string) error {
	d.pulled = append(d.pulled, ref)
	d.present = true
	return nil
}

func (d *recordingDriver) BuildBaseImage(_ context.Context, spec runtime.BaseBuildSpec) error {
	d.builds = append(d.builds, spec)
	return nil
}

func newTestLifecycle(driver runtime.Driver) Lifecycle {
	cfg := config.Config{Runtime: config.RuntimeDocker}
	return Lifecycle{cfg: cfg, registry: NewRegistry(cfg), driver: driver}
}

func TestResolveBaseImageDefaultNoExtrasPullsAndUsesDirectly(t *testing.T) {
	rec := &recordingDriver{fakeDriver: &fakeDriver{}}
	l := newTestLifecycle(rec)

	ref, err := l.resolveBaseImage(context.Background(), ImageConfig{BaseImage: config.DefaultBaseImage})
	if err != nil {
		t.Fatalf("resolveBaseImage error: %v", err)
	}
	if ref != config.DefaultBaseImage {
		t.Fatalf("ref = %q, want the published base used directly", ref)
	}
	if len(rec.pulled) != 1 || rec.pulled[0] != config.DefaultBaseImage {
		t.Fatalf("expected one pull of the published base, got %v", rec.pulled)
	}
	if len(rec.builds) != 0 {
		t.Fatalf("expected no local build for the default base, got %v", rec.builds)
	}
}

func TestResolveBaseImageNonLatestTagIsPrebuilt(t *testing.T) {
	// A non-default tag of the published base (e.g. :dev) must still be treated as
	// prebuilt: pulled and used directly, never rebuilt with tool installs.
	rec := &recordingDriver{fakeDriver: &fakeDriver{}}
	l := newTestLifecycle(rec)

	ref, err := l.resolveBaseImage(context.Background(), ImageConfig{BaseImage: "docker.io/mroger78/ocm-base:dev"})
	if err != nil {
		t.Fatalf("resolveBaseImage error: %v", err)
	}
	if ref != "docker.io/mroger78/ocm-base:dev" {
		t.Fatalf("ref = %q, want the dev base used directly", ref)
	}
	if len(rec.pulled) != 1 {
		t.Fatalf("expected the dev base to be pulled, got %v", rec.pulled)
	}
	if len(rec.builds) != 0 {
		t.Fatalf("expected no local build for a published-base tag, got %v", rec.builds)
	}
}

func TestResolveBaseImagePresentSkipsPull(t *testing.T) {
	rec := &recordingDriver{fakeDriver: &fakeDriver{}, present: true}
	l := newTestLifecycle(rec)

	if _, err := l.resolveBaseImage(context.Background(), ImageConfig{BaseImage: config.DefaultBaseImage}); err != nil {
		t.Fatalf("resolveBaseImage error: %v", err)
	}
	if len(rec.pulled) != 0 {
		t.Fatalf("expected no pull when image already present, got %v", rec.pulled)
	}
}

func TestResolveBaseImageDefaultWithExtrasBuildsOverlay(t *testing.T) {
	rec := &recordingDriver{fakeDriver: &fakeDriver{}, present: true}
	l := newTestLifecycle(rec)

	ref, err := l.resolveBaseImage(context.Background(), ImageConfig{
		BaseImage: config.DefaultBaseImage,
		Packages:  []string{"htop"},
	})
	if err != nil {
		t.Fatalf("resolveBaseImage error: %v", err)
	}
	if ref == config.DefaultBaseImage {
		t.Fatalf("with extras the workspace must build FROM a managed overlay, got the base directly")
	}
	if len(rec.builds) != 1 {
		t.Fatalf("expected one overlay build, got %v", rec.builds)
	}
	if !rec.builds[0].Prebuilt {
		t.Fatalf("overlay build must set Prebuilt=true, got %+v", rec.builds[0])
	}
	if rec.builds[0].FromImage != config.DefaultBaseImage {
		t.Fatalf("overlay must build FROM the published base, got %q", rec.builds[0].FromImage)
	}
}

func TestResolveBaseImageCustomBaseBuildsFullRecipe(t *testing.T) {
	rec := &recordingDriver{fakeDriver: &fakeDriver{}}
	l := newTestLifecycle(rec)

	if _, err := l.resolveBaseImage(context.Background(), ImageConfig{BaseImage: "debian:stable-slim"}); err != nil {
		t.Fatalf("resolveBaseImage error: %v", err)
	}
	if len(rec.pulled) != 0 {
		t.Fatalf("custom base must not pull the published base, got %v", rec.pulled)
	}
	if len(rec.builds) != 1 || rec.builds[0].Prebuilt {
		t.Fatalf("custom base must build the full recipe (Prebuilt=false), got %v", rec.builds)
	}
}

// specRecordingDriver captures the spec passed to CreateContainer and reports the
// container as missing so provision always creates it.
type specRecordingDriver struct {
	*fakeDriver
	created runtime.ContainerSpec
}

func (d *specRecordingDriver) ContainerStatus(context.Context, string) (string, error) {
	return runtime.StatusMissing, nil
}

func (d *specRecordingDriver) CreateContainer(_ context.Context, spec runtime.ContainerSpec) error {
	d.created = spec
	return nil
}

func TestProvisionInjectsPortAndHostNetwork(t *testing.T) {
	rec := &specRecordingDriver{fakeDriver: &fakeDriver{}}
	cfg := config.Config{
		WorkspaceRoot: t.TempDir(),
		Runtime:       config.RuntimeDocker,
		HostNetwork:   true,
		BaseImage:     config.BaseImageConfig{Name: "debian:stable-slim"},
	}
	l := Lifecycle{cfg: cfg, registry: NewRegistry(cfg), driver: rec}

	created, err := l.registry.Create("demo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, spec, err := l.provision(context.Background(), Summary{Manifest: created.Manifest, Path: created.Path})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	if !spec.HostNetwork {
		t.Fatal("spec.HostNetwork = false, want true (config.HostNetwork enabled)")
	}

	want := strconv.Itoa(created.Manifest.OpenCodePort)
	if got := spec.Env[OpenCodePortEnv]; got != want {
		t.Fatalf("spec.Env[%s] = %q, want %q", OpenCodePortEnv, got, want)
	}
	if got := rec.created.Env[OpenCodePortEnv]; got != want {
		t.Fatalf("created container spec env[%s] = %q, want %q", OpenCodePortEnv, got, want)
	}

	// The manifest env must not be polluted with the runtime-only port var.
	if _, ok := created.Manifest.Env[OpenCodePortEnv]; ok {
		t.Fatal("manifest env should not contain the OpenCode port variable")
	}
}

func TestProvisionBackfillsMissingPort(t *testing.T) {
	rec := &specRecordingDriver{fakeDriver: &fakeDriver{}}
	cfg := config.Config{
		WorkspaceRoot: t.TempDir(),
		Runtime:       config.RuntimeDocker,
		BaseImage:     config.BaseImageConfig{Name: "debian:stable-slim"},
	}
	l := Lifecycle{cfg: cfg, registry: NewRegistry(cfg), driver: rec}

	created, err := l.registry.Create("demo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate a pre-port manifest: clear the port and persist it back.
	created.Manifest.OpenCodePort = 0
	if err := SaveManifest(filepath.Join(created.Path, ManifestFile), created.Manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	if _, _, err := l.provision(context.Background(), Summary{Manifest: created.Manifest, Path: created.Path}); err != nil {
		t.Fatalf("provision: %v", err)
	}

	saved, err := LoadManifest(filepath.Join(created.Path, ManifestFile))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if saved.OpenCodePort < OpenCodePortMin || saved.OpenCodePort > OpenCodePortMax {
		t.Fatalf("backfilled port = %d, want within [%d, %d]", saved.OpenCodePort, OpenCodePortMin, OpenCodePortMax)
	}
}
