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

func TestUpdateHomeStateCreatesMissingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	if err := UpdateHomeState(k, func(hs *HomeState) { hs.HomeVolume = "vol-1" }); err != nil {
		t.Fatalf("UpdateHomeState: %v", err)
	}

	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "vol-1" {
		t.Fatalf("HomeVolume = %q, want vol-1", got.HomeVolume)
	}
}

func TestUpdateHomeStatePreservesOtherFields(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WriteState(k, HomeState{HomeVolume: "vol-1", AgentVersion: "1.2.3"}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	if err := UpdateHomeState(k, func(hs *HomeState) { hs.HomeVolume = "vol-2" }); err != nil {
		t.Fatalf("UpdateHomeState: %v", err)
	}

	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "vol-2" || got.AgentVersion != "1.2.3" {
		t.Fatalf("state = %+v, want vol-2 and agent version 1.2.3 preserved", got)
	}
}

func TestUpdateHomeStateReadErrorDoesNotWrite(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	// A directory at the state file path makes the read fail with EISDIR, which
	// is not ErrStateNotFound, so the update must not overwrite anything.
	if err := os.MkdirAll(stateFile(k), 0o700); err != nil {
		t.Fatalf("mkdir state path: %v", err)
	}

	if err := UpdateHomeState(k, func(hs *HomeState) { hs.HomeVolume = "vol-1" }); err == nil {
		t.Fatal("UpdateHomeState() error = nil, want read error")
	}
}

func TestUpdateExistingHomeStateSkipsMissingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	updated, err := UpdateExistingHomeState(k, func(hs *HomeState) { hs.AgentVersion = "1.0.0" })
	if err != nil {
		t.Fatalf("UpdateExistingHomeState: %v", err)
	}
	if updated {
		t.Fatal("updated = true, want false for a missing state file")
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no partial state written)", err)
	}
}

func TestUpdateExistingHomeStateUpdatesPresentState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WriteState(k, HomeState{HomeVolume: "vol-1"}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	updated, err := UpdateExistingHomeState(k, func(hs *HomeState) { hs.AgentVersion = "1.0.0" })
	if err != nil {
		t.Fatalf("UpdateExistingHomeState: %v", err)
	}
	if !updated {
		t.Fatal("updated = false, want true for a present state file")
	}
	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "vol-1" || got.AgentVersion != "1.0.0" {
		t.Fatalf("state = %+v, want vol-1 and agent version 1.0.0", got)
	}
}

func TestUpdateExistingHomeStateReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := os.MkdirAll(stateFile(k), 0o700); err != nil {
		t.Fatalf("mkdir state path: %v", err)
	}

	if _, err := UpdateExistingHomeState(k, func(*HomeState) {}); err == nil {
		t.Fatal("UpdateExistingHomeState() error = nil, want read error")
	}
}

func TestUpdateExistingHomeStateWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := WriteState(k, HomeState{HomeVolume: "vol-1"}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	// Create the lock file first so the subsequent lock succeeds on an existing
	// file and the failure lands on the state write, not the lock open.
	release, _, err := LockHomeState(k, true)
	if err != nil {
		t.Fatalf("LockHomeState: %v", err)
	}
	release()
	keyDir := KeyDir(k)
	if err := os.Chmod(keyDir, 0o555); err != nil {
		t.Skipf("cannot chmod key dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(keyDir, 0o700) })

	if _, err := UpdateExistingHomeState(k, func(hs *HomeState) { hs.AgentVersion = "1.0.0" }); err == nil {
		t.Fatal("UpdateExistingHomeState() error = nil, want write error")
	}
}

func TestReplaceHomeStateOverwritesCorruptState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := os.MkdirAll(KeyDir(k), 0o700); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(stateFile(k), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	if err := ReplaceHomeState(k, HomeState{HomeVolume: "recovered"}); err != nil {
		t.Fatalf("ReplaceHomeState: %v", err)
	}
	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after replace: %v", err)
	}
	if got.HomeVolume != "recovered" {
		t.Fatalf("HomeVolume = %q, want recovered", got.HomeVolume)
	}
}

func TestUpdateHomeStateLockErrorReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirHomeLock
	mkdirHomeLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirHomeLock = orig })

	if err := UpdateHomeState(testKey(), func(*HomeState) {}); !errors.Is(err, wantErr) {
		t.Fatalf("UpdateHomeState() error = %v, want %v", err, wantErr)
	}
}

func TestUpdateHomeStateSerializesWriters(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, ok, err := LockHomeState(k, true)
	if err != nil || !ok {
		t.Fatalf("hold home lock: ok=%v err=%v", ok, err)
	}

	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		done <- UpdateHomeState(k, func(hs *HomeState) { hs.HomeVolume = "vol-1" })
	}()
	<-started

	select {
	case <-done:
		t.Fatal("UpdateHomeState completed while the home lock was held")
	case <-time.After(100 * time.Millisecond):
	}

	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UpdateHomeState: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateHomeState did not complete after the lock was released")
	}
}

func TestLockHomeStateNonBlockingSkipsWhenHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, ok, err := LockHomeState(k, true)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	defer release()

	second, ok2, err2 := LockHomeState(k, false)
	if err2 != nil {
		t.Fatalf("second lock error: %v", err2)
	}
	if ok2 || second != nil {
		t.Fatalf("second non-blocking lock: ok=%v release set=%v, want ok=false", ok2, second != nil)
	}
}

func TestLockHomeStateIsPerKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	releaseA, ok, err := LockHomeState(Key{Slug: "a", Agent: "opencode"}, true)
	if err != nil || !ok {
		t.Fatalf("lock a: ok=%v err=%v", ok, err)
	}
	defer releaseA()

	releaseB, ok, err := LockHomeState(Key{Slug: "b", Agent: "opencode"}, false)
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

	if _, _, err := LockHomeState(testKey(), true); !errors.Is(err, wantErr) {
		t.Fatalf("LockHomeState() error = %v, want %v", err, wantErr)
	}
}

func TestLockHomeStateOpenError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("open boom")
	orig := openHomeLock
	openHomeLock = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openHomeLock = orig })

	if _, _, err := LockHomeState(testKey(), true); !errors.Is(err, wantErr) {
		t.Fatalf("LockHomeState() error = %v, want %v", err, wantErr)
	}
}

func TestLockHomeStateFlockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("flock boom")
	orig := flockHomeLock
	flockHomeLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockHomeLock = orig })

	if _, _, err := LockHomeState(testKey(), true); !errors.Is(err, wantErr) {
		t.Fatalf("LockHomeState() error = %v, want %v", err, wantErr)
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
