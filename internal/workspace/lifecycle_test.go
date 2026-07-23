package workspace

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

func TestOpenCodeServeCommand(t *testing.T) {
	got := openCodeServeCommand()
	want := []string{runtime.EntrypointPath}
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

func TestExtraCACertificateMounts(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first-ca.crt")
	second := filepath.Join(dir, "second-ca.crt")
	writeTestFile(t, first, testCACertificate(t))
	writeTestFile(t, second, testCACertificate(t))

	mounts, fingerprint, err := extraCACertificateMounts([]string{first, second})
	if err != nil {
		t.Fatalf("extraCACertificateMounts returned error: %v", err)
	}
	want := []runtime.Mount{
		{Source: first, Target: "/run/opencode-manager-extra-ca-0.crt", ReadOnly: true},
		{Source: second, Target: "/run/opencode-manager-extra-ca-1.crt", ReadOnly: true},
	}
	if !reflect.DeepEqual(mounts, want) {
		t.Fatalf("mounts = %#v, want %#v", mounts, want)
	}
	if fingerprint == "" {
		t.Fatal("fingerprint should not be empty")
	}
}

func TestExtraCACertificateMountsAreOptional(t *testing.T) {
	mounts, fingerprint, err := extraCACertificateMounts(nil)
	if err != nil {
		t.Fatalf("extraCACertificateMounts returned error: %v", err)
	}
	if mounts != nil || fingerprint != "" {
		t.Fatalf("extraCACertificateMounts = (%#v, %q), want (nil, empty)", mounts, fingerprint)
	}
}

func TestExtraCACertificateMountRejectsMalformedCertificate(t *testing.T) {
	certificate := filepath.Join(t.TempDir(), "company-ca.crt")
	writeTestFile(t, certificate, []byte("not a certificate\n"))

	if _, _, err := extraCACertificateMounts([]string{certificate}); err == nil {
		t.Fatal("extraCACertificateMounts should reject a malformed certificate")
	}
}

type startupStatusDriver struct {
	*fakeDriver
	statuses []string
}

func (d *startupStatusDriver) ContainerStatus(context.Context, string) (string, error) {
	status := d.statuses[0]
	if len(d.statuses) > 1 {
		d.statuses = d.statuses[1:]
	}
	return status, nil
}

func TestVerifyStartedRejectsContainerThatExitsDuringStartup(t *testing.T) {
	driver := &startupStatusDriver{fakeDriver: &fakeDriver{}, statuses: []string{runtime.StatusRunning, runtime.StatusExited}}
	l := Lifecycle{driver: driver}

	if err := l.verifyStarted(context.Background(), "demo"); err == nil {
		t.Fatal("verifyStarted should reject a container that exits during startup")
	}
}

func testCACertificate(t *testing.T) []byte {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             now,
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})
}
