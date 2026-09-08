package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mickael-menu/opencode-manager/internal/agent"
)

const deepSeekProfilesRelDir = ".config/deepseek/profiles"

const deepSeekPNPMVersion = "11.25.0"

// reconcileDeepSeekProfiles runs DSH's native profile install command for every
// synchronized profile. Profile node_modules and pnpm-lock.yaml remain
// workspace-local; DSH forwards install to pnpm and refreshes each lockfile from
// the centrally managed package.json. Lockfiles are intentionally regenerated:
// pnpm otherwise enables frozen-lockfile mode when a workspace inherits CI=true,
// while OCM deliberately does not synchronize that workspace-local file.
func (l Lifecycle) reconcileDeepSeekProfiles(ctx context.Context, summary Summary) error {
	if !summary.Manifest.RuntimeEnabled(agent.DeepSeek) {
		return nil
	}
	profiles, err := deepSeekProfiles(summary.Manifest.HomeDir)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return nil
	}
	if _, err := l.driver.ExecOutput(ctx, summary.Manifest.ContainerName, []string{"pnpm", "--version"}); err != nil {
		args := []string{"npm", "install", "-g", "pnpm@" + deepSeekPNPMVersion}
		if _, installErr := l.driver.ExecOutputAs(ctx, summary.Manifest.ContainerName, "0", args); installErr != nil {
			return fmt.Errorf("install pnpm for DeepSeek profiles: %w", installErr)
		}
	}
	for _, profile := range profiles {
		args := []string{"dsh", "plugin", "--profile", profile, "install", "--no-frozen-lockfile"}
		if _, err := l.driver.ExecOutput(ctx, summary.Manifest.ContainerName, args); err != nil {
			return fmt.Errorf("install DeepSeek profile %q dependencies: %w", profile, err)
		}
	}
	return nil
}

func deepSeekProfiles(homeDir string) ([]string, error) {
	root := filepath.Join(homeDir, filepath.FromSlash(deepSeekProfilesRelDir))
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read DeepSeek profiles %q: %w", root, err)
	}
	profiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		packagePath := filepath.Join(root, entry.Name(), "package.json")
		info, err := os.Lstat(packagePath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("check DeepSeek profile manifest %q: %w", packagePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("DeepSeek profile manifest %q must be a regular file", packagePath)
		}
		profiles = append(profiles, entry.Name())
	}
	sort.Strings(profiles)
	return profiles, nil
}
