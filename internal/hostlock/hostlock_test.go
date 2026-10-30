package hostlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAcquireExclusiveCreatesPrivateDirAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "locks")

	release, err := AcquireExclusive(dir, "test.lock")
	if err != nil {
		t.Fatalf("AcquireExclusive: %v", err)
	}
	defer release()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("dir mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(filepath.Join(dir, "test.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 600", got)
	}
}

func TestAcquireExclusiveHoldsLockUntilRelease(t *testing.T) {
	dir := t.TempDir()
	release, err := AcquireExclusive(dir, "test.lock")
	if err != nil {
		t.Fatalf("AcquireExclusive: %v", err)
	}

	if err := tryLock(t, filepath.Join(dir, "test.lock")); !IsContention(err) {
		t.Fatalf("second lock while held: error = %v, want contention", err)
	}
	release()
	if err := tryLock(t, filepath.Join(dir, "test.lock")); err != nil {
		t.Fatalf("second lock after release: %v", err)
	}
}

func TestAcquireExclusiveMkdirError(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parentFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := AcquireExclusive(filepath.Join(parentFile, "dir"), "test.lock"); err == nil {
		t.Fatal("AcquireExclusive() error = nil, want a mkdir failure")
	}
}

func TestAcquireExclusiveOpenError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "test.lock"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := AcquireExclusive(dir, "test.lock"); err == nil {
		t.Fatal("AcquireExclusive() error = nil, want an open failure")
	}
}

func TestAcquireExclusiveFlockError(t *testing.T) {
	wantErr := errors.New("flock failed")
	original := flock
	flock = func(int, int) error { return wantErr }
	t.Cleanup(func() { flock = original })

	if _, err := AcquireExclusive(t.TempDir(), "test.lock"); !errors.Is(err, wantErr) {
		t.Fatalf("AcquireExclusive() error = %v, want %v", err, wantErr)
	}
}

func TestExclusiveBlocksNonBlockingSecondLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	f := openFile(t, path)
	if err := Exclusive(f); err != nil {
		t.Fatalf("Exclusive: %v", err)
	}

	if err := tryLock(t, path); !IsContention(err) {
		t.Fatalf("ExclusiveNonBlocking while held: error = %v, want contention", err)
	}
}

func TestIsContention(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{syscall.EWOULDBLOCK, true},
		{syscall.EAGAIN, true},
		{fmt.Errorf("wrapped: %w", syscall.EWOULDBLOCK), true},
		{syscall.EBADF, false},
		{errors.New("other"), false},
		{nil, false},
	}
	for _, test := range tests {
		if got := IsContention(test.err); got != test.want {
			t.Errorf("IsContention(%v) = %v, want %v", test.err, got, test.want)
		}
	}
}

func tryLock(t *testing.T, path string) error {
	t.Helper()
	return ExclusiveNonBlocking(openFile(t, path))
}

func openFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
