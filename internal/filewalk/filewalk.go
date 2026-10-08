// Package filewalk provides the best-effort recursive file walk shared by the
// agent provisioning and config-mirror code.
package filewalk

import (
	"io/fs"
	"os"
	"path/filepath"
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
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		if d.IsDir() {
			return nil
		}
		data, mode, ok := readFile(path)
		if !ok {
			return nil
		}
		return visit(Entry{Path: path, Rel: rel, Data: data, Mode: mode})
	})
}

// readFile returns the contents and permission bits of path. ok is false when
// the metadata or contents cannot be read, so a best-effort walk can skip it.
func readFile(path string) ([]byte, fs.FileMode, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false
	}
	return data, info.Mode().Perm(), true
}
