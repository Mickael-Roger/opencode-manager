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

func TestExtraCACertificateMount(t *testing.T) {
	certificate := filepath.Join(t.TempDir(), "company-ca.crt")
	writeTestFile(t, certificate, testCACertificate(t))

	mount, fingerprint, err := extraCACertificateMount(certificate)
	if err != nil {
		t.Fatalf("extraCACertificateMount returned error: %v", err)
	}
	if mount == nil {
		t.Fatal("extraCACertificateMount returned nil mount")
	}
	want := runtime.Mount{Source: certificate, Target: extraCACertificateContainerPath, ReadOnly: true}
	if !reflect.DeepEqual(*mount, want) {
		t.Fatalf("mount = %#v, want %#v", *mount, want)
	}
	if fingerprint == "" {
		t.Fatal("fingerprint should not be empty")
	}
}

func TestExtraCACertificateMountIsOptional(t *testing.T) {
	mount, fingerprint, err := extraCACertificateMount("")
	if err != nil {
		t.Fatalf("extraCACertificateMount returned error: %v", err)
	}
	if mount != nil || fingerprint != "" {
		t.Fatalf("extraCACertificateMount = (%#v, %q), want (nil, empty)", mount, fingerprint)
	}
}

func TestExtraCACertificateMountRejectsMalformedCertificate(t *testing.T) {
	certificate := filepath.Join(t.TempDir(), "company-ca.crt")
	writeTestFile(t, certificate, []byte("not a certificate\n"))

	if _, _, err := extraCACertificateMount(certificate); err == nil {
		t.Fatal("extraCACertificateMount should reject a malformed certificate")
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
