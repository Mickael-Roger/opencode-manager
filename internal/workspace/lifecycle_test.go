package workspace

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/runtime"
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

func TestOpenCodeMountsExcludesLocalAuthByDefault(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	mounts, err := openCodeMounts(false)
	if err != nil {
		t.Fatalf("openCodeMounts returned error: %v", err)
	}

	for _, mount := range mounts {
		if mount.Target == openCodeHomeDir+"/"+openCodeAuthRelPath {
			t.Fatalf("auth mount should not be present by default: %#v", mounts)
		}
	}
}

func TestOpenCodeMountsIncludesWritableLocalAuth(t *testing.T) {
	configHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", home)
	writeTestFile(t, filepath.Join(home, openCodeAuthRelPath), []byte("{}\n"))

	mounts, err := openCodeMounts(true)
	if err != nil {
		t.Fatalf("openCodeMounts returned error: %v", err)
	}

	want := runtime.Mount{
		Source:   filepath.Join(home, openCodeAuthRelPath),
		Target:   openCodeHomeDir + "/" + openCodeAuthRelPath,
		ReadOnly: false,
	}
	for _, mount := range mounts {
		if reflect.DeepEqual(mount, want) {
			return
		}
	}

	t.Fatalf("auth mount not found in %#v", mounts)
}

func TestOpenCodeMountsRequiresLocalAuthFile(t *testing.T) {
	configHome := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", home)

	_, err := openCodeMounts(true)
	if err == nil {
		t.Fatal("openCodeMounts returned nil error, want missing auth file error")
	}

	if !strings.Contains(err.Error(), "useLocalOpenCodeAuth") {
		t.Fatalf("openCodeMounts error = %q, want useLocalOpenCodeAuth context", err.Error())
	}
}
