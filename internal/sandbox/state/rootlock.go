package state

import (
	"fmt"
	"os"
	"path/filepath"
)

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
	if mkdirErr := os.MkdirAll(stateRoot(), 0o700); mkdirErr != nil {
		return nil, false, fmt.Errorf("create state root: %w", mkdirErr)
	}
	path := filepath.Join(stateRoot(), "root.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open root lock: %w", err)
	}
	flock := FlockExclusive
	if !blocking {
		flock = FlockExclusiveNB
	}
	if err := flock(f); err != nil {
		_ = f.Close()
		if !blocking && isLockContention(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock root lock: %w", err)
	}
	return func() { _ = f.Close() }, true, nil
}
