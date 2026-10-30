package state

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestSortedUniqueKeysOrdersBySlugThenAgent(t *testing.T) {
	got := sortedUniqueKeys([]Key{
		{Slug: "b", Agent: "z"},
		{Slug: "a", Agent: "m"},
		{Slug: "b", Agent: "a"},
		{Slug: "a", Agent: "m"},
	})
	want := []Key{{Slug: "a", Agent: "m"}, {Slug: "b", Agent: "a"}, {Slug: "b", Agent: "z"}}
	if !slices.Equal(got, want) {
		t.Fatalf("sortedUniqueKeys() = %v, want %v", got, want)
	}
}

func TestSortedUniqueKeysDoesNotMutateInput(t *testing.T) {
	in := []Key{{Slug: "b", Agent: "x"}, {Slug: "a", Agent: "x"}}
	original := slices.Clone(in)
	_ = sortedUniqueKeys(in)
	if !slices.Equal(in, original) {
		t.Fatalf("sortedUniqueKeys mutated its input: %v, want %v", in, original)
	}
}

func TestWithKeyLocksEmptyIsNoOp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	called := false
	if err := WithKeyLocks(nil, func() error { called = true; return nil }); err != nil {
		t.Fatalf("WithKeyLocks(nil): %v", err)
	}
	if !called {
		t.Fatal("WithKeyLocks must run fn for an empty key set")
	}
}

func TestWithKeyLocksHoldsHomeAndProjectVMLocks(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testKey()

	if err := WithKeyLocks([]Key{k}, func() error {
		if _, ok, err := lockHomeState(k, false); err != nil || ok {
			t.Errorf("home lock not held during fn: ok=%v err=%v", ok, err)
		}
		if _, ok, err := lockProjectVM(k, false); err != nil || ok {
			t.Errorf("project-VM lock not held during fn: ok=%v err=%v", ok, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithKeyLocks: %v", err)
	}

	releaseHome, _, err := lockHomeState(k, true)
	if err != nil {
		t.Fatalf("home lock leaked after WithKeyLocks: %v", err)
	}
	releaseHome()
	releaseVM, _, err := lockProjectVM(k, true)
	if err != nil {
		t.Fatalf("project-VM lock leaked after WithKeyLocks: %v", err)
	}
	releaseVM()
}

func TestWithKeyLocksReleasesOnHomeAcquireFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirHomeLock
	mkdirHomeLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirHomeLock = orig })

	if err := WithKeyLocks([]Key{testKey()}, func() error {
		t.Fatal("fn must not run when an acquire fails")
		return nil
	}); !errors.Is(err, wantErr) {
		t.Fatalf("WithKeyLocks() = %v, want %v", err, wantErr)
	}
}

func TestWithKeyLocksReleasesEarlierKeysOnLaterFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	first := Key{Slug: "a", Agent: "opencode"}
	second := Key{Slug: "b", Agent: "opencode"}
	wantErr := errors.New("mkdir boom")
	orig := mkdirProjectVMLock
	mkdirProjectVMLock = func(dir string, mode os.FileMode) error {
		if dir == ProjectVMLockDir(second) {
			return wantErr
		}
		return os.MkdirAll(dir, mode)
	}
	t.Cleanup(func() { mkdirProjectVMLock = orig })

	if err := WithKeyLocks([]Key{first, second}, func() error {
		t.Fatal("fn must not run when an acquire fails")
		return nil
	}); !errors.Is(err, wantErr) {
		t.Fatalf("WithKeyLocks() = %v, want %v", err, wantErr)
	}

	// The first key's locks were released: re-acquiring them must succeed.
	releaseHome, _, err := lockHomeState(first, true)
	if err != nil {
		t.Fatalf("first key's home lock leaked: %v", err)
	}
	releaseHome()
	releaseVM, _, err := lockProjectVM(first, true)
	if err != nil {
		t.Fatalf("first key's project-VM lock leaked: %v", err)
	}
	releaseVM()
}

// TestWithKeyLocksReleasesEarlierKeysOnHomeFailure pins the home-acquire failure
// path's release: when a later key's home acquire fails, the earlier key's locks
// are released rather than leaked.
func TestWithKeyLocksReleasesEarlierKeysOnHomeFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	first := Key{Slug: "a", Agent: "opencode"}
	second := Key{Slug: "b", Agent: "opencode"}
	wantErr := errors.New("mkdir boom")
	orig := mkdirHomeLock
	mkdirHomeLock = func(dir string, mode os.FileMode) error {
		if dir == filepath.Dir(homeLockPath(second)) {
			return wantErr
		}
		return os.MkdirAll(dir, mode)
	}
	t.Cleanup(func() { mkdirHomeLock = orig })

	if err := WithKeyLocks([]Key{first, second}, func() error {
		t.Fatal("fn must not run when an acquire fails")
		return nil
	}); !errors.Is(err, wantErr) {
		t.Fatalf("WithKeyLocks() = %v, want %v", err, wantErr)
	}

	releaseHome, _, err := lockHomeState(first, true)
	if err != nil {
		t.Fatalf("first key's home lock leaked: %v", err)
	}
	releaseHome()
	releaseVM, _, err := lockProjectVM(first, true)
	if err != nil {
		t.Fatalf("first key's project-VM lock leaked: %v", err)
	}
	releaseVM()
}

func TestWithKeyLocksPropagatesFnError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("boom")
	if err := WithKeyLocks([]Key{testKey()}, func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("WithKeyLocks() = %v, want %v", err, wantErr)
	}
}
