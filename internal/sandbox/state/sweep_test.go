package state

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestProjectSlugsExcludesLocksAndFiles(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	root := stateRoot()
	for _, slug := range []string{"zeta-project", "alpha-project", "mid-project"} {
		if err := os.MkdirAll(filepath.Join(root, slug), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(locksDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	// The launcher-global root state file is a non-directory entry under the
	// root and must not be listed as a project slug.
	testutil.WritePath(t, filepath.Join(root, "state.yaml"), "root")

	slugs, err := ProjectSlugs()
	if err != nil {
		t.Fatalf("ProjectSlugs: %v", err)
	}
	want := []string{"alpha-project", "mid-project", "zeta-project"}
	if !slices.Equal(slugs, want) {
		t.Fatalf("ProjectSlugs = %v, want %v", slugs, want)
	}
}

func TestProjectSlugsEmptyRoot(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slugs, err := ProjectSlugs()
	if err != nil {
		t.Fatalf("ProjectSlugs on an empty root: %v", err)
	}
	if len(slugs) != 0 {
		t.Fatalf("ProjectSlugs = %v, want empty", slugs)
	}
}

func TestClaimTokensReturnsSettledTokenNames(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	dir := filepath.Join(SlugDir("sweep-claims"), "claims")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"zeta.claim", "alpha.claim", "notes.txt"} {
		testutil.WritePath(t, filepath.Join(dir, name), "")
	}
	// A directory entry is not a claim token.
	if err := os.MkdirAll(filepath.Join(dir, "subdir.claim"), 0o700); err != nil {
		t.Fatal(err)
	}

	tokens, err := ClaimTokens("sweep-claims")
	if err != nil {
		t.Fatalf("ClaimTokens: %v", err)
	}
	want := []string{"alpha", "zeta"}
	if !slices.Equal(tokens, want) {
		t.Fatalf("ClaimTokens = %v, want %v", tokens, want)
	}

	// A missing claims dir yields an empty list, not an error.
	tokens, err = ClaimTokens("sweep-claims-missing")
	if err != nil {
		t.Fatalf("ClaimTokens on a missing dir: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("ClaimTokens(missing) = %v, want empty", tokens)
	}
}

func TestRemoveUnheldClaimRemovesFreeFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "sweep-claim-free"

	dir := filepath.Join(SlugDir(slug), "claims")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	claimPath := filepath.Join(dir, "token.claim")
	testutil.WritePath(t, claimPath, "")

	removed, err := RemoveUnheldClaim(slug, "token")
	if err != nil {
		t.Fatalf("RemoveUnheldClaim: %v", err)
	}
	if !removed {
		t.Fatal("a free claim file must be removed")
	}
	if _, err := os.Stat(claimPath); !os.IsNotExist(err) {
		t.Fatalf("claim file should be gone, stat err = %v", err)
	}
}

func TestRemoveUnheldClaimKeepsHeldFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "sweep-claim-held"

	release, claimed, err := AcquireClaim(slug, "token")
	if err != nil || !claimed {
		t.Fatalf("AcquireClaim: claimed=%v err=%v", claimed, err)
	}
	defer release()

	removed, err := RemoveUnheldClaim(slug, "token")
	if err != nil {
		t.Fatalf("RemoveUnheldClaim: %v", err)
	}
	if removed {
		t.Fatal("a held claim must not be removed")
	}
	if _, err := os.Stat(filepath.Join(SlugDir(slug), "claims", "token.claim")); err != nil {
		t.Fatalf("held claim file should remain, stat err = %v", err)
	}
}

// TestRemoveUnheldClaimRetriesWhenFileReplacedUnderIt pins the sameFile guard
// RemoveUnheldClaim shares with AcquireClaim: a claim removed between the open
// and the lock must not be removed blindly (the path may now name a different,
// possibly held, claim), so the sweep retries on the current file.
func TestRemoveUnheldClaimRetriesWhenFileReplacedUnderIt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	attempts := 0
	claimOpened = func(_ *os.File, path string) {
		attempts++
		if attempts == 1 {
			_ = os.Remove(path)
		}
	}
	t.Cleanup(func() { claimOpened = nil })

	removed, err := RemoveUnheldClaim("sweep-claim-race", "token")
	if err != nil {
		t.Fatalf("RemoveUnheldClaim: %v", err)
	}
	if !removed {
		t.Fatal("a replaced claim file must be retried and removed")
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts (one replacement, then success), got %d", attempts)
	}
}

