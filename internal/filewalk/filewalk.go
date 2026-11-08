// Package filewalk provides the best-effort recursive file walk shared by the
// agent provisioning and config-mirror code.
package filewalk

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/projectguard"
)

// Entry is a readable regular file visited during a walk.
type Entry struct {
	Path string
	Rel  string
	Data []byte
	Mode fs.FileMode
}

// Walk visits every readable regular file under root in lexical order. filter
// is consulted for each entry with its root-relative path and directory flag;
// returning false skips a file or prunes a directory. Unreadable entries and
// files whose metadata or contents cannot be read are skipped, so a single bad
// file never aborts the walk. An error returned by visit aborts the walk.
func Walk(root string, filter func(rel string, isDir bool) bool, visit func(Entry) error) error {
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
		data, mode, ok := readFile(path)
		if !ok {
			return nil
		}
		reached = append(reached, path)
		return visit(Entry{Path: path, Rel: rel, Data: data, Mode: mode})
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

// readFile returns the contents and permission bits of path. ok is false when
// the metadata or contents cannot be read, so a best-effort walk can skip it.
func readFile(path string) ([]byte, fs.FileMode, bool) {
	info, err := os.Stat(path)
	if err != nil { // gomutants:disable BRANCH_IF reason="err returning nil means the file was not read, so falling through to the read below, which must then fail too, yields the same return"
		return nil, 0, false
	}
	data, err := projectguard.ReadFile(path, os.ReadFile)
	if err != nil {
		return nil, 0, false
	}
	return data, info.Mode().Perm(), true
}
