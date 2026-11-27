package readback

import (
	"io/fs"
	"os"
	"slices"
	"strings"
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
// data. It reports false for a missing, escaping, or non-regular file.
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
	// readCapped reads at most one byte past the cap, so an over-cap file, or
	// one that fails to read, stays opaque: size and mtime, nil data.
	data, _ := readCapped(file, maxBytes)
	return homeFile{size: info.Size(), modTime: info.ModTime(), data: data}, true
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

// readHomeDirectory lists children in name order without following a path out
// of the home or blocking on a FIFO. ReadDir checks the opened descriptor is a
// directory, so replacing the path after open cannot bypass the check.
func readHomeDirectory(root *os.Root, rel string) []fs.DirEntry {
	directory, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer func() { _ = directory.Close() }()
	entries, _ := directory.ReadDir(-1)
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries
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
