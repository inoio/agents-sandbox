package atomicwrite

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	if errors.Is(err, ErrInstalled) {
		t.Errorf("WriteFile() error = %v, want a failure before the rename, not ErrInstalled", err)
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
	err := WriteFile(path, []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteFile() error = %v, want %v", err, wantErr)
	}
	if errors.Is(err, ErrInstalled) {
		t.Errorf("WriteFile() error = %v, want a failure before the rename, not ErrInstalled", err)
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
	err := WriteFile(path, []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteFile() error = %v, want %v", err, wantErr)
	}
	if errors.Is(err, ErrInstalled) {
		t.Errorf("WriteFile() error = %v, want a failure before the rename, not ErrInstalled", err)
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
	if errors.Is(err, ErrInstalled) {
		t.Errorf("WriteFile() error = %v, want a failure before the rename, not ErrInstalled", err)
	}
	assertNoTempFiles(t, dir)
}

// TestWriteFileSyncsTempFileThenDirectory pins the durable write sequence: the
// temp file's data is flushed with fsync while its handle is still open and
// before the rename, and the destination directory is fsynced after the rename
// has put the file in place, so a crash cannot lose or truncate the write.
func TestWriteFileSyncsTempFileThenDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")

	var events []string
	var dirHandle *os.File
	origSyncTempFile := syncTempFile
	origCloseTempFile := closeTempFile
	origSyncDirectory := syncDirectory
	t.Cleanup(func() {
		syncTempFile = origSyncTempFile
		closeTempFile = origCloseTempFile
		syncDirectory = origSyncDirectory
	})

	syncTempFile = func(f *os.File) error {
		content, err := os.ReadFile(f.Name())
		if err != nil {
			t.Errorf("read temp file at sync: %v", err)
		}
		if string(content) != "payload" {
			t.Errorf("temp file content at sync = %q, want %q", content, "payload")
		}
		events = append(events, "sync temp file")
		return nil
	}
	closeTempFile = func(f *os.File) error {
		events = append(events, "close temp file")
		return f.Close()
	}
	syncDirectory = func(f *os.File) error {
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "payload" {
			t.Errorf(
				"destination at directory sync: content = %q, error = %v, want %q in place",
				content,
				err,
				"payload",
			)
		}
		dirHandle = f
		events = append(events, "sync directory "+f.Name())
		return nil
	}

	if err := WriteFile(path, []byte("payload")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	want := []string{"sync temp file", "close temp file", "sync directory " + dir}
	if !slices.Equal(events, want) {
		t.Errorf("call order = %v, want %v", events, want)
	}
	if dirHandle == nil {
		t.Fatal("directory sync did not receive a handle")
	}
	if err := dirHandle.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("directory handle close error = %v, want the writer to have closed it already", err)
	}
	assertNoTempFiles(t, dir)
}

// TestWriteFileStagesContentOutsideTheDestination pins that a reader never
// observes a partial file: while the new content is being written and flushed,
// the destination still holds the previous content, and only the rename swaps
// it.
func TestWriteFileStagesContentOutsideTheDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := WriteFile(path, []byte("old")); err != nil {
		t.Fatalf("WriteFile() seed error = %v", err)
	}

	origSyncTempFile := syncTempFile
	observed := ""
	syncTempFile = func(f *os.File) error {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read destination at temp-file sync: %v", err)
		}
		observed = string(content)
		return nil
	}
	t.Cleanup(func() { syncTempFile = origSyncTempFile })

	if err := WriteFile(path, []byte("new")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if observed != "old" {
		t.Errorf("destination at temp-file sync = %q, want the previous content %q", observed, "old")
	}
}

