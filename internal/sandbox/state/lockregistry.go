package state

import (
	"fmt"
	"sync"
)

// heldLockPaths tracks the lock file paths this process currently holds via a
// blocking acquire, guarded by heldLockPathsMu. flock is not reentrant: a
// second blocking acquire of a path this process already holds would deadlock,
// so the guard turns that into a fast, descriptive error. The registry is
// process-local and covers both a recursive call and two goroutines in one
// process.
var (
	heldLockPathsMu sync.Mutex
	heldLockPaths   = map[string]bool{}
)

// holdLockPath registers path as held by this process, returning a descriptive
// error if it is already held. It is only used for blocking acquires; a
// non-blocking acquire cannot deadlock and must stay side-effect-free.
func holdLockPath(path string) error {
	heldLockPathsMu.Lock()
	defer heldLockPathsMu.Unlock()
	if heldLockPaths[path] {
		return fmt.Errorf("lock %s already held by this process", path)
	}
	heldLockPaths[path] = true
	return nil
}

// releaseLockPath unregisters path, allowing a later acquire to succeed.
func releaseLockPath(path string) {
	heldLockPathsMu.Lock()
	defer heldLockPathsMu.Unlock()
	delete(heldLockPaths, path)
}
