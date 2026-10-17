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
	if err := os.MkdirAll(filepath.Join(configpaths.Get().UserStateDir(), "locks", "root.lock"), 0o700); err != nil {
		t.Fatalf("block root lock: %v", err)
	}
}

func TestAcquireMigrationLockFailsFastOnSameProcessDoubleHold(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	release, err := acquireMigrationLock()
	if err != nil {
		t.Fatalf("acquireMigrationLock: %v", err)
	}

	// The root lock is not reentrant: a same-process second acquire cannot wait
	// for it, so it must fail fast instead of hanging.
	done := make(chan error, 1)
	go func() {
		r, err := acquireMigrationLock()
		if r != nil {
			r()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("second acquire succeeded while the first held the lock")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire hung while the first held the lock")
	}

	release()

	// A released lock is reacquirable.
	again, err := acquireMigrationLock()
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	again()
}
