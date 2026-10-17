package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		return FlockExclusive(f)
	}
	return FlockExclusiveNB(f)
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

// LockHomeState takes the per-(slug, agent) advisory lock that serializes every
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
func LockHomeState(k Key, blocking bool) (release func(), ok bool, err error) {
	dir := filepath.Dir(homeLockPath(k))
	if mkdirErr := mkdirHomeLock(dir, 0o700); mkdirErr != nil {
		return nil, false, fmt.Errorf("create lock dir: %w", mkdirErr)
	}
	f, err := openHomeLock(homeLockPath(k), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open home lock: %w", err)
	}
	if err := flockHomeLock(f, blocking); err != nil {
		_ = f.Close()
		if !blocking && isLockContention(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock home lock: %w", err)
	}
	return func() { _ = f.Close() }, true, nil
}

// withHomeLock runs fn while holding the blocking home lock.
func withHomeLock(k Key, fn func() error) error {
	release, _, err := LockHomeState(k, true)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

// UpdateHomeState applies mutate to the project's HomeState and writes the
// result, serializing the read-modify-write under the home lock so concurrent
// writers cannot clobber each other's fields (e.g. a volume operation changing
// home_volume while provenance records agent_version). A missing state file
// starts from the zero HomeState. A present-but-unreadable file is returned as
// an error without writing, so an unreadable document is never clobbered.
func UpdateHomeState(k Key, mutate func(*HomeState)) error {
	return withHomeLock(k, func() error {
		hs, err := ReadState(k)
		if err != nil {
			if !errors.Is(err, ErrStateNotFound) {
				return fmt.Errorf("read state for update: %w", err)
			}
			hs = new(HomeState)
		}
		mutate(hs)
		return WriteState(k, *hs)
	})
}

// UpdateExistingHomeState applies mutate and writes only when the project's
// state file already exists, returning updated=false and writing nothing when
// it is absent. Writers that run before home resolution use it so they never
// create a fingerprint-less partial state that would poison later home recovery
// (#183). A present-but-unreadable file returns an error without writing.
func UpdateExistingHomeState(k Key, mutate func(*HomeState)) (bool, error) {
	updated := false
	err := withHomeLock(k, func() error {
		hs, readErr := ReadState(k)
		if readErr != nil {
			if errors.Is(readErr, ErrStateNotFound) {
				return nil
			}
			return fmt.Errorf("read state for update: %w", readErr)
		}
		mutate(hs)
		if writeErr := WriteState(k, *hs); writeErr != nil {
			return writeErr
		}
		updated = true
		return nil
	})
	return updated, err
}

// ReplaceHomeState writes hs as the project's HomeState under the home lock,
// overwriting any existing document, including an unreadable or corrupt one.
// Recovery paths that rebuild a lost or malformed state use it so a broken file
// is repaired rather than left in place; every other writer merges through
// UpdateHomeState or UpdateExistingHomeState.
func ReplaceHomeState(k Key, hs HomeState) error {
	return withHomeLock(k, func() error { return WriteState(k, hs) })
}
