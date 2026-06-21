package workspace

import (
	"reflect"
	"testing"
)

func TestShouldStartForAttach(t *testing.T) {
	if shouldStartForAttach("running") {
		t.Fatal("running container should not be started again before attaching")
	}
	if !shouldStartForAttach("missing") {
		t.Fatal("missing container should be started before attaching")
	}
}

func TestInteractiveOpenCodeCommand(t *testing.T) {
	got := interactiveOpenCodeCommand()
	want := []string{"/usr/local/bin/opencode-manager-entrypoint"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interactiveOpenCodeCommand = %#v, want %#v", got, want)
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
