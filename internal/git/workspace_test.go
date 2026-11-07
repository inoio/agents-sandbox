package git

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWithinSeparatorBoundary pins the separator-aware prefix check: a sibling
// whose name merely starts with the parent's must not count as inside it.
func TestWithinSeparatorBoundary(t *testing.T) {
	if !within("/a/b", "/a/b") {
		t.Error("a path must be within itself")
	}
	if !within("/a/b", "/a/b/c") {
		t.Error("/a/b/c is within /a/b")
	}
	if within("/a/b", "/a/bc") {
		t.Error("/a/bc is not within /a/b (no separator boundary)")
	}
}

// TestIsNamedBareGitDirRequiresBareConfig pins the final isBare check: a
// directory under a name other than ".git" that holds a HEAD file is still not a
// bare git dir unless its config says core.bare = true. A stray "config" with
// core.bare = false (or no config at all) must not pass as a repository.
func TestIsNamedBareGitDirRequiresBareConfig(t *testing.T) {
	base := mustResolvePath(t, t.TempDir())
	writeHead := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(HEAD): %v", err)
		}
	}

	t.Run("HEAD file without a config", func(t *testing.T) {
		dir := filepath.Join(base, "head-only")
		writeHead(dir)
		if isNamedBareGitDir(dir) {
			t.Errorf("isNamedBareGitDir(%q) = true, want false without a bare config", dir)
		}
	})

	t.Run("HEAD file with core.bare = false", func(t *testing.T) {
		dir := filepath.Join(base, "non-bare-config")
		writeHead(dir)
		if err := os.WriteFile(filepath.Join(dir, "config"), []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(config): %v", err)
		}
		if isNamedBareGitDir(dir) {
			t.Errorf("isNamedBareGitDir(%q) = true, want false for core.bare = false", dir)
		}
	})
}
