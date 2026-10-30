package git

import (
	"os"
	"path/filepath"
	"strings"
)

// findWorktreeRoot returns the nearest directory at or above cwd that contains a
// .git entry (a directory for a normal checkout, a file for a linked worktree or
// a bare layout's worktree) or is itself a bare git dir (a bare clone into
// "<name>.git"), or "" when none is found.
func findWorktreeRoot(cwd string) string {
	dir := cwd
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		if isNamedBareGitDir(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// within reports whether child is parent or under it.
func within(parent, child string) bool {
	if child == parent {
		return true
	}
	prefix := parent
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(child, prefix)
}

// isNamedBareGitDir reports whether dir is a bare repository's git dir under a
// name other than ".git". A bare ".git" dir is skipped: it belongs to the
// project around it, which the walk reaches next through its .git entry. The
// HEAD file keeps a stray "config" with core.bare from passing as a repository.
func isNamedBareGitDir(dir string) bool {
	if filepath.Base(dir) == ".git" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		return false
	}
	return isBare(dir)
}
