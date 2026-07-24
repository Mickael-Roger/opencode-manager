package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadUsesDefaultsWhenConfigDoesNotExist(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Runtime != RuntimeDocker {
		t.Fatalf("Runtime = %q, want %q", cfg.Runtime, RuntimeDocker)
	}

	if cfg.BaseImage.Name == "" {
		t.Fatal("BaseImage should have a default value")
	}

	if cfg.UseLocalOpenCodeAuth {
		t.Fatal("UseLocalOpenCodeAuth should default to false")
	}
	if len(cfg.ExtraCACertificates) != 0 {
		t.Fatalf("ExtraCACertificates = %#v, want empty by default", cfg.ExtraCACertificates)
	}

	if cfg.LogLevel != LogLevelWarning {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, LogLevelWarning)
	}

	if cfg.HostNetwork {
		t.Fatal("HostNetwork should default to false")
	}
}

func TestLoadParsesHostNetwork(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("hostNetwork: true\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.HostNetwork {
		t.Fatal("HostNetwork = false, want true")
	}
}

func TestLoadParsesExtraCACertificate(t *testing.T) {
	dir := t.TempDir()
	certificate := filepath.Join(dir, "company-ca.crt")
	writeFile(t, certificate, testCACertificate(t))
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("extraCACertificate: "+certificate+"\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.ExtraCACertificates) != 1 || cfg.ExtraCACertificates[0] != certificate {
		t.Fatalf("ExtraCACertificates = %#v, want [%q]", cfg.ExtraCACertificates, certificate)
	}
}

func TestLoadAcceptsEmptyLegacyExtraCACertificate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, []byte("extraCACertificate: \"\"\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.ExtraCACertificates) != 0 {
		t.Fatalf("ExtraCACertificates = %#v, want empty", cfg.ExtraCACertificates)
	}
}

func TestLoadParsesExtraCACertificateList(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first-ca.crt")
	second := filepath.Join(dir, "second-ca.crt")
	writeFile(t, first, testCACertificate(t))
	writeFile(t, second, testCACertificate(t))
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("extraCACertificate:\n  - "+first+"\n  - "+second+"\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	want := []string{first, second}
	if !reflect.DeepEqual([]string(cfg.ExtraCACertificates), want) {
		t.Fatalf("ExtraCACertificates = %#v, want %#v", cfg.ExtraCACertificates, want)
	}
}

func TestLoadRejectsInvalidExtraCACertificate(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name  string
		value string
	}{
		{name: "relative path", value: "company-ca.crt"},
		{name: "missing file", value: filepath.Join(dir, "missing.crt")},
		{name: "directory", value: dir},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			writeFile(t, path, []byte("extraCACertificate: "+tc.value+"\n"))

			if _, err := Load(path); err == nil {
				t.Fatal("Load should reject invalid extraCACertificate")
			}
		})
	}
}

func TestLoadRejectsMalformedExtraCACertificate(t *testing.T) {
	dir := t.TempDir()
	certificate := filepath.Join(dir, "company-ca.crt")
	writeFile(t, certificate, []byte("not a certificate\n"))
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("extraCACertificate: "+certificate+"\n"))

	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject a malformed extraCACertificate")
	}
}

func TestLoadRejectsNonCAExtraCACertificate(t *testing.T) {
	dir := t.TempDir()
	certificate := filepath.Join(dir, "leaf.crt")
	writeFile(t, certificate, testCertificate(t, false))
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("extraCACertificate: "+certificate+"\n"))

	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject a non-CA extraCACertificate")
	}
}

func TestLoadParsesRuntimeArgs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("runtimeArgs:\n  - --dns\n  - 1.1.1.1\n  - --add-host=db:10.0.0.5\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	want := []string{"--dns", "1.1.1.1", "--add-host=db:10.0.0.5"}
	if len(cfg.RuntimeArgs) != len(want) {
		t.Fatalf("RuntimeArgs = %#v, want %#v", cfg.RuntimeArgs, want)
	}
	for i, w := range want {
		if cfg.RuntimeArgs[i] != w {
			t.Fatalf("RuntimeArgs[%d] = %q, want %q", i, cfg.RuntimeArgs[i], w)
		}
	}
}

func TestLoadRejectsEmptyRuntimeArg(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("runtimeArgs:\n  - --dns\n  - \"\"\n"))

	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject an empty runtimeArgs entry")
	}
}

func TestLoadParsesWorkspaceHookCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte(
		"workspacePostCreateCommands:\n  - git clone git@example.com:me/repo .\n  - npm install\n"+
			"workspacePreDeleteCommands:\n  - git push\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.WorkspacePostCreateCommands) != 2 || cfg.WorkspacePostCreateCommands[1] != "npm install" {
		t.Fatalf("WorkspacePostCreateCommands = %#v", cfg.WorkspacePostCreateCommands)
	}
	if len(cfg.WorkspacePreDeleteCommands) != 1 || cfg.WorkspacePreDeleteCommands[0] != "git push" {
		t.Fatalf("WorkspacePreDeleteCommands = %#v", cfg.WorkspacePreDeleteCommands)
	}
}

func TestLoadResolvesWorkspaceEnv(t *testing.T) {
	t.Setenv("LOCAL_TOKEN", "host-secret")
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, path, []byte("workspaceEnv:\n  LOG_LEVEL: debug\n  API_TOKEN: '{env:LOCAL_TOKEN}'\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	resolved, err := cfg.ResolveWorkspaceEnv()
	if err != nil {
		t.Fatalf("ResolveWorkspaceEnv returned error: %v", err)
	}
	if !reflect.DeepEqual(resolved, map[string]string{"LOG_LEVEL": "debug", "API_TOKEN": "host-secret"}) {
		t.Fatalf("resolved workspace env = %#v", resolved)
	}
	if got := cfg.WorkspaceEnvKeys(); got != "API_TOKEN,LOG_LEVEL" {
		t.Fatalf("WorkspaceEnvKeys = %q, want API_TOKEN,LOG_LEVEL", got)
	}
}

func TestLoadRejectsInvalidWorkspaceEnv(t *testing.T) {
	for _, content := range []string{
		"workspaceEnv:\n  OCM_OPENCODE_PORT: value\n",
		"workspaceEnv:\n  INVALID-NAME: value\n",
		"workspaceEnv:\n  TOKEN: '{env:INVALID-NAME}'\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		writeFile(t, path, []byte(content))
		if _, err := Load(path); err == nil {
			t.Fatalf("Load should reject workspaceEnv config %q", content)
		}
	}
}

func TestResolveWorkspaceEnvRequiresHostVariable(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default returned error: %v", err)
	}
	cfg.WorkspaceEnv = map[string]string{"TOKEN": "{env:MISSING_TOKEN}"}
	if _, err := cfg.ResolveWorkspaceEnv(); err == nil {
		t.Fatal("ResolveWorkspaceEnv should reject a missing host variable")
	}
}

func TestLoadRejectsEmptyWorkspaceHookCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("workspacePreDeleteCommands:\n  - git push\n  - \"   \"\n"))

	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject a blank workspacePreDeleteCommands entry")
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("logLevel: verbose\n"))

	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject an invalid logLevel")
	}
}

func TestLoadAcceptsValidLogLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("logLevel: debug\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.LogLevel != LogLevelDebug {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, LogLevelDebug)
	}
}

func TestLoadMergesConfigWithDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte("runtime: podman\nworkspaceRoot: /tmp/workspaces\nuseLocalOpenCodeAuth: true\nbaseImage:\n  packages:\n    - ripgrep\n  commands:\n    - update-ca-certificates\n"))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Runtime != RuntimePodman {
		t.Fatalf("Runtime = %q, want %q", cfg.Runtime, RuntimePodman)
	}

	if cfg.WorkspaceRoot != "/tmp/workspaces" {
		t.Fatalf("WorkspaceRoot = %q, want /tmp/workspaces", cfg.WorkspaceRoot)
	}
	if !cfg.UseLocalOpenCodeAuth {
		t.Fatal("UseLocalOpenCodeAuth = false, want true")
	}

	if cfg.BaseImage.Name == "" {
		t.Fatal("BaseImage should keep default value")
	}
	if len(cfg.BaseImage.Packages) != 1 || cfg.BaseImage.Packages[0] != "ripgrep" {
		t.Fatalf("BaseImage.Packages = %#v, want ripgrep", cfg.BaseImage.Packages)
	}
	if len(cfg.BaseImage.Commands) != 1 || cfg.BaseImage.Commands[0] != "update-ca-certificates" {
		t.Fatalf("BaseImage.Commands = %#v, want update-ca-certificates", cfg.BaseImage.Commands)
	}
}

