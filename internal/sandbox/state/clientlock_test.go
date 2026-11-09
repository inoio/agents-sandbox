package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestAcquireClientLease(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

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
	configpathstest.WithMockConfigPaths(t)

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
	configpathstest.WithMockConfigPaths(t)

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
	configpathstest.WithMockConfigPaths(t)

	k := Key{Slug: "staleproj-b", Agent: "opencode"}

	// Create the clients directory manually.
	clientsDir := filepath.Join(stateRoot(), k.Slug, k.Agent, "clients")
	if err := os.MkdirAll(clientsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Create a stale lock file with no holder (just a file, no flock) and age it
	// past the mid-create grace window, so it reads as a crashed client's
	// leftover rather than a lease being created right now.
	stalePath := filepath.Join(clientsDir, "12345-1000000000.lock")
	testutil.WritePath(t, stalePath, "stale")
	aged := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stalePath, aged, aged); err != nil {
		t.Fatal(err)
	}

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

// TestCountActiveClients_CountsFreshUnlockedLockFile pins the mid-create window
// of AcquireClientLease: the lock file exists but is not yet flocked. A counter
// landing in that window sees an unlocked file whose mtime is fresh; it must
// count it as a live client and keep it, not treat it as garbage. Removing it
// would leave the acquirer holding a flock on an anonymous inode that no later
// count can see (#298).
func TestCountActiveClients_CountsFreshUnlockedLockFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	k := Key{Slug: "freshlease-proj", Agent: "opencode"}

	clientsDir := filepath.Join(stateRoot(), k.Slug, k.Agent, "clients")
	if err := os.MkdirAll(clientsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// A file created exactly the way AcquireClientLease creates it, but before
	// its flock: present, unlocked, fresh mtime, still open.
	leasePath := filepath.Join(clientsDir, "54321-2000000000.lock")
	f, err := os.OpenFile(leasePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if got := CountActiveClients(k); got != 1 {
		t.Fatalf("CountActiveClients = %d, want 1 for a lease mid-create", got)
	}
	if _, err := os.Stat(leasePath); err != nil {
		t.Fatalf("fresh lease file must be kept, but was removed: %v", err)
	}
}

// TestCountActiveClients_AcquireVsCountRace exercises the real acquire-vs-count
// race: a goroutine hammers CountActiveClients while the main goroutine
// repeatedly acquires a lease and checks it is visible immediately after
// acquisition. The hammer never unlinks a fresh lease (counted as mid-create),
// and even a lease it did unlink is re-created by AcquireClientLease's sameFile
// retry, so the invariant holds every iteration. Bounded so it stays fast in CI.
func TestCountActiveClients_AcquireVsCountRace(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	k := Key{Slug: "acquirecountrace-proj", Agent: "opencode"}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_ = CountActiveClients(k)
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()

	for i := range 200 {
		release, err := AcquireClientLease(k)
		if err != nil {
			t.Fatalf("AcquireClientLease: %v", err)
		}
		if got := CountActiveClients(k); got == 0 {
			release()
			t.Fatalf("lease invisible right after acquisition, iteration %d", i)
		}
		release()
	}
}

func TestAcquireClientLease_NonExistentSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	k := Key{Slug: "nonexistent-slug-xyz", Agent: "opencode"}
	release, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease should succeed for non-existent slug: %v", err)
	}
	release()
}

func TestCountActiveClients_NoClientsDir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	k := Key{Slug: "nodir-proj", Agent: "opencode"}
	if got := CountActiveClients(k); got != 0 {
		t.Errorf("CountActiveClients = %d, want 0 when clients dir is absent", got)
	}
}

func TestCountActiveClients_SkipsUnopenableEntry(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	k := Key{Slug: "subdir-proj", Agent: "opencode"}
	clientsDir := filepath.Join(stateRoot(), k.Slug, k.Agent, "clients")
	if err := os.MkdirAll(filepath.Join(clientsDir, "a-subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A live lease sorting after the unopenable entry: the walk must skip only
	// the bad entry and keep counting, not abandon the rest of the directory.
	leasePath := filepath.Join(clientsDir, "z-fresh.lock")
	lease, err := os.OpenFile(leasePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })

	// A directory entry cannot be flock'd and must be skipped, not counted
	// and not removed.
	if got := CountActiveClients(k); got != 1 {
		t.Errorf("CountActiveClients = %d, want 1 (the subdirectory skipped, the lease counted)", got)
	}
	if _, err := os.Stat(filepath.Join(clientsDir, "a-subdir")); err != nil {
		t.Fatalf("subdirectory entry should not be removed, got %v", err)
	}
}

// TestCountActiveClientsAgedHeldLeaseIsLive pins that a lease whose flock is
// held is counted and kept even after it is older than the mid-create grace
// window: only an *unlocked* aged lease is a crashed client's leftover. The
// held branch must short-circuit before the freshness check, or the counter
// would unlink a live client's lease and undercount.
func TestCountActiveClientsAgedHeldLeaseIsLive(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "agedheldproj", Agent: "opencode"}

	release, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	clientsDir := filepath.Join(KeyDir(k), "clients")
	entries, err := os.ReadDir(clientsDir)
	if err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-time.Hour)
	for _, entry := range entries {
		if err := os.Chtimes(filepath.Join(clientsDir, entry.Name()), aged, aged); err != nil {
			t.Fatal(err)
		}
	}

	if got := CountActiveClients(k); got != 1 {
		t.Fatalf("CountActiveClients = %d, want 1 for an aged but held lease", got)
	}
	entries, err = os.ReadDir(clientsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a held lease file must not be unlinked, found %d entries", len(entries))
	}
}

