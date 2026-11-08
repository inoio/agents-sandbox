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

	release, ok, err := lockHomeState(testKey(), true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("lockHomeState() error = %v, want %v", err, wantErr)
	}
	if ok || release != nil {
		t.Fatalf("lockHomeState() = (release set=%v, ok=%v), want ok=false and a nil release", release != nil, ok)
	}
}

func TestLockHomeStateOpenError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	wantErr := errors.New("open boom")
	orig := openHomeLock
	openHomeLock = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openHomeLock = orig })

	release, ok, err := lockHomeState(k, true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("lockHomeState() error = %v, want %v", err, wantErr)
	}
	if ok || release != nil {
		t.Fatalf("lockHomeState() = (release set=%v, ok=%v), want ok=false and a nil release", release != nil, ok)
	}
	// The blocking open failure released the registry entry it registered, so a
	// later acquire of the same path is not a false "already held".
	path := homeLockPath(k)
	if err := holdLockPath(path); err != nil {
		t.Fatalf("a failed open leaked its registry entry: %v", err)
	}
	releaseLockPath(path)
}

func TestLockHomeStateFlockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	wantErr := errors.New("flock boom")
	orig := flockHomeLock
	flockHomeLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockHomeLock = orig })

	release, ok, err := lockHomeState(k, true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("lockHomeState() error = %v, want %v", err, wantErr)
	}
	if ok || release != nil {
		t.Fatalf("lockHomeState() = (release set=%v, ok=%v), want ok=false and a nil release", release != nil, ok)
	}
	// The blocking flock failure released the registry entry it registered.
	path := homeLockPath(k)
	if err := holdLockPath(path); err != nil {
		t.Fatalf("a failed flock leaked its registry entry: %v", err)
	}
	releaseLockPath(path)
}

// TestLockHomeStateUsesPrivateModes pins the lock dir and file modes at the
// seam, so the assertion does not depend on the test process's umask.
func TestLockHomeStateUsesPrivateModes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	var dirMode, fileMode os.FileMode
	origMkdir, origOpen := mkdirHomeLock, openHomeLock
	mkdirHomeLock = func(dir string, mode os.FileMode) error {
		dirMode = mode
		return os.MkdirAll(dir, mode)
	}
	openHomeLock = func(path string, flag int, mode os.FileMode) (*os.File, error) {
		fileMode = mode
		return os.OpenFile(path, flag, mode)
	}
	t.Cleanup(func() { mkdirHomeLock, openHomeLock = origMkdir, origOpen })

	release, ok, err := lockHomeState(testKey(), false)
	if err != nil || !ok {
		t.Fatalf("lockHomeState: ok=%v err=%v", ok, err)
	}
	release()

	if dirMode != 0o700 {
		t.Errorf("lock dir mode = %o, want 700", dirMode)
	}
	if fileMode != 0o600 {
		t.Errorf("lock file mode = %o, want 600", fileMode)
	}
}

// TestLockHomeStateReentrantReportsNotOk pins the reentrancy fail-fast: a second
// blocking acquire of a held path errors with ok=false, so a caller that reads
// the tuple is not handed a false success.
func TestLockHomeStateReentrantReportsNotOk(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, ok, err := lockHomeState(k, true)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	defer release()

	release2, ok2, err2 := lockHomeState(k, true)
	if err2 == nil || !strings.Contains(err2.Error(), "already held") {
		t.Fatalf("reentrant acquire error = %v, want an already-held error", err2)
	}
	if ok2 || release2 != nil {
		t.Fatalf("reentrant acquire = (release set=%v, ok=%v), want ok=false and a nil release", release2 != nil, ok2)
	}
}

// TestLockHomeStateBlockingWaitsForFlock pins blocking=true to a blocking flock:
// while another open file description holds the lock, the acquire must wait, not
// report contention.
func TestLockHomeStateBlockingWaitsForFlock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	path := homeLockPath(k)
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
		release, _, lockErr := lockHomeState(k, true)
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

// TestLockHomeStateBlockingContentionErrorIsSurfaced pins that a contention error
// on a blocking acquire is a failure, not the non-blocking "lock is busy" signal.
func TestLockHomeStateBlockingContentionErrorIsSurfaced(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	orig := flockHomeLock
	flockHomeLock = func(*os.File, bool) error { return syscall.EWOULDBLOCK }
	t.Cleanup(func() { flockHomeLock = orig })

	if _, ok, err := lockHomeState(testKey(), true); err == nil {
		t.Fatalf("blocking acquire with a contention error = (ok=%v, err=nil), want an error", ok)
	} else if ok {
		t.Fatal("a failed acquire must report ok=false")
	}
}

// TestLockHomeStateNonBlockingRealErrorIsSurfaced pins that a non-contention
// flock failure on a non-blocking acquire is an error, not the "lock is busy"
// signal.
func TestLockHomeStateNonBlockingRealErrorIsSurfaced(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("flock boom")
	orig := flockHomeLock
	flockHomeLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockHomeLock = orig })

	if _, ok, err := lockHomeState(testKey(), false); !errors.Is(err, wantErr) {
		t.Fatalf("non-blocking acquire = (ok=%v, err=%v), want %v", ok, err, wantErr)
	} else if ok {
		t.Fatal("a failed acquire must report ok=false")
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
