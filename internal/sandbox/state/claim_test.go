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

func TestAcquireClaimReleaseRemovesFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "claimproj-b"

	release1, claimed1, err := AcquireClaim(slug, "keyX")
	if err != nil || !claimed1 {
		t.Fatalf("first acquire: claimed=%v err=%v", claimed1, err)
	}

	// Release removes the claim file so tokens do not accumulate.
	release1()
	dir := filepath.Join(stateRoot(), slug, "claims")
	if _, err := os.Stat(filepath.Join(dir, "keyX.claim")); !os.IsNotExist(err) {
		t.Fatalf("release should remove the claim file, stat err = %v", err)
	}

	// The key is free to claim again afterwards.
	release2, claimed2, err := AcquireClaim(slug, "keyX")
	if err != nil || !claimed2 {
		t.Fatalf("re-acquire after release: claimed=%v err=%v", claimed2, err)
	}
	release2()
}

func TestAcquireClaimFlockErrorSurfaces(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	claimOpened = func(f *os.File, _ string) { _ = f.Close() }
	t.Cleanup(func() { claimOpened = nil })

	if _, _, err := AcquireClaim("claimflockproj", "key"); err == nil {
		t.Fatal("expected a non-contention flock error to surface")
	}
}

func TestAcquireClaimRetriesWhenFileRemovedUnderIt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	attempts := 0
	claimOpened = func(_ *os.File, path string) {
		attempts++
		_ = os.Remove(path)
	}
	t.Cleanup(func() { claimOpened = nil })

	_, claimed, err := AcquireClaim("claimraceproj", "key")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if claimed {
		t.Fatal("a file removed under every attempt must not hand out a claim")
	}
	if attempts != 5 {
		t.Fatalf("expected 5 attempts before giving up, got %d", attempts)
	}
}

func TestSameFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if !sameFile(f, path) {
		t.Fatal("an open file should match its path")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if sameFile(f, path) {
		t.Fatal("a removed path should not match")
	}
	_ = f.Close()

	// A path that exists but whose open file is closed: stat of the fd fails.
	again, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
	if sameFile(f, path) {
		t.Fatal("a closed file descriptor should not match")
	}
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
