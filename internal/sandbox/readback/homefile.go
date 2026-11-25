package readback

import (
	"io/fs"
	"os"
	"syscall"
	"time"
)

// The sandboxed agent controls the home volume, so every read-back read of it
// stays inside the home (os.Root: a symlinked file or parent leading out of it
// is refused), never blocks (a FIFO opens non-blocking), and takes only a
// regular file, as checked on the opened file.

// homeFile is a regular file read from the sandbox home: its size and mtime,
// and its content when it fits the cap (nil for a larger file).
type homeFile struct {
	size    int64
	modTime time.Time
	data    []byte
}

// readHomeFile opens rel under root and reads it when it is a regular file of
// at most maxBytes; a larger regular file reports its size and mtime with nil
// data. It reports false for a missing, escaping, non-regular, or unreadable
// file.
func readHomeFile(root *os.Root, rel string, maxBytes int64) (homeFile, bool) {
	// gomutants:disable-next-line INTEGER_INCREMENT reason="the permission is used only with O_CREATE, which this read-only open never passes"
	file, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	// gomutants:disable-next-line BRANCH_IF reason="a failed open leaves file nil, whose Stat fails with ErrInvalid, so the next check refuses it too"
	if err != nil {
		return homeFile{}, false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	// gomutants:disable-next-line EXPRESSION_REMOVE reason="fstat of a file this process just opened does not fail in a test; the error check only guards the nil info it would return"
	if err != nil || !info.Mode().IsRegular() {
		return homeFile{}, false
	}
	read := homeFile{size: info.Size(), modTime: info.ModTime(), data: nil}
	if info.Size() > maxBytes {
		return read, true
	}
	data, ok := readCapped(file, maxBytes)
	// gomutants:disable-next-line BRANCH_IF,RETURN_TRUE reason="the size check above already sends an over-cap file down the opaque path; readCapped fails only when the file grows past the cap between fstat and the read, a race a test cannot stage"
	if !ok {
		return homeFile{}, false
	}
	read.data = data
	return read, true
}

// ReadHomeFile reads the home-relative rel under home within the read-back's
// bounds and reports its content, or false when it is missing, leads out of
// the home, is not a regular file, is unreadable, or exceeds maxBytes.
func ReadHomeFile(home, rel string, maxBytes int64) ([]byte, bool) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, false
	}
	defer func() { _ = root.Close() }()
	// A refused file is the zero homeFile, so its nil data covers both a
	// refusal and an over-cap file.
	file, _ := readHomeFile(root, rel, maxBytes)
	return file.data, file.data != nil
}

// lstatInHome describes the home-relative rel without following it, resolving
// its parents inside home only, so a store reached through a symlink that leads
// out of the home is refused.
func lstatInHome(home, rel string) (fs.FileInfo, error) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.Lstat(rel)
}
