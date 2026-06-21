package workspace

import (
	"reflect"
	"testing"
)

func TestOpenCodeServeCommand(t *testing.T) {
	got := openCodeServeCommand()
	want := []string{"opencode", "serve", "--hostname", "127.0.0.1", "--port", "4096"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("openCodeServeCommand = %#v, want %#v", got, want)
	}
}

func TestOpenCodeSessionCommand(t *testing.T) {
	got := openCodeSessionCommand()
	want := []string{"/usr/local/bin/opencode-manager-attach"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("openCodeSessionCommand = %#v, want %#v", got, want)
	}
}

func TestManagedBaseImageNameChangesWithDefinition(t *testing.T) {
	first, err := managedBaseImageName(ImageConfig{BaseImage: "debian:stable-slim", Packages: []string{"jq"}})
	if err != nil {
		t.Fatalf("managedBaseImageName returned error: %v", err)
	}
	second, err := managedBaseImageName(ImageConfig{BaseImage: "debian:stable-slim", Packages: []string{"ripgrep"}})
	if err != nil {
		t.Fatalf("managedBaseImageName returned error: %v", err)
	}
	repeated, err := managedBaseImageName(ImageConfig{BaseImage: "debian:stable-slim", Packages: []string{"jq"}})
	if err != nil {
		t.Fatalf("managedBaseImageName returned error: %v", err)
	}

	if first == second {
		t.Fatalf("base image names should differ when definitions differ: %q", first)
	}
	if first != repeated {
		t.Fatalf("base image name should be stable: %q != %q", first, repeated)
	}
}