func TestEnsureGlobalConfigCreatesTemplates(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	if err := EnsureGlobalConfig(); err != nil {
		t.Fatalf("EnsureGlobalConfig returned error: %v", err)
	}

	dir := filepath.Join(configHome, "opencode-manager", "opencode")

	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("AGENTS.md = %q, want empty", string(agents))
	}

	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatalf("read opencode.json: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("opencode.json must not be empty")
	}
	if !json.Valid(data) {
		t.Fatalf("opencode.json is not valid JSON: %q", string(data))
	}

	for _, name := range GlobalTemplateDirs {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat template dir %q: %v", name, err)
		}
		if !info.IsDir() {
			t.Fatalf("template %q should be a directory", name)
		}
	}
}

// Shared source files remain readable for host-side OpenCode tooling.
func TestEnsureGlobalConfigMakesMountedFilesWorldReadable(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	if err := EnsureGlobalConfig(); err != nil {
		t.Fatalf("EnsureGlobalConfig returned error: %v", err)
	}

	dir := filepath.Join(configHome, "opencode-manager", "opencode")
	for _, name := range []string{"AGENTS.md", "opencode.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm()&0o004 == 0 {
			t.Fatalf("%s mode = %o, want world-readable", name, info.Mode().Perm())
		}
	}

	// An older, 0600 file from before this fix is healed to world-readable on the
	// next ensure (EnsureGlobalConfig runs on every launch).
	stale := filepath.Join(dir, "opencode.json")
	if err := os.Chmod(stale, 0o600); err != nil {
		t.Fatalf("chmod stale: %v", err)
	}
	if err := EnsureGlobalConfig(); err != nil {
		t.Fatalf("EnsureGlobalConfig (re-heal) returned error: %v", err)
	}
	info, err := os.Stat(stale)
	if err != nil {
		t.Fatalf("stat after re-heal: %v", err)
	}
	if info.Mode().Perm()&0o004 == 0 {
		t.Fatalf("opencode.json mode after re-heal = %o, want world-readable", info.Mode().Perm())
	}
}

func TestEnsureGlobalConfigPreservesExistingFiles(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	dir := filepath.Join(configHome, "opencode-manager")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create global dir: %v", err)
	}
	custom := "{\"model\":\"custom/model\"}\n"
	writeFile(t, filepath.Join(dir, "opencode.json"), []byte(custom))

	if err := EnsureGlobalConfig(); err != nil {
		t.Fatalf("EnsureGlobalConfig returned error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("read opencode.json: %v", err)
	}
	if string(got) != custom {
		t.Fatalf("opencode.json = %q, want preserved %q", string(got), custom)
	}
}

func TestEnsureGlobalConfigMigratesLegacyTemplatesWhenSharedDirectoryIsEmpty(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	global := filepath.Join(configHome, "opencode-manager")
	if err := os.MkdirAll(global, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(global, "opencode.json"), []byte("{\"model\":\"legacy\"}"))

	if err := EnsureGlobalConfig(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(global, "opencode", "opencode.json"))
	if err != nil || string(data) != "{\"model\":\"legacy\"}" {
		t.Fatalf("migrated config = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(global, "opencode.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy config remains or stat failed: %v", err)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func testCACertificate(t *testing.T) []byte {
	return testCertificate(t, true)
}

func testCertificate(t *testing.T, isCA bool) []byte {
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
		IsCA:                  isCA,
		BasicConstraintsValid: isCA,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})
}

func TestIsManagedBaseImage(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"docker.io/mroger78/ocm-base:latest", true},
		{"docker.io/mroger78/ocm-base:dev", true},
		{"docker.io/mroger78/ocm-base:0.3.0", true},
		{"docker.io/mroger78/ocm-base", true},
		{"mroger78/ocm-base:dev", true},
		{"docker.io/mroger78/ocm-base@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true},
		{"debian:stable-slim", false},
		{"docker.io/debian:stable-slim", false},
		{"docker.io/mroger78/other:latest", false},
		{"ghcr.io/mroger78/ocm-base:latest", false},
	}
	for _, c := range cases {
		if got := IsManagedBaseImage(c.name); got != c.want {
			t.Errorf("IsManagedBaseImage(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
