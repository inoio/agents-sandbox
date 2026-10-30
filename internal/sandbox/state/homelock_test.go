package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func testKey() Key {
	return Key{Slug: "proj", Agent: "opencode"}
}

func TestWithHomeStateReadErrorDoesNotWrite(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	// A directory at the state file path makes the read fail with EISDIR, which
	// is not ErrStateNotFound, so the transaction must not overwrite anything.
	if err := os.MkdirAll(stateFile(k), 0o700); err != nil {
		t.Fatalf("mkdir state path: %v", err)
	}

	if err := WithHomeState(k, func(hs *HomeState, _ bool) (bool, error) {
		hs.HomeVolume = "vol-1"
		return true, nil
	}); err == nil {
		t.Fatal("WithHomeState() error = nil, want read error")
	}
}

func TestWithHomeStateWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := writeState(k, HomeState{HomeVolume: "vol-1"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	// Create the lock file first so the subsequent lock succeeds on an existing
	// file and the failure lands on the state write, not the lock open.
	release, _, err := lockHomeState(k, true)
	if err != nil {
		t.Fatalf("lockHomeState: %v", err)
	}
	release()
	keyDir := KeyDir(k)
	if err := os.Chmod(keyDir, 0o555); err != nil {
		t.Skipf("cannot chmod key dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(keyDir, 0o700) })

	if err := WithHomeState(k, func(hs *HomeState, _ bool) (bool, error) {
		hs.AgentVersion = "1.0.0"
		return true, nil
	}); err == nil {
		t.Fatal("WithHomeState() error = nil, want write error")
	}
}

func TestWithHomeStateLockErrorReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirHomeLock
	mkdirHomeLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirHomeLock = orig })

	if err := WithHomeState(testKey(), func(*HomeState, bool) (bool, error) {
		return true, nil
	}); !errors.Is(err, wantErr) {
		t.Fatalf("WithHomeState() error = %v, want %v", err, wantErr)
	}
}

func TestWithHomeStateFailsFastWhileHomeLockHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, ok, err := lockHomeState(k, true)
	if err != nil || !ok {
		t.Fatalf("hold home lock: ok=%v err=%v", ok, err)
	}
	defer release()

	// A same-process blocking re-acquire from another goroutine cannot wait for
	// the lock (flock is not reentrant), so it must fail fast instead of hanging.
	done := make(chan error, 1)
	go func() {
		done <- WithHomeState(k, func(hs *HomeState, _ bool) (bool, error) {
			hs.HomeVolume = "vol-1"
			return true, nil
		})
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "already held") {
			t.Fatalf("WithHomeState() error = %v, want an already-held error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WithHomeState hung while the home lock was held")
	}
}

func TestLockHomeStateNonBlockingSkipsWhenHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, ok, err := lockHomeState(k, true)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	defer release()

	second, ok2, err2 := lockHomeState(k, false)
	if err2 != nil {
		t.Fatalf("second lock error: %v", err2)
	}
	if ok2 || second != nil {
		t.Fatalf("second non-blocking lock: ok=%v release set=%v, want ok=false", ok2, second != nil)
	}
}

func TestLockHomeStateIsPerKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	releaseA, ok, err := lockHomeState(Key{Slug: "a", Agent: "opencode"}, true)
	if err != nil || !ok {
		t.Fatalf("lock a: ok=%v err=%v", ok, err)
	}
	defer releaseA()

	releaseB, ok, err := lockHomeState(Key{Slug: "b", Agent: "opencode"}, false)
	if err != nil || !ok {
		t.Fatalf("lock b: ok=%v err=%v, want a different key to be independent", ok, err)
	}
	releaseB()
}

func TestLockHomeStateMkdirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirHomeLock
	mkdirHomeLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirHomeLock = orig })

	if _, _, err := lockHomeState(testKey(), true); !errors.Is(err, wantErr) {
		t.Fatalf("lockHomeState() error = %v, want %v", err, wantErr)
	}
}

func TestLockHomeStateOpenError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("open boom")
	orig := openHomeLock
	openHomeLock = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openHomeLock = orig })

	if _, _, err := lockHomeState(testKey(), true); !errors.Is(err, wantErr) {
		t.Fatalf("lockHomeState() error = %v, want %v", err, wantErr)
	}
}

func TestLockHomeStateFlockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("flock boom")
	orig := flockHomeLock
	flockHomeLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockHomeLock = orig })

	if _, _, err := lockHomeState(testKey(), true); !errors.Is(err, wantErr) {
		t.Fatalf("lockHomeState() error = %v, want %v", err, wantErr)
	}
}

func TestHomeLockPathLivesUnderLocksDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	want := filepath.Join(configpaths.Get().UserStateDir(), "locks", k.Slug, k.Agent+".lock")
	if got := homeLockPath(k); got != want {
		t.Fatalf("homeLockPath = %q, want %q", got, want)
	}
	if strings.HasPrefix(homeLockPath(k), KeyDir(k)) {
		t.Fatalf("home lock must not live in the removable key dir")
	}
}

func TestHomeLockPathLegacyHasNoAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "proj"}
	want := filepath.Join(configpaths.Get().UserStateDir(), "locks", k.Slug, "legacy.lock")
	if got := homeLockPath(k); got != want {
		t.Fatalf("homeLockPath = %q, want %q", got, want)
	}
}
