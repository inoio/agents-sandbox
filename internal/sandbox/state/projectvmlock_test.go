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

// TestProjectVMLockDirLivesUnderLocksDir pins the project-VM creation lock to
// the never-deleted locks tree. It lived under KeyDir before #330, so prune's
// state removal unlinked the lock a concurrent VM creation held.
func TestProjectVMLockDirLivesUnderLocksDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	want := filepath.Join(configpaths.Get().UserStateDir(), "locks", k.Slug, k.Agent)
	if got := ProjectVMLockDir(k); got != want {
		t.Fatalf("ProjectVMLockDir = %q, want %q", got, want)
	}
	if strings.HasPrefix(ProjectVMLockDir(k), KeyDir(k)) {
		t.Fatal("project-VM lock dir must not live in the removable key dir")
	}
}

// TestProjectVMLockDirIsAgentScoped pins the project-VM creation lock to the
// agent. Agents of one project use separate VMs (see projectVMName), so sharing
// one creation lock would make an unrelated agent's creation wait (#330).
func TestProjectVMLockDirIsAgentScoped(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	opencode := Key{Slug: "proj", Agent: "opencode"}
	pi := Key{Slug: "proj", Agent: "pi"}

	if ProjectVMLockDir(opencode) == ProjectVMLockDir(pi) {
		t.Fatalf("different agents must not share a creation lock: both %q", ProjectVMLockDir(opencode))
	}
}

// TestLockProjectVMFailsFastWhileHeld pins the same-process guard: a second
// blocking acquire of a held project-VM lock errors instead of deadlocking.
func TestLockProjectVMFailsFastWhileHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, _, err := lockProjectVM(k, true)
	if err != nil {
		t.Fatalf("lockProjectVM: %v", err)
	}
	defer release()

	if _, ok, err := lockProjectVM(k, true); err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("second blocking acquire = (%v, %v), want an already-held error", ok, err)
	} else if ok {
		t.Fatal("a failed acquire must report ok=false")
	}
}

// TestLockProjectVMFlockContention pins the non-blocking contract callers rely
// on: contention reports ok=false, not an error.
func TestLockProjectVMFlockContention(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, _, err := lockProjectVM(k, true)
	if err != nil {
		t.Fatalf("lockProjectVM: %v", err)
	}
	defer release()

	_, ok, err := lockProjectVM(k, false)
	if err != nil {
		t.Fatalf("non-blocking acquire: %v", err)
	}
	if ok {
		t.Fatal("non-blocking acquire must report contention while the lock is held")
	}
}

// TestLockProjectVMNonBlockingSucceedsWhenFree pins the non-blocking success
// path (and its release).
func TestLockProjectVMNonBlockingSucceedsWhenFree(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	release, ok, err := lockProjectVM(k, false)
	if err != nil {
		t.Fatalf("lockProjectVM: %v", err)
	}
	if !ok {
		t.Fatal("non-blocking acquire must succeed when the lock is free")
	}
	release()
}

func TestLockProjectVMMkdirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirProjectVMLock
	mkdirProjectVMLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirProjectVMLock = orig })

	_, ok, err := lockProjectVM(testKey(), true)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "create lock dir") {
		t.Fatalf("lockProjectVM() = %v, want a create-lock-dir error wrapping %v", err, wantErr)
	}
	if ok {
		t.Fatal("a failed acquire must report ok=false")
	}
}

func TestLockProjectVMOpenError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	wantErr := errors.New("open boom")
	orig := openProjectVMLock
	openProjectVMLock = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openProjectVMLock = orig })

	_, ok, err := lockProjectVM(k, true)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "open project VM lock") {
		t.Fatalf("lockProjectVM() = %v, want an open error wrapping %v", err, wantErr)
	}
	if ok {
		t.Fatal("a failed acquire must report ok=false")
	}
	// The open failure released the registry entry the blocking acquire
	// registered, so a later acquire of the same path is not a false "already
	// held".
	path := filepath.Join(ProjectVMLockDir(k), ProjectVMLockName)
	if err := holdLockPath(path); err != nil {
		t.Fatalf("a failed open leaked its registry entry: %v", err)
	}
	releaseLockPath(path)
}

