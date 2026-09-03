package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
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
