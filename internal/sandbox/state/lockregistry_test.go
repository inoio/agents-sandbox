package state

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestLockRegistryFailsFastOnDoubleHold(t *testing.T) {
	if err := holdLockPath("/tmp/x.lock"); err != nil {
		t.Fatalf("first hold: %v", err)
	}
	err := holdLockPath("/tmp/x.lock")
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("second hold = %v, want an already-held error", err)
	}
	releaseLockPath("/tmp/x.lock")
	if err := holdLockPath("/tmp/x.lock"); err != nil {
		t.Fatalf("re-hold after release: %v", err)
	}
	releaseLockPath("/tmp/x.lock")
}

func TestRemoveHomeStateIfFailsFastWhileHomeLockHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatal(err)
	}
	release, _, err := lockHomeState(k, true)
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	defer release()
	if err := RemoveHomeStateIf(k, removeAlways); err == nil {
		t.Fatal("RemoveHomeStateIf proceeded while the blocking home lock was held")
	}
	if _, rerr := ReadState(k); rerr != nil {
		t.Fatalf("state must survive the refused removal: %v", rerr)
	}
}

func TestLockHomeStateFailsFastOnSameProcessDoubleHold(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, ok, err := lockHomeState(k, true)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}

	_, _, err = lockHomeState(k, true)
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("second blocking lock = %v, want an already-held error", err)
	}

	release()

	again, ok, err := lockHomeState(k, true)
	if err != nil || !ok {
		t.Fatalf("reacquire after release: ok=%v err=%v", ok, err)
	}
	again()
}

func TestLockRootStateFailsFastOnSameProcessDoubleHold(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	release, ok, err := lockRootState(true)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}

	_, _, err = lockRootState(true)
	if err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("second blocking lock = %v, want an already-held error", err)
	}

	release()

	again, ok, err := lockRootState(true)
	if err != nil || !ok {
		t.Fatalf("reacquire after release: ok=%v err=%v", ok, err)
	}
	again()
}

func TestLockHomeStateNonBlockingDoesNotRegisterHeldPath(t *testing.T) {
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

func TestLockRootStateFlockErrorReleasesRegistry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("flock boom")
	orig := flockRootLock
	flockRootLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockRootLock = orig })

	if _, _, err := lockRootState(true); !errors.Is(err, wantErr) {
		t.Fatalf("lockRootState() error = %v, want %v", err, wantErr)
	}

	// The failed acquire must not leave the path registered, or a retry that
	// actually succeeds would then fail fast on a stale registration.
	flockRootLock = orig
	release, ok, err := lockRootState(true)
	if err != nil || !ok {
		t.Fatalf("retry after flock failure: ok=%v err=%v", ok, err)
	}
	release()
}