func TestRemoveUnheldClaimGivesUpWhenReplacedEveryAttempt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	attempts := 0
	claimOpened = func(_ *os.File, path string) {
		attempts++
		_ = os.Remove(path)
	}
	t.Cleanup(func() { claimOpened = nil })

	removed, err := RemoveUnheldClaim("sweep-claim-race-every", "token")
	if err != nil {
		t.Fatalf("RemoveUnheldClaim: %v", err)
	}
	if removed {
		t.Fatal("a claim replaced on every attempt must not be reported removed")
	}
	if attempts != 5 {
		t.Fatalf("expected 5 attempts before giving up, got %d", attempts)
	}
}

func TestSweepClientLeasesRemovesOnlyStaleFreeLeases(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "sweep-leases-proj", Agent: "opencode"}
	clientsDir := filepath.Join(KeyDir(k), "clients")
	if err := os.MkdirAll(clientsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// A live lease: held by this process, must be kept.
	release, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	// A fresh unlocked lease: a client between create and flock, must be kept.
	freshPath := filepath.Join(clientsDir, "11111-3000000000.lock")
	fresh, err := os.OpenFile(freshPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })

	// A stale unlocked lease: a crashed client's leftover, must be removed.
	stalePath := filepath.Join(clientsDir, "22222-4000000000.lock")
	testutil.WritePath(t, stalePath, "stale")
	aged := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stalePath, aged, aged); err != nil {
		t.Fatal(err)
	}

	if got := SweepClientLeases(k); got != 1 {
		t.Fatalf("SweepClientLeases = %d, want 1", got)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale lease should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("fresh lease should be kept, stat err = %v", err)
	}
	// The held and mid-create leases both still count as live.
	if got := CountActiveClients(k); got != 2 {
		t.Fatalf("CountActiveClients after sweep = %d, want 2", got)
	}
}

func TestSweepClientLeasesMissingDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if got := SweepClientLeases(Key{Slug: "sweep-nodir", Agent: "opencode"}); got != 0 {
		t.Fatalf("SweepClientLeases = %d, want 0 for a missing clients dir", got)
	}
}

func TestRemoveSlugDirIfEmptyKeepsNonEmptyAndReclaimsEmptyShells(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// A slug dir whose subtree holds a file stays, and the empty sibling shell
	// beneath it is reclaimed.
	full := SlugDir("sweep-dir-full")
	if err := os.MkdirAll(filepath.Join(full, "opencode", "clients"), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(full, "opencode", "state.yaml"), "schema: 1\n")
	if err := RemoveSlugDirIfEmpty("sweep-dir-full"); err != nil {
		t.Fatalf("RemoveSlugDirIfEmpty on a non-empty dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(full, "opencode", "state.yaml")); err != nil {
		t.Fatalf("the state file should be kept, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(full, "opencode", "clients")); !os.IsNotExist(err) {
		t.Fatalf("the emptied client-lease dir should be removed, stat err = %v", err)
	}

	// A slug dir holding only empty child dirs is removed bottom-up.
	shells := SlugDir("sweep-dir-shells")
	if err := os.MkdirAll(filepath.Join(shells, "opencode", "clients"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSlugDirIfEmpty("sweep-dir-shells"); err != nil {
		t.Fatalf("RemoveSlugDirIfEmpty on an empty-subtree dir: %v", err)
	}
	if _, err := os.Stat(shells); !os.IsNotExist(err) {
		t.Fatalf("a slug dir of empty shells should be removed, stat err = %v", err)
	}

	// An empty slug dir is removed.
	empty := SlugDir("sweep-dir-empty")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSlugDirIfEmpty("sweep-dir-empty"); err != nil {
		t.Fatalf("RemoveSlugDirIfEmpty on an empty dir: %v", err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatalf("empty slug dir should be removed, stat err = %v", err)
	}

	// A missing slug dir is a no-op.
	if err := RemoveSlugDirIfEmpty("sweep-dir-missing"); err != nil {
		t.Fatalf("RemoveSlugDirIfEmpty on a missing dir: %v", err)
	}
}

func TestStateFileModTime(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "sweep-modtime", Agent: "opencode"}
	path := filepath.Join(KeyDir(k), "state.yaml")
	if err := os.MkdirAll(KeyDir(k), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, path, "schema: 1\n")
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatal(err)
	}

	got, err := StateFileModTime(k)
	if err != nil {
		t.Fatalf("StateFileModTime: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("StateFileModTime = %v, want %v", got, want)
	}

	// A missing state file is ErrStateNotFound, distinguishing absence from a
	// stat failure.
	if _, err := StateFileModTime(Key{Slug: "sweep-modtime-missing"}); !errors.Is(err, ErrStateNotFound) {
		t.Errorf("StateFileModTime on a missing file error = %v, want ErrStateNotFound", err)
	}
}

// TestProjectSlugsMissingRoot pins that absence is the empty list, not an error:
// a fresh install has no state root yet. It uses the real resolver so the state
// dir is not eagerly created, as the mock resolver's accessors do.
func TestProjectSlugsMissingRoot(t *testing.T) {
	configpaths.WithRealConfigPaths(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	slugs, err := ProjectSlugs()
	if err != nil {
		t.Fatalf("ProjectSlugs on a missing root: %v", err)
	}
	if len(slugs) != 0 {
		t.Errorf("ProjectSlugs = %v, want none", slugs)
	}
}

// TestProjectSlugsReadError pins the fail-closed root read: an unreadable state
// root is an error, never a silently empty project list.
func TestProjectSlugsReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	root := stateRoot()
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, root, "not a directory")

	if _, err := ProjectSlugs(); err == nil {
		t.Fatal("ProjectSlugs on a file root should fail closed")
	}
}

// TestStateFileModTimeStatError pins that a path component that is not a
// directory surfaces as an error rather than being mistaken for absence.
func TestStateFileModTimeStatError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(stateRoot(), "sweep-modtime-file"), "file")

	if _, err := StateFileModTime(Key{Slug: "sweep-modtime-file", Agent: "opencode"}); err == nil {
		t.Fatal("StateFileModTime with a non-directory slug should surface the stat error")
	}
}

// TestClaimTokensReadError pins the fail-closed claim read: a claims path that
// is not a directory is an error, not an empty token list.
func TestClaimTokensReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "sweep-claims-file"
	if err := os.MkdirAll(SlugDir(slug), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(SlugDir(slug), "claims"), "file")

	if _, err := ClaimTokens(slug); err == nil {
		t.Fatal("ClaimTokens on a file claims path should surface the read error")
	}
}

