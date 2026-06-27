package workspace

import (
	"context"
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
