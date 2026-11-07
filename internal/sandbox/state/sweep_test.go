package state

import (
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

func TestRemoveSlugDirIfEmptyKeepsNonEmpty(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// A non-empty slug dir stays.
	full := SlugDir("sweep-dir-full")
	if err := os.MkdirAll(filepath.Join(full, "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSlugDirIfEmpty("sweep-dir-full"); err != nil {
		t.Fatalf("RemoveSlugDirIfEmpty on a non-empty dir: %v", err)
	}
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("non-empty slug dir should be kept, stat err = %v", err)
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
