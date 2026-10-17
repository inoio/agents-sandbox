package state

import (
	"errors"
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
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

	if _, _, err := lockRootState(true); !errors.Is(err, wantErr) {
		t.Fatalf("lockRootState() error = %v, want %v", err, wantErr)
	}
}

func TestLockRootStateOpenError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("open boom")
	orig := openRootLock
	openRootLock = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openRootLock = orig })

	if _, _, err := lockRootState(true); !errors.Is(err, wantErr) {
		t.Fatalf("lockRootState() error = %v, want %v", err, wantErr)
	}
}

func TestLockRootStateFlockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("flock boom")
	orig := flockRootLock
	flockRootLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockRootLock = orig })

	if _, _, err := lockRootState(true); !errors.Is(err, wantErr) {
		t.Fatalf("lockRootState() error = %v, want %v", err, wantErr)
	}
}
