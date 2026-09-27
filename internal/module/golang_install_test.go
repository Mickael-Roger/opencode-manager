package module

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the actual shell script against real archives/checksums, redirecting
// only its installation prefix and HTTP/sudo commands into an unprivileged
// sandbox. These tests must never download Go or touch the machine's toolchain.
func TestGolangInstallPreservesToolchain(t *testing.T) {
	for _, tool := range []string{"sh", "jq", "sha256sum", "tar", "flock"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	source, err := os.ReadFile("../../modules/language/golang/install")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		mode     string
		wantOK   bool
		attempts string
	}{
		{"valid archive", "valid", true, "1"},
		{"corrupt then valid", "recover", true, "2"},
		{"repeated corruption", "corrupt", false, "3"},
		{"network failure", "network", false, "1"},
		{"invalid archive with matching checksum", "invalid", false, "1"},
		{"replacement failure rolls back", "replace", false, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			prefix := filepath.Join(root, "local")
			bin := filepath.Join(root, "bin")
			home := filepath.Join(root, "home")
			tmp := filepath.Join(root, "tmp")
			for _, dir := range []string{bin, home, tmp, filepath.Join(prefix, "go")} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, text string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(prefix, "go", "old"), "working toolchain")
			write(filepath.Join(root, "install"), strings.ReplaceAll(string(source), "/usr/local", prefix))
			write(filepath.Join(bin, "dpkg"), "#!/bin/sh\necho amd64\n")
			write(filepath.Join(bin, "sleep"), "#!/bin/sh\nexit 0\n")
			write(filepath.Join(bin, "sudo"), `#!/bin/sh
if [ "$MODE" = replace ] && [ "$1" = mv ] && [ "$3" = "$PREFIX/go" ]; then
  case "$2" in */previous) ;; *) exit 1 ;; esac
fi
exec "$@"
`)
			write(filepath.Join(bin, "curl"), `#!/bin/sh
set -eu
url=
out=
while [ "$#" -gt 0 ]; do
  case "$1" in
    https:*) url="$1" ;;
    -o) shift; out="$1" ;;
  esac
  shift
done
case "$url" in
  *mode=json*) cp "$FIXTURE/index.json" "$out" ;;
  *)
    n=0
    if [ -f "$FIXTURE/count" ]; then n=$(cat "$FIXTURE/count"); fi
    n=$((n + 1))
    printf '%s' "$n" > "$FIXTURE/count"
    printf '%s\n' "$out" >> "$FIXTURE/downloads"
    [ "$MODE" != network ] || exit 56
    if [ "$MODE" = corrupt ] || { [ "$MODE" = recover ] && [ "$n" -eq 1 ]; }; then
      printf 'corrupt transfer' > "$out"
    else
      cp "$FIXTURE/archive" "$out"
    fi
    ;;
esac
`)
			archive := goTestArchive(t)
			if tc.mode == "invalid" {
				archive = []byte("not a gzip archive")
			}
			if err := os.WriteFile(filepath.Join(root, "archive"), archive, 0o600); err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(root, "index.json"), fmt.Sprintf(`[{"version":"go1.25.0","stable":true,"files":[{"filename":"go1.25.0.linux-amd64.tar.gz","version":"go1.25.0","os":"linux","arch":"amd64","kind":"archive","sha256":"%x"}]}]`, sha256.Sum256(archive)))
			cmd := exec.Command("sh", filepath.Join(root, "install"))
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "HOME="+home, "TMPDIR="+tmp,
				"FIXTURE="+root, "PREFIX="+prefix, "MODE="+tc.mode, "OCM_VERSION=1.25")
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantOK {
				t.Fatalf("install error = %v, want success %v:\n%s", err, tc.wantOK, out)
			}
			old, oldErr := os.ReadFile(filepath.Join(prefix, "go", "old"))
			if tc.wantOK {
				if !os.IsNotExist(oldErr) {
					t.Fatal("old toolchain not replaced")
				}
				if _, err := os.Stat(filepath.Join(prefix, "go", "bin", "go")); err != nil {
					t.Fatal(err)
				}
			} else if oldErr != nil || string(old) != "working toolchain" {
				t.Fatalf("failed install damaged existing toolchain: %q, %v", old, oldErr)
			}
			count, _ := os.ReadFile(filepath.Join(root, "count"))
			if string(count) != tc.attempts {
				t.Fatalf("download attempts = %s, want %s", count, tc.attempts)
			}
			downloads, _ := os.ReadFile(filepath.Join(root, "downloads"))
			for _, path := range strings.Fields(string(downloads)) {
				if !strings.HasPrefix(path, tmp+"/") || filepath.Dir(path) == tmp {
					t.Fatalf("download outside private temporary directory: %s", path)
				}
			}
			entries, _ := os.ReadDir(tmp)
			stages, _ := filepath.Glob(filepath.Join(prefix, ".ocm-go.*"))
			if len(entries) != 0 || len(stages) != 0 {
				t.Fatalf("temporary files leaked: %v %v", entries, stages)
			}
		})
	}
}

func goTestArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, dir := range []string{"go/", "go/bin/"} {
		if err := tw.WriteHeader(&tar.Header{Name: dir, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
			t.Fatal(err)
		}
	}
	body := "#!/bin/sh\nexit 0\n"
	if err := tw.WriteHeader(&tar.Header{Name: "go/bin/go", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
