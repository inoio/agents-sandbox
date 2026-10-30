package git

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestSanitizeFolderNameBoundaries pins the separator predicate at each ASCII
// edge: the digit range, the gap between '9' and 'a', and the top of the
// lowercase range. A char just outside a case must be treated as a separator;
// the boundary char itself must not be.
func TestSanitizeFolderNameBoundaries(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a0b", "a0b"},
		{"a9b", "a9b"},
		{"aZb", "azb"},
		{"a:b", "a-b"},
		{"a{b", "a-b"},
		{"a?b", "a-b"},
	}
	for _, tc := range cases {
		if got := sanitizeFolderName(tc.in); got != tc.want {
			t.Errorf("sanitizeFolderName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestOriginURLEmptyableOriginRemote pins that an origin remote with no URL is
// reported as no origin, not indexed blindly.
func TestOriginURLEmptyableOriginRemote(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfg := "[core]\n\trepositoryformatversion = 0\n" +
		"[remote \"origin\"]\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := originURL(dir); got != "" {
		t.Errorf("originURL(%q) = %q, want empty", dir, got)
	}
}

// TestOriginURLNonRepoIgnoresCwdConfig pins that a directory outside a repo
// resolves to no origin even when the process CWD happens to hold a "config"
// file: the empty git dir short-circuits before any relative read.
func TestOriginURLNonRepoIgnoresCwdConfig(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "config"),
		[]byte("[remote \"origin\"]\n\turl = git@github.com:oops/should-not-be-read.git\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Chdir(cwd)
	if got := originURL(filepath.Join(cwd, "not-a-repo")); got != "" {
		t.Errorf("originURL() = %q, want empty", got)
	}
}

func TestReadRepoConfigMissingIsNotExist(t *testing.T) {
	_, err := readRepoConfig(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readRepoConfig() error = %v, want fs.ErrNotExist", err)
	}
}

// TestLastPathSegmentSeparators pins the successive strips: '@' userinfo, the
// last '/', the last ':', and the .git suffix, including a leading separator
// (index 0) and an '@' inside the final segment.
func TestLastPathSegmentSeparators(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"https userinfo and port", "https://user@host:8080/org/repo.git", "repo"},
		{"scheme no path", "https://host", "host"},
		{"at in final segment", "host/repo@v2.git", "v2"},
		{"leading at", "@host", "host"},
		{"leading slash", "/repo", "repo"},
		{"leading colon", ":repo", "repo"},
		{"no separator", "repo.git", "repo"},
	}
	for _, tc := range cases {
		if got := lastPathSegment(tc.url); got != tc.want {
			t.Errorf("lastPathSegment(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

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
