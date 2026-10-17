package runtimemigration

import (
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// acquireMigrationLock takes the host-only advisory lock that serializes the
// legacy home-volume copy campaign across processes. The flock auto-releases on
// process exit, so a crashed holder never pins the campaign. The returned
// function releases the lock.
//
// The lock is taken blocking, so it is always acquired when err is nil; the
// non-blocking ok result is intentionally ignored.
func acquireMigrationLock() (func(), error) {
	release, _, err := state.LockRootState(true)
	if err != nil {
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	return release, nil
}
