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

	k := Key{Slug: "testproj-aBc1234D", Agent: "opencode"}

	// Acquire first lease.
	release, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	// Lock file should exist.
	entries, err := os.ReadDir(filepath.Join(stateRoot(), k.Slug, k.Agent, "clients"))
	if err != nil {
		t.Fatalf("ReadDir clients: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 lock file after acquire, got %d", len(entries))
	}

	// CountActiveClients should report 1.
	if got := CountActiveClients(k); got != 1 {
		t.Errorf("CountActiveClients = %d, want 1", got)
	}

	// Counting twice still returns 1 (idempotent).
	if got := CountActiveClients(k); got != 1 {
		t.Errorf("second CountActiveClients = %d, want 1", got)
	}

	// Release the lease.
	release()

	// Lock file should be gone.
	entries, err = os.ReadDir(filepath.Join(stateRoot(), k.Slug, k.Agent, "clients"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir clients after release: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 lock files after release, got %d: %v", len(entries), entries)
	}

	// CountActiveClients should report 0 after release.
	if got := CountActiveClients(k); got != 0 {
		t.Errorf("CountActiveClients after release = %d, want 0", got)
	}
}

func TestAcquireClientLease_DirectoriesCreated(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "newslug-x7y9z", Agent: "opencode"}

	// The key dir should not exist yet.
	stateRootDir := filepath.Join(stateRoot(), k.Slug, k.Agent)
	if _, err := os.Stat(stateRootDir); !os.IsNotExist(err) {
		t.Fatalf("key dir should not exist yet")
	}

	release, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	release()

	// State root and clients dir should exist.
	if _, err := os.Stat(stateRootDir); err != nil {
		t.Fatalf("key dir should exist after AcquireClientLease: %v", err)
	}
	clientsDir := filepath.Join(stateRootDir, "clients")
	if _, err := os.Stat(clientsDir); err != nil {
		t.Fatalf("clients dir should exist after AcquireClientLease: %v", err)
	}
}

func TestAcquireClientLease_MultipleClients(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "multiproj-a", Agent: "opencode"}

	r1, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease #1: %v", err)
	}
	r2, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease #2: %v", err)
	}
	r3, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease #3: %v", err)
	}

	if got := CountActiveClients(k); got != 3 {
		t.Fatalf("CountActiveClients = %d, want 3", got)
	}

	// Release one.
	r1()
	if got := CountActiveClients(k); got != 2 {
		t.Fatalf("after 1 release: CountActiveClients = %d, want 2", got)
	}

	// Release another.
	r2()
	if got := CountActiveClients(k); got != 1 {
		t.Fatalf("after 2 releases: CountActiveClients = %d, want 1", got)
	}

	// Release last.
	r3()
	if got := CountActiveClients(k); got != 0 {
		t.Fatalf("after 3 releases: CountActiveClients = %d, want 0", got)
	}
}

func TestCountActiveClients_CleansStaleLockFiles(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "staleproj-b", Agent: "opencode"}

	// Create the clients directory manually.
	clientsDir := filepath.Join(stateRoot(), k.Slug, k.Agent, "clients")
	if err := os.MkdirAll(clientsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Create a stale lock file with no holder (just a file, no flock).
	stalePath := filepath.Join(clientsDir, "12345-1000000000.lock")
	testutil.WritePath(t, stalePath, "stale")

	// CountActiveClients should clean up the stale file.
	count := CountActiveClients(k)
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

	k := Key{Slug: "nonexistent-slug-xyz", Agent: "opencode"}
	release, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease should succeed for non-existent slug: %v", err)
	}
	release()
}

func TestCountActiveClients_NoClientsDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "nodir-proj", Agent: "opencode"}
	if got := CountActiveClients(k); got != 0 {
		t.Errorf("CountActiveClients = %d, want 0 when clients dir is absent", got)
	}
}

func TestCountActiveClients_SkipsUnopenableEntry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "subdir-proj", Agent: "opencode"}
	clientsDir := filepath.Join(stateRoot(), k.Slug, k.Agent, "clients")
	if err := os.MkdirAll(filepath.Join(clientsDir, "a-subdir"), 0o700); err != nil {
		t.Fatal(err)
	}

	// A directory entry cannot be flock'd and must be skipped, not counted
	// and not removed.
	if got := CountActiveClients(k); got != 0 {
		t.Errorf("CountActiveClients = %d, want 0 for unopenable entry", got)
	}
	if _, err := os.Stat(filepath.Join(clientsDir, "a-subdir")); err != nil {
		t.Fatalf("subdirectory entry should not be removed, got %v", err)
	}
}

func TestAcquireClientLease_MkdirFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Place a file where the key dir would live so MkdirAll fails.
	k := Key{Slug: "blocked-proj", Agent: "opencode"}
	keyPath := filepath.Join(stateRoot(), k.Slug)
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, keyPath, "not a directory")

	if _, err := AcquireClientLease(k); err == nil {
		t.Fatal("AcquireClientLease should fail when the client lock dir cannot be created")
	}
}

func TestAcquireClientLease_OpenFileFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("cannot simulate an unwritable dir as root")
	}
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "roproj-a", Agent: "opencode"}

	clientsDir := filepath.Join(stateRoot(), k.Slug, k.Agent, "clients")
	if err := os.MkdirAll(clientsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Make the clients dir read-only so the lock file cannot be created.
	if err := os.Chmod(clientsDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(clientsDir, 0o700)

	if _, err := AcquireClientLease(k); err == nil {
		t.Fatal("expected error when the lock file cannot be opened")
	}
}

func TestAcquireClientLease_MultipleFromSameProcess(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "testproj-z", Agent: "opencode"}

	r1, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease #1: %v", err)
	}

	// We should be able to acquire a second one (paths are unique).
	r2, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease #2: %v", err)
	}

	// Both active.
	if got := CountActiveClients(k); got != 2 {
		t.Fatalf("CountActiveClients = %d, want 2", got)
	}

	r1()
	r2()

	if got := CountActiveClients(k); got != 0 {
		t.Fatalf("after both releases: CountActiveClients = %d, want 0", got)
	}
}

func TestClientLeaseScopedByKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	rel, err := AcquireClientLease(Key{Slug: "p-abc123", Agent: "opencode"})
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer rel()
	if got := CountActiveClients(Key{Slug: "p-abc123", Agent: "opencode"}); got != 1 {
		t.Errorf("CountActiveClients(same key) = %d, want 1", got)
	}
	if got := CountActiveClients(Key{Slug: "p-abc123", Agent: "pi"}); got != 0 {
		t.Errorf("CountActiveClients(other agent) = %d, want 0", got)
	}
}