// failingDirEntry is a DirEntry whose Info always fails, standing in for an
// entry that disappears between ReadDir and the freshness stat.
type failingDirEntry struct{}

func (failingDirEntry) Name() string               { return "gone.lock" }
func (failingDirEntry) IsDir() bool                { return false }
func (failingDirEntry) Type() fs.FileMode          { return 0 }
func (failingDirEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }

// TestLeaseIsFreshTreatsStatFailureAsFresh pins the conservative default: an
// entry whose mtime cannot be read is never unlinked on uncertainty, so it must
// be treated as fresh.
func TestLeaseIsFreshTreatsStatFailureAsFresh(t *testing.T) {
	if !leaseIsFresh(failingDirEntry{}) {
		t.Fatal("a lease whose Info() fails must be treated as fresh, never unlinked")
	}
}

// TestAcquireClientLeaseUsesPrivateModes pins the client-lock dir and file modes
// at the seam, including the dir re-created after a sweep race, so the assertion
// does not depend on the test process's umask.
func TestAcquireClientLeaseUsesPrivateModes(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "leasemodeproj", Agent: "opencode"}

	var dirModes, fileModes []os.FileMode
	origMkdir, origOpen := mkdirClientLeaseDir, openClientLeaseFile
	mkdirClientLeaseDir = func(dir string, mode os.FileMode) error {
		dirModes = append(dirModes, mode)
		return os.MkdirAll(dir, mode)
	}
	openClientLeaseFile = func(path string, flag int, mode os.FileMode) (*os.File, error) {
		fileModes = append(fileModes, mode)
		return os.OpenFile(path, flag, mode)
	}
	swept := false
	clientLeaseDirReady = func(dir string) {
		if swept {
			return
		}
		swept = true
		if err := os.Remove(dir); err != nil {
			t.Fatalf("simulate sweep: %v", err)
		}
	}
	t.Cleanup(func() {
		mkdirClientLeaseDir, openClientLeaseFile = origMkdir, origOpen
		clientLeaseDirReady = nil
	})

	release, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	release()

	if len(dirModes) < 2 {
		t.Fatalf("expected the dir to be created then re-created, got modes %v", dirModes)
	}
	for i, mode := range dirModes {
		if mode != 0o700 {
			t.Errorf("clients dir mode #%d = %o, want 700", i, mode)
		}
	}
	if len(fileModes) == 0 || fileModes[0] != 0o600 {
		t.Errorf("client lease file mode = %v, want 600", fileModes)
	}
}

// TestAcquireClientLeaseMkdirErrorWraps pins the create-dir failure: it must
// surface immediately, not fall through to the per-attempt open/re-create path
// that would mask the original failure.
func TestAcquireClientLeaseMkdirErrorWraps(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	openErr := errors.New("open fallthrough")
	origMkdir, origOpen := mkdirClientLeaseDir, openClientLeaseFile
	mkdirClientLeaseDir = func(string, os.FileMode) error { return wantErr }
	// A distinct, non-ENOENT open error keeps a fall-through past the create-dir
	// failure from aliasing the mkdir error.
	openClientLeaseFile = func(string, int, os.FileMode) (*os.File, error) { return nil, openErr }
	t.Cleanup(func() { mkdirClientLeaseDir, openClientLeaseFile = origMkdir, origOpen })

	_, err := AcquireClientLease(Key{Slug: "leasemkdiresc", Agent: "opencode"})
	if !errors.Is(err, wantErr) || errors.Is(err, openErr) {
		t.Fatalf("AcquireClientLease() error = %v, want the create-dir error %v", err, wantErr)
	}
}

// TestAcquireClientLeaseOpenErrorWraps pins the lease-file open failure: a
// non-ENOENT error surfaces wrapped rather than being retried.
func TestAcquireClientLeaseOpenErrorWraps(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	wantErr := errors.New("open boom")
	orig := openClientLeaseFile
	openClientLeaseFile = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openClientLeaseFile = orig })

	if _, err := AcquireClientLease(Key{Slug: "leaseopeneresc", Agent: "opencode"}); !errors.Is(err, wantErr) {
		t.Fatalf("AcquireClientLease() error = %v, want %v wrapped", err, wantErr)
	}
}

