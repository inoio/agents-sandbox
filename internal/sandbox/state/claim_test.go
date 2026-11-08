package state

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestAcquireClaimExclusivePerKey(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
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
	configpathstest.WithMockConfigPaths(t)
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
	configpathstest.WithMockConfigPaths(t)
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
	configpathstest.WithMockConfigPaths(t)
	claimOpened = func(f *os.File, _ string) { _ = f.Close() }
	t.Cleanup(func() { claimOpened = nil })

	release, claimed, err := AcquireClaim("claimflockproj", "key")
	if err == nil {
		t.Fatal("expected a non-contention flock error to surface")
	}
	if !errors.Is(err, syscall.EBADF) {
		t.Fatalf("flock error = %v, want the underlying EBADF wrapped", err)
	}
	if claimed {
		t.Error("a failed acquire must report claimed=false")
	}
	if release != nil {
		t.Error("a failed acquire must return a nil release")
	}
}

func TestAcquireClaimRetriesWhenFileRemovedUnderIt(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
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

// TestSameFileDifferentInode pins the identity comparison at the heart of the
// claim-removal race guard: when the path resolves to a different inode than the
// open file (another holder removed and recreated it), sameFile must report
// false. Both stat calls succeed here, so only os.SameFile can distinguish them.
func TestSameFileDifferentInode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = replacement.Close()

	if sameFile(f, path) {
		t.Fatal("a path naming a different inode than the open file must not match")
	}
}

// TestAcquireClaimUsesPrivateModes pins the claims dir and file modes at the
// seam, so the assertion does not depend on the test process's umask.
func TestAcquireClaimUsesPrivateModes(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	var dirMode, fileMode os.FileMode
	origMkdir, origOpen := mkdirClaimDir, openClaimFile
	mkdirClaimDir = func(dir string, mode os.FileMode) error {
		dirMode = mode
		return os.MkdirAll(dir, mode)
	}
	openClaimFile = func(path string, flag int, mode os.FileMode) (*os.File, error) {
		fileMode = mode
		return os.OpenFile(path, flag, mode)
	}
	t.Cleanup(func() { mkdirClaimDir, openClaimFile = origMkdir, origOpen })

	release, claimed, err := AcquireClaim("claimmodeproj", "key")
	if err != nil || !claimed {
		t.Fatalf("AcquireClaim: claimed=%v err=%v", claimed, err)
	}
	release()

	if dirMode != 0o700 {
		t.Errorf("claims dir mode = %o, want 700", dirMode)
	}
	if fileMode != 0o600 {
		t.Errorf("claim file mode = %o, want 600", fileMode)
	}
}

// TestAcquireClaimMkdirErrorWrapsAndReportsUnclaimed pins the claims-dir
// creation failure: it must wrap the underlying error (not flatten it) and
// report claimed=false.
func TestAcquireClaimMkdirErrorWrapsAndReportsUnclaimed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirClaimDir
	mkdirClaimDir = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirClaimDir = orig })

	release, claimed, err := AcquireClaim("claimmkdiresc", "key")
	if !errors.Is(err, wantErr) {
		t.Fatalf("AcquireClaim() error = %v, want %v wrapped", err, wantErr)
	}
	if claimed {
		t.Error("a failed acquire must report claimed=false")
	}
	if release != nil {
		t.Error("a failed acquire must return a nil release")
	}
}

// TestAcquireClaimOpenErrorWrapsAndReportsUnclaimed pins the claim-file open
// failure: the wrap is preserved and the tuple reports unclaimed.
func TestAcquireClaimOpenErrorWrapsAndReportsUnclaimed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	wantErr := errors.New("open boom")
	orig := openClaimFile
	openClaimFile = func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openClaimFile = orig })

	release, claimed, err := AcquireClaim("claimopeneresc", "key")
	if !errors.Is(err, wantErr) {
		t.Fatalf("AcquireClaim() error = %v, want %v wrapped", err, wantErr)
	}
	if claimed {
		t.Error("a failed acquire must report claimed=false")
	}
	if release != nil {
		t.Error("a failed acquire must return a nil release")
	}
}

func TestAcquireClaim_MkdirError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
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
	configpathstest.WithMockConfigPaths(t)
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
