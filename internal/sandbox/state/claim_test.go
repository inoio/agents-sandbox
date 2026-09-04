package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestAcquireClaimExclusivePerKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "claimproj-a"

	release1, claimed1, err := AcquireClaim(slug, "keyA")
	if err != nil || !claimed1 {
		t.Fatalf("first acquire: claimed=%v err=%v", claimed1, err)
	}
	defer release1()

	// A second acquire of the same key must lose (claimed=false, no release).
	release2, claimed2, err := AcquireClaim(slug, "keyA")
	if err != nil {
		t.Fatalf("second acquire err=%v", err)
	}
	if claimed2 {
		t.Fatal("second acquire of same key should not claim")
	}
	if release2 != nil {
		t.Fatal("non-claiming acquire should return nil release")
	}

	// A different key claims independently.
	release3, claimed3, err := AcquireClaim(slug, "keyB")
	if err != nil || !claimed3 {
		t.Fatalf("keyB acquire: claimed=%v err=%v", claimed3, err)
	}
	defer release3()

	// Releasing the first owner frees the key for a later caller.
	release1()
	release4, claimed4, err := AcquireClaim(slug, "keyA")
	if err != nil || !claimed4 {
		t.Fatalf("re-acquire after release: claimed=%v err=%v", claimed4, err)
	}
	release4()
}

func TestAcquireClaimAbandonedFileReclaimable(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "claimproj-c"

	// Simulate a crashed holder: create the claim file with no live flock.
	dir := filepath.Join(stateRoot(), slug, "claims")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.claim"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	release, claimed, err := AcquireClaim(slug, "stale")
	if err != nil {
		t.Fatalf("acquire stale: %v", err)
	}
	if !claimed {
		t.Fatal("abandoned claim should be reclaimable")
	}
	release()
}

func TestAcquireClaimReclaimableAfterReleaseWithFilePresent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "claimproj-b"

	release1, claimed1, err := AcquireClaim(slug, "keyX")
	if err != nil || !claimed1 {
		t.Fatalf("first acquire: claimed=%v err=%v", claimed1, err)
	}

	// Release frees the flock but leaves the claim file in place.
	release1()
	dir := filepath.Join(stateRoot(), slug, "claims")
	if _, err := os.Stat(filepath.Join(dir, "keyX.claim")); err != nil {
		t.Fatalf("claim file should remain after release: %v", err)
	}

	// An un-removed file with a freed flock must be reclaimable.
	release2, claimed2, err := AcquireClaim(slug, "keyX")
	if err != nil || !claimed2 {
		t.Fatalf("re-acquire after release: claimed=%v err=%v", claimed2, err)
	}
	release2()
}

func TestAcquireClaim_MkdirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "claimmkdirproj"
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A file where the claims dir should live makes MkdirAll fail.
	testutil.WritePath(t, filepath.Join(sdir, "claims"), "not a directory")

	if _, _, err := AcquireClaim(slug, "key"); err == nil {
		t.Fatal("expected error when the claims dir cannot be created")
	}
}

func TestAcquireClaim_OpenFileError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("cannot simulate an unwritable dir as root")
	}
	configpaths.WithMockConfigPaths(t)
	slug := "claimroproj"
	dir := filepath.Join(configpaths.Get().UserStateDir(), slug, "claims")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)

	if _, _, err := AcquireClaim(slug, "key"); err == nil {
		t.Fatal("expected error when the claim file cannot be opened")
	}
}
