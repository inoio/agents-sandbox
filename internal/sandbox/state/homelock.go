package state

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/hostlock"
)

// Seams over the home lock's I/O so tests can exercise failures the real
// functions cannot trigger. Production keeps the real functions.

var (
	mkdirHomeLock = os.MkdirAll
	openHomeLock  = os.OpenFile
)

// flockHomeLock takes an exclusive advisory lock on the home lock file,
// non-blocking when blocking is false. It is a seam so tests can exercise the
// flock failure path.
var flockHomeLock = func(f *os.File, blocking bool) error {
	if blocking {
		return hostlock.Exclusive(f)
	}
	return hostlock.ExclusiveNonBlocking(f)
}

// homeLockPath returns the per-(slug, agent) home-state lock file path. Lock
// files live under the never-deleted locksDir, not the removable key directory.
func homeLockPath(k Key) string {
	name := k.Agent + ".lock"
	if k.Agent == "" {
		name = "legacy.lock"
	}
	return filepath.Join(locksDir(), k.Slug, name)
}

// lockHomeState takes the per-(slug, agent) advisory lock that serializes every
// HomeState read-modify-write across processes, so concurrent writers (volume
// operations, upgrade bookkeeping, fingerprint persistence) cannot lose each
// other's changes. The flock auto-releases on process exit, so a crashed holder
// never pins the lock. It is not reentrant: a single call path must never take
// the same key's home lock twice, since flock locks a file description per open.
//
// With blocking true the call waits for the lock; with blocking false it
// returns ok=false and a nil release when another process holds it.
//
//nolint:nonamedreturns // the documented (release, ok, err) signature is the contract
func lockHomeState(k Key, blocking bool) (release func(), ok bool, err error) {
	path := homeLockPath(k)
	dir := filepath.Dir(path)
	if mkdirErr := mkdirHomeLock(dir, 0o700); mkdirErr != nil {
		return nil, false, fmt.Errorf("create lock dir: %w", mkdirErr)
	}
	if blocking {
		if holdErr := holdLockPath(path); holdErr != nil {
			return nil, false, holdErr
		}
	}
	f, err := openHomeLock(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		if blocking {
			releaseLockPath(path)
		}
		return nil, false, fmt.Errorf("open home lock: %w", err)
	}
	if err := flockHomeLock(f, blocking); err != nil {
		_ = f.Close()
		if blocking {
			releaseLockPath(path)
		}
		if !blocking && hostlock.IsContention(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock home lock: %w", err)
	}
	release = func() {
		_ = f.Close()
		if blocking {
			releaseLockPath(path)
		}
	}
	return release, true, nil
}

// withHomeLock runs fn while holding the blocking home lock.
func withHomeLock(k Key, fn func() error) error {
	release, _, err := lockHomeState(k, true)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}
