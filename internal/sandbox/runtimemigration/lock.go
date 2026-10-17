package runtimemigration

import (
	"errors"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// acquireMigrationLock takes the host-only advisory lock that serializes the
// legacy home-volume copy campaign across processes. The flock auto-releases on
// process exit, so a crashed holder never pins the campaign. The returned
// function releases the lock.
func acquireMigrationLock() (func(), error) {
	release, ok, err := state.LockRootState(true)
	if err != nil {
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	if !ok {
		return nil, errors.New("acquire migration lock: lock not acquired")
	}
	return release, nil
}
