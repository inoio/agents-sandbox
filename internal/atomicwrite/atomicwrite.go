// Package atomicwrite writes launcher state and configuration files atomically.
package atomicwrite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Filesystem seams. Writing to, flushing, or closing a freshly created temp
// file, and opening or flushing a directory, only fail in situations (a full
// disk, a vanished directory) that a test filesystem cannot produce, so tests
// substitute them to exercise the error handling.
var (
	writeTempFile = func(f *os.File, data []byte) error {
		_, err := f.Write(data)
		return err
	}
	syncTempFile  = (*os.File).Sync
	closeTempFile = (*os.File).Close
	openDirectory = os.Open
	syncDirectory = (*os.File).Sync
)

// ErrInstalled marks a WriteFile error raised after the rename already replaced
// the destination: the new content is in place, and the error reports a failed
// durability step (flushing a directory) rather than an absent write. A caller
// that rolls back on a write error must leave the installed content alone when
// the error matches ErrInstalled, or it would discard what the destination
// already names.
var ErrInstalled = errors.New("destination already replaced")

// WriteFile atomically replaces path with data, creating the parent directory
// (mode 0700) if needed and flushing the parent of every directory it creates
// so the new tree is itself durable. It writes a uniquely named temporary file
// in the destination directory, flushes the temp file through os.File.Sync
// (fsync on Linux, the stronger F_FULLFSYNC on darwin), then renames it into
// place: a reader never observes a partial file, and two concurrent writers
// cannot collide on the temporary name (the last writer wins). The directory
// entry the rename creates is flushed too, so a completed write survives a
// crash or power loss. The file is written with mode 0600.
//
// An error wrapping ErrInstalled is returned only after the rename has put the
// data in place; every other error leaves the destination untouched.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := createDirectoryTree(dir); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if err := writeTempFile(tmp, data); err != nil {
		_ = closeTempFile(tmp)
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := syncTempFile(tmp); err != nil {
		_ = closeTempFile(tmp)
		_ = os.Remove(tmpName)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := closeTempFile(tmp); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	if err := flushDirectory(dir); err != nil {
		return fmt.Errorf("%w: %w", ErrInstalled, err)
	}
	return nil
}

// createDirectoryTree creates dir (mode 0700) if needed and flushes the parent
// of every directory it creates, shallowest first, so each new entry in the
// tree survives a crash or power loss — not only the leaf's entry that
// flushDirectory flushes after the rename.
func createDirectoryTree(dir string) error {
	pending := missingDirectories(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	for _, created := range pending {
		if err := flushDirectory(filepath.Dir(created)); err != nil {
			return err
		}
	}
	return nil
}

// missingDirectories returns the directories at or under dir that do not exist
// yet, shallowest first, so createDirectoryTree can flush each one's parent. The
// walk stops at the first existing ancestor, which always exists (root, or the
// current directory for a relative path).
func missingDirectories(dir string) []string {
	var missing []string
	for current := dir; ; current = filepath.Dir(current) {
		if _, err := os.Stat(current); err == nil {
			break
		}
		missing = append(missing, current)
	}
	slices.Reverse(missing)
	return missing
}

// flushDirectory opens dir and flushes it, so an entry recently added to or
// replaced in it is durable.
func flushDirectory(dir string) error {
	handle, err := openDirectory(dir)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", dir, err)
	}
	defer func() { _ = handle.Close() }()
	if err := syncDirectory(handle); err != nil {
		return fmt.Errorf("sync dir %s: %w", dir, err)
	}
	return nil
}
