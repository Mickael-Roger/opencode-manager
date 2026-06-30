package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/config"
	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

// driftDriver reports a container that exists and is running, with a
// configurable runtime config, and counts recreate calls.
type driftDriver struct {
	*fakeDriver
	rc      runtime.ContainerRuntimeConfig
	rcErr   error
	removed int
	created int
}

func (d *driftDriver) ContainerStatus(context.Context, string) (string, error) {
	return runtime.StatusRunning, nil
}
func (d *driftDriver) ContainerRuntimeConfig(context.Context, string) (runtime.ContainerRuntimeConfig, error) {
	return d.rc, d.rcErr
}
func (d *driftDriver) RemoveContainer(context.Context, string) error { d.removed++; return nil }
func (d *driftDriver) CreateContainer(context.Context, runtime.ContainerSpec) error {
	d.created++
	return nil
}

func TestContainerSpecDrift(t *testing.T) {
	manifest := Manifest{ContainerName: "c", OpenCodePort: 4097}
	cases := []struct {
		name  string
		rc    runtime.ContainerRuntimeConfig
		rcErr error
		spec  runtime.ContainerSpec
		want  bool
	}{
		{
			name: "matches host and port",
			rc:   runtime.ContainerRuntimeConfig{NetworkMode: "host", Env: map[string]string{OpenCodePortEnv: "4097"}},
			spec: runtime.ContainerSpec{HostNetwork: true},
			want: false,
		},
		{
			name: "network namespace changed",
			rc:   runtime.ContainerRuntimeConfig{NetworkMode: "bridge", Env: map[string]string{OpenCodePortEnv: "4097"}},
			spec: runtime.ContainerSpec{HostNetwork: true},
			want: true,
		},
		{
			name: "port env missing (legacy container)",
			rc:   runtime.ContainerRuntimeConfig{NetworkMode: "bridge", Env: map[string]string{}},
			spec: runtime.ContainerSpec{HostNetwork: false},
			want: true,
		},
		{
			name: "port env stale",
			rc:   runtime.ContainerRuntimeConfig{NetworkMode: "bridge", Env: map[string]string{OpenCodePortEnv: "4096"}},
			spec: runtime.ContainerSpec{HostNetwork: false},
			want: true,
		},
		{
			name:  "inspect error does not churn",
			rcErr: errors.New("boom"),
			spec:  runtime.ContainerSpec{HostNetwork: true},
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &driftDriver{fakeDriver: &fakeDriver{}, rc: tc.rc, rcErr: tc.rcErr}
			l := Lifecycle{driver: d}
			if got := l.containerSpecDrift(context.Background(), manifest, tc.spec); got != tc.want {
				t.Fatalf("containerSpecDrift = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProvisionRecreatesOnNetworkDrift(t *testing.T) {
	// A bridge container while config now wants host networking must be recreated
	// so it picks up the host namespace and its assigned port.
	d := &driftDriver{fakeDriver: &fakeDriver{}, rc: runtime.ContainerRuntimeConfig{NetworkMode: "bridge", Env: map[string]string{}}}
	cfg := config.Config{
		WorkspaceRoot: t.TempDir(),
		Runtime:       config.RuntimeDocker,
		HostNetwork:   true,
		BaseImage:     config.BaseImageConfig{Name: "debian:stable-slim"},
	}
	l := Lifecycle{cfg: cfg, registry: NewRegistry(cfg), driver: d}

	created, err := l.registry.Create("demo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	status, _, err := l.provision(context.Background(), Summary{Manifest: created.Manifest, Path: created.Path})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if d.removed != 1 {
		t.Fatalf("expected the drifted container to be removed once, got %d", d.removed)
	}
	if d.created != 1 {
		t.Fatalf("expected the container to be recreated once, got %d", d.created)
	}
	if status != runtime.StatusCreated {
		t.Fatalf("status = %q, want %q after recreate", status, runtime.StatusCreated)
	}
}
