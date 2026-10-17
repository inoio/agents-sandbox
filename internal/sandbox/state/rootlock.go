package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// Seams over the lock's I/O so tests can exercise failures a real file cannot
// trigger. Production keeps the real functions.

var (
	mkdirRootLock = os.MkdirAll
	openRootLock  = os.OpenFile
)

// flockRootLock takes an exclusive advisory lock, non-blocking when blocking is
// false. It is a seam so tests can exercise the flock failure path.
var flockRootLock = func(f *os.File, blocking bool) error {
	if blocking {
		return FlockExclusive(f)
	}
	return FlockExclusiveNB(f)
}

// rootLockPath returns the host-only root-state lock file path, under the
// never-deleted locksDir.
func rootLockPath() string {
	return filepath.Join(locksDir(), "root.lock")
}

// LockRootState takes the host-only advisory lock that serializes every
// RootState read-modify-write across processes. The flock auto-releases on
// process exit, so a crashed holder never pins the lock. The returned function
// releases the lock.
//
// With blocking true, the call waits for the lock; with blocking false it
// returns ok=false and a nil release when another process holds it.
//
//nolint:nonamedreturns // the documented (release, ok, err) signature is the contract
func LockRootState(blocking bool) (release func(), ok bool, err error) {
	if mkdirErr := mkdirRootLock(filepath.Dir(rootLockPath()), 0o700); mkdirErr != nil {
		return nil, false, fmt.Errorf("create state root: %w", mkdirErr)
	}
	path := rootLockPath()
	f, err := openRootLock(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open root lock: %w", err)
	}
	if err := flockRootLock(f, blocking); err != nil {
		_ = f.Close()
		if !blocking && isLockContention(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock root lock: %w", err)
	}
	return func() { _ = f.Close() }, true, nil
}
