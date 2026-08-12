package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AcquireClientLease creates a unique lock file for the current client under the
// project slug's client state dir and holds an exclusive flock on it. It returns
// a release function. The flock is auto-released if the process exits or crashes,
// so a dead client never pins the VM. CountActiveClients derives the live client
// count from held locks, avoiding stale persisted counters.
func AcquireClientLease(slug string) (func(), error) {
	dir := filepath.Join(slugDir(slug), "clients")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create client lock dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-%d.lock", os.Getpid(), time.Now().UnixNano()))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open client lock %s: %w", path, err)
	}
	if err := FlockExclusive(f); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("flock client lock %s: %w", path, err)
	}
	return func() {
		_ = f.Close()
		_ = os.Remove(path)
	}, nil
}

// CountActiveClients returns the number of live client leases for a slug. A lock
// file whose lock can be acquired immediately is abandoned (no live holder) and is
// removed. Files that fail a non-blocking exclusive lock are held by a live client
// and counted.
func CountActiveClients(slug string) int {
	dir := filepath.Join(slugDir(slug), "clients")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	active := 0
	for _, e := range entries {
		f, err := os.OpenFile(filepath.Join(dir, e.Name()), os.O_RDWR, 0o600)
		if err != nil {
			continue
		}
		if err := FlockExclusiveNB(f); err == nil {
			// No live holder.
			_ = f.Close()
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		_ = f.Close()
		active++
	}
	return active
}
