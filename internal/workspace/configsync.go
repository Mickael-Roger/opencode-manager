package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mickael-menu/opencode-manager/internal/config"
)

const openCodeSyncJournal = ".ocm-opencode-sync.json"

var (
	openCodeSyncMu              sync.Mutex
	reservedOpenCodeSourceNames = map[string]struct{}{
		".gitignore":          {},
		"package.json":        {},
		"package-lock.json":   {},
		"npm-shrinkwrap.json": {},
		"bun.lock":            {},
		"bun.lockb":           {},
		"pnpm-lock.yaml":      {},
		"yarn.lock":           {},
		"node_modules":        {},
	}
)

type openCodeSyncJournalData struct {
	Entries []string `json:"entries"`
}

// StartOpenCodeConfigSync reconciles existing workspaces immediately and keeps
// their OpenCode configuration current while the manager process is active.
func StartOpenCodeConfigSync(ctx context.Context, cfg config.Config) error {
	syncer := openCodeConfigSyncer{registry: NewRegistry(cfg)}
	if err := syncer.reconcile(); err != nil {
		return err
	}

	source, err := config.OpenCodeDir()
	if err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create shared OpenCode config watcher: %w", err)
	}
	if err := addOpenCodeWatches(watcher, source); err != nil {
		watcher.Close()
		return err
	}

	go syncer.watch(ctx, watcher, source)
	return nil
}

type openCodeConfigSyncer struct {
	registry Registry
}

func (s openCodeConfigSyncer) reconcile() error {
	workspaces, err := s.registry.List()
	if err != nil {
		return fmt.Errorf("list workspaces for shared OpenCode config sync: %w", err)
	}
	for _, workspace := range workspaces {
		if err := syncWorkspaceOpenCodeConfig(workspace.Manifest.HomeDir); err != nil {
			return fmt.Errorf("sync shared OpenCode config to workspace %q: %w", workspace.Manifest.Name, err)
		}
	}
	return nil
}

func (s openCodeConfigSyncer) watch(ctx context.Context, watcher *fsnotify.Watcher, source string) {
	defer watcher.Close()
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) {
				if err := addOpenCodeWatches(watcher, source); err != nil {
					slog.Warn("refresh shared OpenCode config watches", "error", err)
				}
			}
			if timer == nil {
				timer = time.NewTimer(100 * time.Millisecond)
				timerC = timer.C
			} else if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
				timer.Reset(100 * time.Millisecond)
			} else {
				timer.Reset(100 * time.Millisecond)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("shared OpenCode config watcher error", "error", err)
		case <-timerC:
			timer = nil
			timerC = nil
			if err := s.reconcile(); err != nil {
				slog.Warn("synchronize changed shared OpenCode config", "error", err)
			}
		}
	}
}

func addOpenCodeWatches(watcher *fsnotify.Watcher, source string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if err := watcher.Add(path); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			return fmt.Errorf("watch shared OpenCode directory %q: %w", path, err)
		}
		return nil
	})
}

// syncWorkspaceOpenCodeConfig copies every shared source entry one way into a
// workspace and records manager-owned paths. A missing source entry removes only
// a previously journaled path, never arbitrary workspace configuration.
func syncWorkspaceOpenCodeConfig(homeDir string) error {
	openCodeSyncMu.Lock()
	defer openCodeSyncMu.Unlock()

	if err := config.EnsureGlobalConfig(); err != nil {
		return err
	}
	source, err := config.OpenCodeDir()
	if err != nil {
		return err
	}
	entries, err := sourceEntries(source)
	if err != nil {
		return err
	}

	destination := filepath.Join(homeDir, ".config", "opencode")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("create workspace OpenCode directory %q: %w", destination, err)
	}
	journalPath := filepath.Join(homeDir, openCodeSyncJournal)
	previous, err := loadOpenCodeSyncJournal(journalPath)
	if err != nil {
		return err
	}
	sort.Slice(previous, func(i, j int) bool {
		return len(previous[i]) > len(previous[j])
	})
	for _, rel := range previous {
		if _, exists := entries[rel]; exists {
			continue
		}
		if err := removeManagedOpenCodeEntry(destination, rel); err != nil {
			return err
		}
	}

	paths := make([]string, 0, len(entries))
	for rel := range entries {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		if entries[rel] {
			if err := os.MkdirAll(filepath.Join(destination, rel), 0o700); err != nil {
				return fmt.Errorf("create workspace OpenCode directory %q: %w", rel, err)
			}
			continue
		}
		if err := copyOpenCodeFile(filepath.Join(source, rel), filepath.Join(destination, rel)); err != nil {
			return err
		}
	}
	if err := saveOpenCodeSyncJournal(journalPath, paths); err != nil {
		return err
	}
	return EnsureWorkspaceStatusPlugin(destination)
}

// sourceEntries validates the top-level shared source and returns relative paths
// with true for directories and false for regular files.
func sourceEntries(source string) (map[string]bool, error) {
	entries := make(map[string]bool)
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if filepath.Dir(rel) == "." {
			if _, reserved := reservedOpenCodeSourceNames[entry.Name()]; reserved {
				return fmt.Errorf("shared OpenCode config %q cannot contain top-level %q: it is generated per workspace", source, entry.Name())
			}
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("shared OpenCode config entry %q must not be a symbolic link", path)
		}
		if entry.IsDir() {
			entries[rel] = true
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("shared OpenCode config entry %q must be a regular file or directory", path)
		}
		entries[rel] = false
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read shared OpenCode config %q: %w", source, err)
	}
	return entries, nil
}

func copyOpenCodeFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open shared OpenCode file %q: %w", source, err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open workspace OpenCode file %q: %w", destination, err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("copy shared OpenCode file %q: %w", source, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close workspace OpenCode file %q: %w", destination, closeErr)
	}
	return nil
}

func loadOpenCodeSyncJournal(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read OpenCode sync journal %q: %w", path, err)
	}
	var journal openCodeSyncJournalData
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("parse OpenCode sync journal %q: %w", path, err)
	}
	return journal.Entries, nil
}

func saveOpenCodeSyncJournal(path string, entries []string) error {
	data, err := json.Marshal(openCodeSyncJournalData{Entries: entries})
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write OpenCode sync journal %q: %w", temporary, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("save OpenCode sync journal %q: %w", path, err)
	}
	return nil
}

func removeManagedOpenCodeEntry(destination, rel string) error {
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("invalid OpenCode sync journal path %q", rel)
	}
	path := filepath.Join(destination, rel)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) {
		return fmt.Errorf("remove managed workspace OpenCode entry %q: %w", path, err)
	}
	return nil
}
