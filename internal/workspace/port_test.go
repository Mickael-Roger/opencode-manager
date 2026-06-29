package workspace

import (
	"path/filepath"
	"testing"
)

func TestAllocateOpenCodePortSkipsUsed(t *testing.T) {
	used := map[int]bool{OpenCodePortMin: true}

	port, err := allocateOpenCodePort(used)
	if err != nil {
		t.Fatalf("allocateOpenCodePort returned error: %v", err)
	}
	if port == OpenCodePortMin {
		t.Fatalf("port = %d, want a port other than the used %d", port, OpenCodePortMin)
	}
	if port < OpenCodePortMin || port > OpenCodePortMax {
		t.Fatalf("port = %d, want within [%d, %d]", port, OpenCodePortMin, OpenCodePortMax)
	}
}

func TestNewManifestAssignsUniquePorts(t *testing.T) {
	registry := NewRegistry(testConfig(t))

	first, err := registry.NewManifest("alpha")
	if err != nil {
		t.Fatalf("NewManifest alpha returned error: %v", err)
	}
	if first.OpenCodePort < OpenCodePortMin || first.OpenCodePort > OpenCodePortMax {
		t.Fatalf("alpha port = %d, want within [%d, %d]", first.OpenCodePort, OpenCodePortMin, OpenCodePortMax)
	}
	if err := SaveManifest(filepath.Join(registry.WorkspaceDir("alpha"), ManifestFile), first); err != nil {
		t.Fatalf("SaveManifest alpha returned error: %v", err)
	}

	// With alpha's port recorded, a second workspace must not reuse it.
	second, err := registry.NewManifest("zeta")
	if err != nil {
		t.Fatalf("NewManifest zeta returned error: %v", err)
	}
	if second.OpenCodePort == first.OpenCodePort {
		t.Fatalf("zeta port = %d, want a different port from alpha", second.OpenCodePort)
	}
}