// TestAcquireClientLeaseSweptDirRecreateErrorWraps pins the re-create failure
// after a sweep race: the second MkdirAll's error is wrapped, not swallowed.
func TestAcquireClientLeaseSweptDirRecreateErrorWraps(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "leasesweeprecreateesc", Agent: "opencode"}
	wantErr := errors.New("recreate boom")

	orig := mkdirClientLeaseDir
	calls := 0
	mkdirClientLeaseDir = func(dir string, mode os.FileMode) error {
		calls++
		if calls == 1 {
			return os.MkdirAll(dir, mode)
		}
		return wantErr
	}
	swept := false
	clientLeaseDirReady = func(dir string) {
		if swept {
			return
		}
		swept = true
		if err := os.Remove(dir); err != nil {
			t.Fatalf("simulate sweep: %v", err)
		}
	}
	t.Cleanup(func() {
		mkdirClientLeaseDir = orig
		clientLeaseDirReady = nil
	})

	if _, err := AcquireClientLease(k); !errors.Is(err, wantErr) {
		t.Fatalf("AcquireClientLease() error = %v, want %v wrapped", err, wantErr)
	}
}

func TestAcquireClientLease_MkdirFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

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
	configpathstest.WithMockConfigPaths(t)
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

func TestAcquireClientLease_FlockErrorSurfaces(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	clientLeaseOpened = func(f *os.File, _ string) { _ = f.Close() }
	t.Cleanup(func() { clientLeaseOpened = nil })

	_, err := AcquireClientLease(Key{Slug: "leaseflockproj", Agent: "opencode"})
	if err == nil {
		t.Fatal("expected a flock error to surface")
	}
	if !errors.Is(err, syscall.EBADF) {
		t.Fatalf("flock error = %v, want the underlying EBADF wrapped", err)
	}
}

// TestAcquireClientLease_RetriesThenSucceeds pins the Greptile P1 on #299: a
// CountActiveClients poll can unlink the lease in the create-to-lock window, and
// the acquirer must not keep the lock on the now-anonymous inode. It re-checks
// the directory entry with sameFile and retries on a fresh name.
func TestAcquireClientLease_RetriesThenSucceeds(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "leaseretryproj", Agent: "opencode"}

	attempts := 0
	clientLeaseOpened = func(_ *os.File, path string) {
		attempts++
		if attempts == 1 {
			_ = os.Remove(path)
		}
	}
	t.Cleanup(func() { clientLeaseOpened = nil })

	release, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	if attempts != 2 {
		t.Fatalf("expected 2 attempts (one removal, then success), got %d", attempts)
	}
	if got := CountActiveClients(k); got != 1 {
		t.Fatalf("CountActiveClients = %d, want 1 after the retry", got)
	}
}

// TestAcquireClientLease_RecreatesSweptClientsDir pins the sweep race: a prune
// can reclaim the empty clients dir between MkdirAll and the lease open, so an
// ENOENT open must recreate the dir and retry rather than fail the session's
// lease (which would leave the client out of the active count).
func TestAcquireClientLease_RecreatesSweptClientsDir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "sweptleaseproj", Agent: "opencode"}

	swept := false
	clientLeaseDirReady = func(dir string) {
		if swept {
			return
		}
		swept = true
		if err := os.Remove(dir); err != nil {
			t.Fatalf("simulate sweep: %v", err)
		}
	}
	t.Cleanup(func() { clientLeaseDirReady = nil })

	release, err := AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	if got := CountActiveClients(k); got != 1 {
		t.Fatalf("CountActiveClients = %d, want 1 after the dir was recreated", got)
	}
}

// TestAcquireClientLease_RecreateFailureSurfaces pins that a clients dir which
// cannot be recreated after the sweep race (its parent turned unwritable) fails
// the acquisition rather than looping.
func TestAcquireClientLease_RecreateFailureSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("cannot make a dir unwritable as root")
	}
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "sweptfailproj", Agent: "opencode"}

	clientLeaseDirReady = func(dir string) {
		if err := os.Remove(dir); err != nil {
			t.Fatalf("simulate sweep: %v", err)
		}
		if err := os.Chmod(KeyDir(k), 0o500); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		clientLeaseDirReady = nil
		_ = os.Chmod(KeyDir(k), 0o700)
	})

	if _, err := AcquireClientLease(k); err == nil {
		t.Fatal("expected the failed recreate to surface")
	}
}

func TestAcquireClientLease_ErrorsWhenUnlinkedEveryAttempt(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	attempts := 0
	clientLeaseOpened = func(_ *os.File, path string) {
		attempts++
		_ = os.Remove(path)
	}
	t.Cleanup(func() { clientLeaseOpened = nil })

	if _, err := AcquireClientLease(Key{Slug: "leaseraceproj", Agent: "opencode"}); err == nil {
		t.Fatal("a lease file removed under every attempt must not be handed out")
	}
	if attempts != 5 {
		t.Fatalf("expected 5 attempts before giving up, got %d", attempts)
	}
}

func TestAcquireClientLease_MultipleFromSameProcess(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

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
	configpathstest.WithMockConfigPaths(t)
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
