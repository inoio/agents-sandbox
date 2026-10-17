package state

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestLockRootStateNonBlockingSkipsWhenHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	release, ok, err := LockRootState(true)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	defer release()
	second, ok2, err2 := LockRootState(false)
	if err2 != nil {
		t.Fatalf("second lock error: %v", err2)
	}
	if ok2 || second != nil {
		t.Fatalf("second non-blocking lock: ok=%v release set=%v, want ok=false", ok2, second != nil)
	}
}

func TestLockRootStateReleasesForReacquire(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	release, ok, err := LockRootState(false)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	release()
	again, ok, err := LockRootState(false)
	if err != nil || !ok {
		t.Fatalf("reacquire: ok=%v err=%v", ok, err)
	}
	again()
}
