package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestAcquireClientLease(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "testproj-aBc1234D"

	// Acquire first lease.
	release, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	// Lock file should exist.
	entries, err := os.ReadDir(filepath.Join(stateRoot(), slug, "clients"))
	if err != nil {
		t.Fatalf("ReadDir clients: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 lock file after acquire, got %d", len(entries))
	}

	// CountActiveClients should report 1.
	if got := CountActiveClients(slug); got != 1 {
		t.Errorf("CountActiveClients = %d, want 1", got)
	}

	// Counting twice still returns 1 (idempotent).
	if got := CountActiveClients(slug); got != 1 {
		t.Errorf("second CountActiveClients = %d, want 1", got)
	}

	// Release the lease.
	release()

	// Lock file should be gone.
	entries, err = os.ReadDir(filepath.Join(stateRoot(), slug, "clients"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir clients after release: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 lock files after release, got %d: %v", len(entries), entries)
	}

	// CountActiveClients should report 0 after release.
	if got := CountActiveClients(slug); got != 0 {
		t.Errorf("CountActiveClients after release = %d, want 0", got)
	}
}

func TestAcquireClientLease_DirectoriesCreated(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "newslug-x7y9z"

	// The clients dir and slug dir should not exist yet.
	stateRootDir := filepath.Join(stateRoot(), slug)
	if _, err := os.Stat(stateRootDir); !os.IsNotExist(err) {
		t.Fatalf("slug dir should not exist yet")
	}

	release, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	release()

	// State root and clients dir should exist.
	if _, err := os.Stat(stateRootDir); err != nil {
		t.Fatalf("slug dir should exist after AcquireClientLease: %v", err)
	}
	clientsDir := filepath.Join(stateRootDir, "clients")
	if _, err := os.Stat(clientsDir); err != nil {
		t.Fatalf("clients dir should exist after AcquireClientLease: %v", err)
	}
}

func TestAcquireClientLease_MultipleClients(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "multiproj-a"

	r1, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease #1: %v", err)
	}
	r2, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease #2: %v", err)
	}
	r3, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease #3: %v", err)
	}

	if got := CountActiveClients(slug); got != 3 {
		t.Fatalf("CountActiveClients = %d, want 3", got)
	}

	// Release one.
	r1()
	if got := CountActiveClients(slug); got != 2 {
		t.Fatalf("after 1 release: CountActiveClients = %d, want 2", got)
	}

	// Release another.
	r2()
	if got := CountActiveClients(slug); got != 1 {
		t.Fatalf("after 2 releases: CountActiveClients = %d, want 1", got)
	}

	// Release last.
	r3()
	if got := CountActiveClients(slug); got != 0 {
		t.Fatalf("after 3 releases: CountActiveClients = %d, want 0", got)
	}
}

func TestCountActiveClients_CleansStaleLockFiles(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "staleproj-b"

	// Create the clients directory manually.
	clientsDir := filepath.Join(stateRoot(), slug, "clients")
	if err := os.MkdirAll(clientsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Create a stale lock file with no holder (just a file, no flock).
	stalePath := filepath.Join(clientsDir, "12345-1000000000.lock")
	testutil.WritePath(t, stalePath, "stale")

	// CountActiveClients should clean up the stale file.
	count := CountActiveClients(slug)
	if count != 0 {
		t.Fatalf("CountActiveClients = %d, want 0 for stale-only dir", count)
	}

	// The stale file should be removed.
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale lock file should be removed, but still exists: %v", err)
	}
}

func TestAcquireClientLease_NonExistentSlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "nonexistent-slug-xyz"
	release, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease should succeed for non-existent slug: %v", err)
	}
	release()
}

func TestCountActiveClients_NoClientsDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "nodir-proj"
	if got := CountActiveClients(slug); got != 0 {
		t.Errorf("CountActiveClients = %d, want 0 when clients dir is absent", got)
	}
}

func TestCountActiveClients_SkipsUnopenableEntry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "subdir-proj"
	clientsDir := filepath.Join(stateRoot(), slug, "clients")
	if err := os.MkdirAll(filepath.Join(clientsDir, "a-subdir"), 0o700); err != nil {
		t.Fatal(err)
	}

	// A directory entry cannot be flock'd and must be skipped, not counted
	// and not removed.
	if got := CountActiveClients(slug); got != 0 {
		t.Errorf("CountActiveClients = %d, want 0 for unopenable entry", got)
	}
	if _, err := os.Stat(filepath.Join(clientsDir, "a-subdir")); err != nil {
		t.Fatalf("subdirectory entry should not be removed, got %v", err)
	}
}

func TestAcquireClientLease_MkdirFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Place a file where the slug dir would live so MkdirAll fails.
	slug := "blocked-proj"
	slugPath := filepath.Join(stateRoot(), slug)
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, slugPath, "not a directory")

	if _, err := AcquireClientLease(slug); err == nil {
		t.Fatal("AcquireClientLease should fail when the client lock dir cannot be created")
	}
}

func TestAcquireClientLease_MultipleFromSameProcess(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "testproj-z"

	r1, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease #1: %v", err)
	}

	// We should be able to acquire a second one (paths are unique).
	r2, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease #2: %v", err)
	}

	// Both active.
	if got := CountActiveClients(slug); got != 2 {
		t.Fatalf("CountActiveClients = %d, want 2", got)
	}

	r1()
	r2()

	if got := CountActiveClients(slug); got != 0 {
		t.Fatalf("after both releases: CountActiveClients = %d, want 0", got)
	}
}
