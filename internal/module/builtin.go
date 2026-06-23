package module

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// builtinFS holds the module definitions shipped with opencode-manager. They are
// extracted to a host directory at startup (SeedBuiltins) so they can be
// bind-mounted into workspace containers like any user-authored module.
//
//go:embed all:builtin
var builtinFS embed.FS

const builtinRoot = "builtin"

// SeedBuiltins extracts the embedded built-in modules into dest, overwriting
// existing copies so the shipped version is always current (the manager owns and
// versions them). User-authored modules in dest live in differently named
// subdirectories and are left untouched.
func SeedBuiltins(dest string) error {
	return fs.WalkDir(builtinFS, builtinRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(builtinRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, err := builtinFS.ReadFile(path)
		if err != nil {
			return err
		}

		mode := os.FileMode(0o644)
		if base := filepath.Base(rel); base == InstallScript || base == UninstallScript {
			mode = 0o755
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return fmt.Errorf("write built-in module file %q: %w", target, err)
		}
		// WriteFile applies the umask on create and leaves an existing file's mode
		// untouched, so force the exec bit explicitly.
		if err := os.Chmod(target, mode); err != nil {
			return fmt.Errorf("set mode on built-in module file %q: %w", target, err)
		}
		return nil
	})
}
