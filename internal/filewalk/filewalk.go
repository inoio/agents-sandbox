// Package filewalk provides the best-effort recursive file walk shared by the
// agent provisioning and config-mirror code.
package filewalk

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/inoio/agents-sandbox/internal/projectguard"
)

// Entry is a regular file visited during a walk. Data is nil for a file the
// walk recorded without reading it, which is a file larger than the walk's
// content cap; Size and ModTime still describe it.
type Entry struct {
	Path    string
	Rel     string
	Data    []byte
	Mode    fs.FileMode
	Size    int64
	ModTime time.Time
}

// Walk visits every readable regular file under root in lexical order. filter
// is consulted for each entry with its root-relative path and directory flag;
// returning false skips a file or prunes a directory. Unreadable entries and
// files whose metadata or contents cannot be read are skipped, so a single bad
// file never aborts the walk. An error returned by visit aborts the walk.
func Walk(root string, filter func(rel string, isDir bool) bool, visit func(Entry) error) error {
	// gomutants:disable-next-line INTEGER_DECREMENT reason="WalkCapped reads every file when maxReadBytes is zero or less, so a cap of 0 and -1 are the same"
	return WalkCapped(root, 0, filter, visit)
}

// WalkCapped is Walk with a content-read cap: a file larger than maxReadBytes is
// visited with a nil Data and its Size and ModTime set, so a caller that only
// needs a file's metadata never loads it into memory. A maxReadBytes of zero or
// less reads every file, exactly like Walk.
func WalkCapped(
	root string,
	maxReadBytes int64,
	filter func(rel string, isDir bool) bool,
	visit func(Entry) error,
) error {
	var reached []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort: skip entries the walk cannot read
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if !filter(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() { // gomutants:disable BRANCH_IF reason="filter runs before the directory check and the directory branch above returns SkipDir, and WalkDir's root callback always yields isDir true, so the only directory reaching here is the pruned root"
			return nil
		}
		data, mode, size, modTime, ok := readFile(path, maxReadBytes)
		if !ok {
			return nil
		}
		reached = append(reached, path)
		return visit(Entry{Path: path, Rel: rel, Data: data, Mode: mode, Size: size, ModTime: modTime})
	})
	if err != nil {
		return err
	}
	// An approved project file the walk would have taken but did not reach was
	// deleted since the approval: that stops the run rather than going missing.
	return projectguard.CheckListing(root, reached, func(rel string) bool { return selected(filter, rel) })
}

// selected reports whether filter takes the file at rel: every directory
// above it and the file itself pass.
func selected(filter func(rel string, isDir bool) bool, rel string) bool {
	for dir := filepath.Dir(rel); dir != "."; dir = filepath.Dir(dir) {
		if !filter(dir, true) {
			return false
		}
	}
	return filter(rel, false)
}

// readFile returns the contents and metadata of path. ok is false when the
// metadata or contents cannot be read, so a best-effort walk can skip it. A
// file larger than maxReadBytes (when maxReadBytes is positive) is returned with
// a nil data slice and ok true, so the walk records it without loading it.
func readFile(path string, maxReadBytes int64) ([]byte, fs.FileMode, int64, time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, 0, time.Time{}, false
	}
	if maxReadBytes > 0 && info.Size() > maxReadBytes {
		return nil, info.Mode().Perm(), info.Size(), info.ModTime(), true
	}
	data, err := projectguard.ReadFile(path, os.ReadFile)
	if err != nil {
		return nil, 0, 0, time.Time{}, false
	}
	return data, info.Mode().Perm(), info.Size(), info.ModTime(), true
}