// TestWriteFileSyncTempFileError covers the file-sync failure via the seam: the
// write fails before the rename, and the temp file is cleaned up.
func TestWriteFileSyncTempFileError(t *testing.T) {
	wantErr := errors.New("sync boom")
	orig := syncTempFile
	syncTempFile = func(*os.File) error { return wantErr }
	t.Cleanup(func() { syncTempFile = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	err := WriteFile(path, []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteFile() error = %v, want %v", err, wantErr)
	}
	if errors.Is(err, ErrInstalled) {
		t.Errorf("WriteFile() error = %v, want a failure before the rename, not ErrInstalled", err)
	}
	assertNoTempFiles(t, dir)
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("destination stat error = %v, want the write to have failed before the rename", statErr)
	}
}

// TestWriteFileOpenDirectoryError covers the failure to open the destination
// directory for its post-rename sync via the seam. The rename has already
// completed, so the destination keeps the new content.
func TestWriteFileOpenDirectoryError(t *testing.T) {
	wantErr := errors.New("open dir boom")
	orig := openDirectory
	openDirectory = func(string) (*os.File, error) { return nil, wantErr }
	t.Cleanup(func() { openDirectory = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	err := WriteFile(path, []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteFile() error = %v, want %v", err, wantErr)
	}
	if !errors.Is(err, ErrInstalled) {
		t.Errorf("WriteFile() error = %v, want ErrInstalled for a post-rename failure", err)
	}
	if content, readErr := os.ReadFile(path); readErr != nil || string(content) != "x" {
		t.Errorf("destination content = %q, error = %v, want %q", content, readErr, "x")
	}
	assertNoTempFiles(t, dir)
}

// TestWriteFileSyncDirectoryError covers the directory-sync failure via the
// seam: the error propagates and the destination keeps the new content.
func TestWriteFileSyncDirectoryError(t *testing.T) {
	wantErr := errors.New("sync dir boom")
	orig := syncDirectory
	syncDirectory = func(*os.File) error { return wantErr }
	t.Cleanup(func() { syncDirectory = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	err := WriteFile(path, []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteFile() error = %v, want %v", err, wantErr)
	}
	if !errors.Is(err, ErrInstalled) {
		t.Errorf("WriteFile() error = %v, want ErrInstalled for a post-rename failure", err)
	}
	if content, readErr := os.ReadFile(path); readErr != nil || string(content) != "x" {
		t.Errorf("destination content = %q, error = %v, want %q", content, readErr, "x")
	}
	assertNoTempFiles(t, dir)
}

// TestWriteFileFlushesParentOfEachCreatedDirectory pins the directory-tree
// durability: creating a nested tree flushes the parent of every directory it
// created, shallowest first, so the new entries — not only the leaf's that the
// post-rename flush covers — survive a crash or power loss.
func TestWriteFileFlushesParentOfEachCreatedDirectory(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "a", "b", "state.yaml")

	var flushed []string
	orig := syncDirectory
	syncDirectory = func(f *os.File) error {
		flushed = append(flushed, f.Name())
		return nil
	}
	t.Cleanup(func() { syncDirectory = orig })

	if err := WriteFile(path, []byte("x")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// base persists the newly created a, base/a persists b, and base/a/b
	// persists the renamed file. All three are needed for a durable first write.
	want := []string{base, filepath.Join(base, "a"), filepath.Join(base, "a", "b")}
	if !slices.Equal(flushed, want) {
		t.Errorf("flushed directories = %v, want %v", flushed, want)
	}
}

// TestWriteFileDirectoryTreeSyncErrorFailsBeforeRename covers a flush of a
// directory created by MkdirAll: it runs before the temp file exists, so the
// failure is not ErrInstalled and the destination is never touched.
func TestWriteFileDirectoryTreeSyncErrorFailsBeforeRename(t *testing.T) {
	wantErr := errors.New("sync ancestor boom")
	orig := syncDirectory
	syncDirectory = func(*os.File) error { return wantErr }
	t.Cleanup(func() { syncDirectory = orig })

	base := t.TempDir()
	path := filepath.Join(base, "a", "b", "state.yaml")
	err := WriteFile(path, []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteFile() error = %v, want %v", err, wantErr)
	}
	if errors.Is(err, ErrInstalled) {
		t.Errorf("WriteFile() error = %v, want a pre-rename failure, not ErrInstalled", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("destination stat error = %v, want the write to have failed before touching it", statErr)
	}
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
