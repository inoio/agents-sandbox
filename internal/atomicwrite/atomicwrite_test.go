package atomicwrite

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteFileWritesContentAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.yaml")

	if err := WriteFile(path, []byte("hello")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 0700", dirInfo.Mode().Perm())
	}
}

func TestWriteFileReplacesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	if err := WriteFile(path, []byte("first")); err != nil {
		t.Fatalf("WriteFile() first error = %v", err)
	}
	if err := WriteFile(path, []byte("second")); err != nil {
		t.Fatalf("WriteFile() second error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "second" {
		t.Errorf("content = %q, want %q", data, "second")
	}
}

// TestWriteFileConcurrentWritersDoNotCollide is the regression guard for
// concurrent writers: a shared fixed-name temp file made one writer's rename
// fail after the other removed it. Unique temp names make every writer succeed.
func TestWriteFileConcurrentWritersDoNotCollide(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")

	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Go(func() {
			errs <- WriteFile(path, []byte("payload"))
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent WriteFile() error = %v", err)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("final file missing: %v", err)
	}
}

// TestWriteFileMkdirAllError covers the parent-directory creation failure: a
// regular file occupies the directory's path.
func TestWriteFileMkdirAllError(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(blocked, "state.yaml")
	err := WriteFile(path, []byte("x"))
	if err == nil {
		t.Fatal("WriteFile() error = nil, want MkdirAll failure")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != "mkdir" {
		t.Errorf("WriteFile() error = %v, want a wrapped *os.PathError from creating the parent directory", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("destination %s exists, want the write to have failed before touching it", path)
	}
}

// TestWriteFileCreateTempError covers the temp-file creation failure: the
// destination directory is not writable.
func TestWriteFileCreateTempError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block creation")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	path := filepath.Join(dir, "state.yaml")
	err := WriteFile(path, []byte("x"))
	if err == nil {
		t.Fatal("WriteFile() error = nil, want CreateTemp failure")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != "open" {
		t.Errorf("WriteFile() error = %v, want a wrapped *os.PathError from opening the temp file", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("destination stat error = %v, want the write to have failed before touching it", statErr)
	}
}

// TestWriteFileWriteError covers the temp-file write failure via the seam.
func TestWriteFileWriteError(t *testing.T) {
	wantErr := errors.New("write boom")
	orig := writeTempFile
	writeTempFile = func(*os.File, []byte) error { return wantErr }
	t.Cleanup(func() { writeTempFile = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := WriteFile(path, []byte("x")); !errors.Is(err, wantErr) {
		t.Fatalf("WriteFile() error = %v, want %v", err, wantErr)
	}
	assertNoTempFiles(t, dir)
}

// TestWriteFileCloseError covers the temp-file close failure via the seam.
func TestWriteFileCloseError(t *testing.T) {
	wantErr := errors.New("close boom")
	orig := closeTempFile
	closeTempFile = func(f *os.File) error {
		_ = f.Close()
		return wantErr
	}
	t.Cleanup(func() { closeTempFile = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := WriteFile(path, []byte("x")); !errors.Is(err, wantErr) {
		t.Fatalf("WriteFile() error = %v, want %v", err, wantErr)
	}
	assertNoTempFiles(t, dir)
}

// TestWriteFileRenameError covers the rename failure (a directory occupies the
// destination) and pins that the temp file is cleaned up. The *os.LinkError is
// asserted through the returned error so the wrap is load-bearing.
func TestWriteFileRenameError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}

	err := WriteFile(path, []byte("x"))
	if err == nil {
		t.Fatal("WriteFile() error = nil, want rename failure")
	}
	if _, ok := errors.AsType[*os.LinkError](err); !ok {
		t.Errorf("WriteFile() error = %v, want the rename's *os.LinkError reachable", err)
	}
	assertNoTempFiles(t, dir)
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".state.yaml.*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover temp files = %v, want none", matches)
	}
}
