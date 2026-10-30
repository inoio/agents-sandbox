package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/inoio/agents-sandbox/internal/hostlock"
)

// clientLeaseGraceWindow is how long an unlocked client lease is still treated
// as live. AcquireClientLease creates the lock file and only then takes its
// flock, so a CountActiveClients landing in that window sees a file with no
// holder; treating it as garbage would leave the acquirer holding a flock on an
// anonymous inode that no later count can see, making the lease permanently
// invisible (#298, the client-lease race flagged in the #245 review). The window
// only needs to exceed the create-to-flock gap (microseconds); a second is
// conservatively generous while still reaping a crashed client's abandoned
// lease promptly.
const clientLeaseGraceWindow = time.Second

// clientLeaseOpened is a test seam invoked right after a lease file is opened
// and before it is locked and verified. It lets a test force the open/lock race
// deterministically; production leaves it nil.
var clientLeaseOpened func(f *os.File, path string)

// AcquireClientLease creates a unique lock file for the current client under the
// project/agent key's client state dir and holds an exclusive flock on it. It
// returns a release function. The flock is auto-released if the process exits or
// crashes, so a dead client never pins the VM. CountActiveClients derives the
// live client count from held locks, avoiding stale persisted counters.
//
// CountActiveClients removes an unlocked lease, so the file can be unlinked
// between this caller's create and its lock (a client stalled longer than
// clientLeaseGraceWindow between the two). A caller that locked a file removed
// underneath it detects that via sameFile and retries on a fresh name, so a lease
// is never left locked on an anonymous inode no later count can see.
func AcquireClientLease(k Key) (func(), error) {
	dir := filepath.Join(KeyDir(k), "clients")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create client lock dir: %w", err)
	}
	for range 5 {
		path := filepath.Join(dir, fmt.Sprintf("%d-%d.lock", os.Getpid(), time.Now().UnixNano()))
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open client lock %s: %w", path, err)
		}
		if clientLeaseOpened != nil {
			clientLeaseOpened(f, path)
		}
		if err := hostlock.Exclusive(f); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("flock client lock %s: %w", path, err)
		}
		if !sameFile(f, path) {
			// CountActiveClients unlinked the file between our create and our lock;
			// we hold the flock on an inode with no directory entry, so retry.
			_ = f.Close()
			continue
		}
		return func() {
			// Remove while holding the flock, then release it.
			_ = os.Remove(path)
			_ = f.Close()
		}, nil
	}
	return nil, fmt.Errorf("acquire client lock for %s/%s: unlinked on every attempt", k.Slug, k.Agent)
}

// CountActiveClients returns the number of live client leases for a key. A lease
// whose lock is held by a live client is counted. A lease whose lock is free is
// either fresh — the client is between creating the file and taking its flock,
// so it is counted and kept — or old, a crashed client's leftover, and is
// removed. The grace window bounding "fresh" is clientLeaseGraceWindow.
//
// The freshness check and the removal happen while the probe lock is held, so an
// acquirer cannot take its flock between the "unlocked" decision and the unlink;
// AcquireClientLease detects a removal that did happen via sameFile and retries.
func CountActiveClients(k Key) int {
	dir := filepath.Join(KeyDir(k), "clients")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	active := 0
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		f, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			continue
		}
		if err := hostlock.ExclusiveNonBlocking(f); err != nil {
			// Held by a live client.
			_ = f.Close()
			active++
			continue
		}
		// Unlocked: "fresh" decides between a lease mid-create (keep and count)
		// and a crashed client's leftover (remove).
		if leaseIsFresh(e) {
			_ = f.Close()
			active++
			continue
		}
		_ = os.Remove(path)
		_ = f.Close()
	}
	return active
}

// leaseIsFresh reports whether an unlocked lease file is young enough to be a
// client mid-create rather than an abandoned lease. A stat failure is treated as
// fresh so an entry is never unlinked on uncertainty.
func leaseIsFresh(e os.DirEntry) bool {
	info, err := e.Info()
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) <= clientLeaseGraceWindow
}
