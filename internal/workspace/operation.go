package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// beginWorkspaceOperation serializes mutations across Lifecycle copies and
// separate manager/CLI processes. Lock files live outside the workspace so a
// delete cannot unlink a lock while another process is waiting on its inode.
// Keep the lock through provisioning, module scripts, and marker/manifest writes.
// Internal helpers must not acquire it again (flock is not reentrant).
func (l Lifecycle) beginWorkspaceOperation(ctx context.Context, summary Summary) (Summary, func(), error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return summary, nil, fmt.Errorf("workspace lock directory: %w", err)
	}
	key := sha256.Sum256([]byte(string(l.cfg.Runtime) + "\x00" + summary.Manifest.ContainerName))
	unlock, err := acquireOperationLock(ctx, filepath.Join(cache, "opencode-manager", "locks", fmt.Sprintf("%x.lock", key)))
	if err != nil {
		return summary, nil, fmt.Errorf("lock workspace %q: %w", summary.Manifest.Name, err)
	}

	// A preceding operation may have updated or deleted the workspace while this
	// caller waited. Never recreate it or reconcile using an obsolete snapshot.
	if summary.Path != "" {
		manifest, err := LoadManifest(filepath.Join(summary.Path, ManifestFile))
		if err != nil {
			unlock()
			return summary, nil, err
		}
		summary.Manifest = manifest
	}
	return summary, unlock, nil
}

func acquireOperationLock(ctx context.Context, path string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	// Closing also releases the lock on process exit. Do not remove the file:
	// waiters must continue locking the same inode.
	unlock := func() { _ = f.Close() }
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			unlock()
			return nil, err
		}
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return unlock, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			unlock()
			return nil, err
		}
		select {
		case <-ctx.Done():
			unlock()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
