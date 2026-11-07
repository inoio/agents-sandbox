package main

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/inoio/agents-sandbox/internal/git"
)

func TestMemoizeIdentityResolvesOncePerPath(t *testing.T) {
	calls := map[string]int{}
	base := func(cwd string) (git.Identity, error) {
		calls[cwd]++
		return git.Identity{RepoSlug: "slug-" + cwd}, nil
	}
	memo := memoizeIdentity(base)
	dirA := t.TempDir()
	dirB := t.TempDir()

	first, err := memo(dirA)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	second, err := memo(dirA)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if first.RepoSlug != second.RepoSlug {
		t.Fatalf("memoized value changed: %q vs %q", first.RepoSlug, second.RepoSlug)
	}
	if _, err := memo(dirB); err != nil {
		t.Fatalf("resolve dirB: %v", err)
	}
	if calls[dirA] != 1 {
		t.Errorf("base called %d times for the same path, want 1", calls[dirA])
	}
	if calls[dirB] != 1 {
		t.Errorf("base called %d times for a distinct path, want 1", calls[dirB])
	}
}

func TestMemoizeIdentityCachesTheError(t *testing.T) {
	wantErr := errors.New("unresolvable")
	calls := 0
	base := func(string) (git.Identity, error) { calls++; return git.Identity{}, wantErr }
	memo := memoizeIdentity(base)
	dir := t.TempDir()

	if _, err := memo(dir); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if _, err := memo(dir); !errors.Is(err, wantErr) {
		t.Fatalf("second err = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Errorf("base called %d times for a cached error, want 1", calls)
	}
}

// memoizeIdentity caches base's result per resolved working directory. The
// preflight passes the literal ".", so keying on the argument would collapse
// every chdir test into one entry; filepath.Abs anchors the key to the real
// directory at call time.
func memoizeIdentity(base func(string) (git.Identity, error)) func(string) (git.Identity, error) {
	type result struct {
		identity git.Identity
		err      error
	}
	var (
		mutex  sync.Mutex
		cached = map[string]result{}
	)
	return func(cwd string) (git.Identity, error) {
		key, err := filepath.Abs(cwd)
		if err != nil {
			return base(cwd)
		}
		mutex.Lock()
		defer mutex.Unlock()
		if got, ok := cached[key]; ok {
			return got.identity, got.err
		}
		identity, resolveErr := base(cwd)
		cached[key] = result{identity: identity, err: resolveErr}
		return identity, resolveErr
	}
}