func TestLockProjectVMFlockErrorReleasesRegistry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	wantErr := errors.New("flock boom")
	orig := flockProjectVMLock
	flockProjectVMLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockProjectVMLock = orig })

	if _, ok, err := lockProjectVM(k, true); !errors.Is(err, wantErr) {
		t.Fatalf("lockProjectVM() = %v, want %v", err, wantErr)
	} else if ok {
		t.Fatal("a failed acquire must report ok=false")
	}

	path := filepath.Join(ProjectVMLockDir(k), ProjectVMLockName)
	if err := holdLockPath(path); err != nil {
		t.Fatalf("a failed acquire leaked its registry entry: %v", err)
	}
	releaseLockPath(path)
}

// TestLockProjectVMUsesPrivateModes pins the lock dir and file modes at the
// seam, so the assertion does not depend on the test process's umask.
func TestLockProjectVMUsesPrivateModes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	var dirMode, fileMode os.FileMode
	origMkdir, origOpen := mkdirProjectVMLock, openProjectVMLock
	mkdirProjectVMLock = func(dir string, mode os.FileMode) error {
		dirMode = mode
		return os.MkdirAll(dir, mode)
	}
	openProjectVMLock = func(path string, flag int, mode os.FileMode) (*os.File, error) {
		fileMode = mode
		return os.OpenFile(path, flag, mode)
	}
	t.Cleanup(func() { mkdirProjectVMLock, openProjectVMLock = origMkdir, origOpen })

	release, _, err := lockProjectVM(testKey(), false)
	if err != nil {
		t.Fatalf("lockProjectVM: %v", err)
	}
	release()

	if dirMode != 0o700 {
		t.Errorf("lock dir mode = %o, want 700", dirMode)
	}
	if fileMode != 0o600 {
		t.Errorf("lock file mode = %o, want 600", fileMode)
	}
}

// TestLockProjectVMBlockingWaitsForFlock pins blocking=true to a blocking flock:
// while another open file description holds the lock, the acquire must wait, not
// report contention.
func TestLockProjectVMBlockingWaitsForFlock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()
	dir := ProjectVMLockDir(k)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(filepath.Join(dir, ProjectVMLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := hostlock.Exclusive(holder); err != nil {
		_ = holder.Close()
		t.Fatalf("hold raw flock: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		release, _, lockErr := lockProjectVM(k, true)
		if lockErr == nil {
			release()
		}
		result <- lockErr
	}()

	// The lock is still held, so a blocking acquire must not return yet.
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

// TestLockProjectVMBlockingContentionErrorIsSurfaced pins that a contention error
// on a blocking acquire is a failure, not the non-blocking "lock is busy"
// signal. The injected flock stands in for a real flock that returned EWOULDBLOCK.
func TestLockProjectVMBlockingContentionErrorIsSurfaced(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	orig := flockProjectVMLock
	flockProjectVMLock = func(*os.File, bool) error { return syscall.EWOULDBLOCK }
	t.Cleanup(func() { flockProjectVMLock = orig })

	if _, ok, err := lockProjectVM(testKey(), true); err == nil {
		t.Fatalf("blocking acquire with a contention error = (ok=%v, err=nil), want an error", ok)
	} else if ok {
		t.Fatal("a failed acquire must report ok=false")
	}
}

// TestLockProjectVMNonBlockingRealErrorIsSurfaced pins that a non-contention
// flock failure on a non-blocking acquire is an error, not the "lock is busy"
// signal.
func TestLockProjectVMNonBlockingRealErrorIsSurfaced(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("flock boom")
	orig := flockProjectVMLock
	flockProjectVMLock = func(*os.File, bool) error { return wantErr }
	t.Cleanup(func() { flockProjectVMLock = orig })

	if _, ok, err := lockProjectVM(testKey(), false); !errors.Is(err, wantErr) {
		t.Fatalf("non-blocking acquire = (ok=%v, err=%v), want %v", ok, err, wantErr)
	} else if ok {
		t.Fatal("a failed acquire must report ok=false")
	}
}
