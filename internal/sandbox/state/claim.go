package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// AcquireClaim atomically claims a named token under the slug's state dir,
// returning claimed=true and a release func only when this caller won the
// claim. If another live holder owns it, claimed is false and release is nil.
// A dead holder's flock auto-releases, so an abandoned claim can always be
// retaken by a later caller.
func AcquireClaim(slug, key string) (func(), bool, error) {
	dir := filepath.Join(slugDir(slug), "claims")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("create claim dir: %w", err)
	}
	pruneAbandonedClaims(dir)
	path := filepath.Join(dir, key+".claim")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open claim %s: %w", path, err)
	}
	if err := FlockExclusiveNB(f); err != nil {
		_ = f.Close()
		if isLockContention(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock claim %s: %w", path, err)
	}
	return func() {
		_ = f.Close()
	}, true, nil
}

// pruneAbandonedClaims removes claim files under dir whose flock is free (no
// live holder). A file that can be locked immediately belongs to a crashed or
// dead client and is stale; removing it prevents abandoned claims from
// accumulating. Files that fail the non-blocking lock are live-held and kept.
// Because only files with no live flock are removed, pruning can never create
// two simultaneous holders for a key.
func pruneAbandonedClaims(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		f, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			continue
		}
		if err := FlockExclusiveNB(f); err == nil {
			// No live holder, so the claim is abandoned.
			_ = f.Close()
			_ = os.Remove(path)
			continue
		}
		_ = f.Close()
	}
}

// isLockContention reports whether a flock error means the lock is held by
// another process (as opposed to a real failure).
func isLockContention(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
