package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/hostlock"
)

func TestLockRootStateNonBlockingSkipsWhenHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	release, ok, err := lockRootState(true)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	defer release()
	second, ok2, err2 := lockRootState(false)
	if err2 != nil {
		t.Fatalf("second lock error: %v", err2)
	}
	if ok2 || second != nil {
		t.Fatalf("second non-blocking lock: ok=%v release set=%v, want ok=false", ok2, second != nil)
	}
}

func TestLockRootStateReleasesForReacquire(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	release, ok, err := lockRootState(false)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	release()
	again, ok, err := lockRootState(false)
	if err != nil || !ok {
		t.Fatalf("reacquire: ok=%v err=%v", ok, err)
	}
	again()
}

func TestLockRootStateMkdirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirRootLock
	mkdirRootLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirRootLock = orig })

	release, ok, err := lockRootState(true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("lockRootState() error = %v, want %v", err, wantErr)
	}
	if ok || release != nil {
		t.Fatalf("lockRootState() = (release set=%v, ok=%v), want ok=false and a nil release", release != nil, ok)
	}
}

func TestLockRootStateOpenError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("open boom")
	orig := openRootLock
	openRootLock = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openRootLock = orig })

	release, ok, err := lockRootState(true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("lockRootState() error = %v, want %v", err, wantErr)
	}
	if ok || release != nil {
		t.Fatalf("lockRootState() = (release set=%v, ok=%v), want ok=false and a nil release", release != nil, ok)
	}
	// The blocking open failure released the registry entry it registered.
	path := rootLockPath()
	if err := holdLockPath(path); err != nil {
		t.Fatalf("a failed open leaked its registry entry: %v", err)
	}
	releaseLockPath(path)
}

func TestLockRootStateFlockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("flock boom")
	orig := flockRootLock
	flockRootLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockRootLock = orig })

	release, ok, err := lockRootState(true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("lockRootState() error = %v, want %v", err, wantErr)
	}
	if ok || release != nil {
		t.Fatalf("lockRootState() = (release set=%v, ok=%v), want ok=false and a nil release", release != nil, ok)
	}
}

// TestLockRootStateReentrantReportsNotOk pins the reentrancy fail-fast: a second
// blocking acquire of a held path errors with ok=false.
func TestLockRootStateReentrantReportsNotOk(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	release, ok, err := lockRootState(true)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	defer release()

	release2, ok2, err2 := lockRootState(true)
	if err2 == nil || !strings.Contains(err2.Error(), "already held") {
		t.Fatalf("reentrant acquire error = %v, want an already-held error", err2)
	}
	if ok2 || release2 != nil {
		t.Fatalf("reentrant acquire = (release set=%v, ok=%v), want ok=false and a nil release", release2 != nil, ok2)
	}
}

// TestLockRootStateUsesPrivateModes pins the lock dir and file modes at the
// seam, so the assertion does not depend on the test process's umask.
func TestLockRootStateUsesPrivateModes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	var dirMode, fileMode os.FileMode
	origMkdir, origOpen := mkdirRootLock, openRootLock
	mkdirRootLock = func(dir string, mode os.FileMode) error {
		dirMode = mode
		return os.MkdirAll(dir, mode)
	}
	openRootLock = func(path string, flag int, mode os.FileMode) (*os.File, error) {
		fileMode = mode
		return os.OpenFile(path, flag, mode)
	}
	t.Cleanup(func() { mkdirRootLock, openRootLock = origMkdir, origOpen })

	release, ok, err := lockRootState(false)
	if err != nil || !ok {
		t.Fatalf("lockRootState: ok=%v err=%v", ok, err)
	}
	release()

	if dirMode != 0o700 {
		t.Errorf("lock dir mode = %o, want 700", dirMode)
	}
	if fileMode != 0o600 {
		t.Errorf("lock file mode = %o, want 600", fileMode)
	}
}

// TestLockRootStateBlockingWaitsForFlock pins blocking=true to a blocking flock:
// while another open file description holds the lock, the acquire must wait, not
// report contention.
func TestLockRootStateBlockingWaitsForFlock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	path := rootLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := hostlock.Exclusive(holder); err != nil {
		_ = holder.Close()
		t.Fatalf("hold raw flock: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		release, _, lockErr := lockRootState(true)
		if lockErr == nil {
			release()
		}
		result <- lockErr
	}()

	select {
	case err := <-result:
		_ = holder.Close()
		t.Fatalf("blocking acquire returned while the lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	_ = holder.Close()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("blocking acquire after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocking acquire did not unblock after the lock was released")
	}
}

// TestLockRootStateBlockingContentionErrorIsSurfaced pins that a contention error
// on a blocking acquire is a failure, not the non-blocking "lock is busy" signal.
func TestLockRootStateBlockingContentionErrorIsSurfaced(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	orig := flockRootLock
	flockRootLock = func(*os.File, bool) error { return syscall.EWOULDBLOCK }
	t.Cleanup(func() { flockRootLock = orig })

	if _, ok, err := lockRootState(true); err == nil {
		t.Fatalf("blocking acquire with a contention error = (ok=%v, err=nil), want an error", ok)
	} else if ok {
		t.Fatal("a failed acquire must report ok=false")
	}
}

// TestLockRootStateNonBlockingRealErrorIsSurfaced pins that a non-contention
// flock failure on a non-blocking acquire is an error, not the "lock is busy"
// signal.
func TestLockRootStateNonBlockingRealErrorIsSurfaced(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("flock boom")
	orig := flockRootLock
	flockRootLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockRootLock = orig })

	if _, ok, err := lockRootState(false); !errors.Is(err, wantErr) {
		t.Fatalf("non-blocking acquire = (ok=%v, err=%v), want %v", ok, err, wantErr)
	} else if ok {
		t.Fatal("a failed acquire must report ok=false")
	}
}
