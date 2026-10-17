package runtimemigration

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// blockRootLock occupies the shared root-state lock path with a directory so
// LockRootState's open fails.
func blockRootLock(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(configpaths.Get().UserStateDir(), "root.lock"), 0o700); err != nil {
		t.Fatalf("block root lock: %v", err)
	}
}

func TestAcquireMigrationLockBlocksSecondHolder(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	release, err := acquireMigrationLock()
	if err != nil {
		t.Fatalf("acquireMigrationLock: %v", err)
	}
	done := make(chan struct{})
	go func() {
		r, _ := acquireMigrationLock()
		if r != nil {
			r()
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("second acquire returned while the first held the lock")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire did not proceed after release")
	}
}