// TestRemoveUnheldClaimMkdirError pins the claims-dir creation failure: a slug
// path that is a file cannot hold a claims dir.
func TestRemoveUnheldClaimMkdirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "sweep-claim-mkdir"
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, SlugDir(slug), "file")

	if _, err := RemoveUnheldClaim(slug, "token"); err == nil {
		t.Fatal("RemoveUnheldClaim with a file slug path should surface the mkdir error")
	}
}

// TestRemoveUnheldClaimOpenError pins the open failure: a claim path that is a
// directory cannot be opened as a claim file.
func TestRemoveUnheldClaimOpenError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "sweep-claim-open"
	if err := os.MkdirAll(filepath.Join(SlugDir(slug), "claims", "token.claim"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveUnheldClaim(slug, "token"); err == nil {
		t.Fatal("RemoveUnheldClaim with a directory claim path should surface the open error")
	}
}

// TestRemoveUnheldClaimFlockError pins the non-contention flock failure: a
// closed probe file cannot be locked, and the error must surface rather than be
// taken for a held claim.
func TestRemoveUnheldClaimFlockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	claimOpened = func(f *os.File, _ string) { _ = f.Close() }
	t.Cleanup(func() { claimOpened = nil })

	if _, err := RemoveUnheldClaim("sweep-claim-flock", "token"); err == nil {
		t.Fatal("RemoveUnheldClaim should surface a non-contention flock error")
	}
}

// TestRemoveUnheldClaimRemoveError pins the unlink failure: a claim file in a
// read-only claims dir is locked but cannot be removed, and the error surfaces.
func TestRemoveUnheldClaimRemoveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "sweep-claim-remove"
	claimsDir := filepath.Join(SlugDir(slug), "claims")
	if err := os.MkdirAll(claimsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(claimsDir, "token.claim"), "")
	if err := os.Chmod(claimsDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(claimsDir, 0o700) })

	if _, err := RemoveUnheldClaim(slug, "token"); err == nil {
		t.Fatal("RemoveUnheldClaim should surface a failed unlink")
	}
}

// TestRemoveSlugDirIfEmptyReadError pins the fail-closed slug read: a slug path
// that is a file surfaces the read error.
func TestRemoveSlugDirIfEmptyReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "sweep-dir-file"
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, SlugDir(slug), "file")

	if err := RemoveSlugDirIfEmpty(slug); err == nil {
		t.Fatal("RemoveSlugDirIfEmpty on a file should surface the read error")
	}
}

// TestRemoveSlugDirIfEmptyRemoveError pins the bottom-up unlink failure: an
// empty child dir in a read-only slug dir cannot be removed, and the error
// propagates from the child.
func TestRemoveSlugDirIfEmptyRemoveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "sweep-dir-readonly"
	child := filepath.Join(SlugDir(slug), "opencode")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(SlugDir(slug), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(SlugDir(slug), 0o700) })

	if err := RemoveSlugDirIfEmpty(slug); err == nil {
		t.Fatal("RemoveSlugDirIfEmpty should surface a failed child removal")
	}
}
