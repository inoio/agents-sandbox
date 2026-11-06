//go:build integration

package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// newSubmoduleUnderWorktreesAncestor creates a real submodule whose superproject
// lives under a path component literally named "worktrees", and returns the
// submodule's working-tree root. git records the submodule gitdir relative
// (../.git/modules/sub), so this is the shape a joined-then-stripped gitdir
// misclassifies: the "worktrees" ancestor leaks into the joined path.
func newSubmoduleUnderWorktreesAncestor(t *testing.T) string {
	t.Helper()
	origin := testutil.InitRepo(t)
	super := filepath.Join(mustResolvePath(t, t.TempDir()), "worktrees", "super")
	require.NoError(t, os.MkdirAll(super, 0o755))
	testutil.RunGit(t, super, "init")
	testutil.ConfigureRepo(t, super)
	testutil.WriteFile(t, super, "s", "s")
	testutil.RunGit(t, super, "add", ".")
	testutil.RunGit(t, super, "commit", "-m", "init")
	testutil.RunGit(t, super, "-c", "protocol.file.allow=always", "submodule", "add", origin, "sub")
	return filepath.Join(super, "sub")
}

// newRepoUnderWorktreesAncestor creates a real repo whose path contains a
// component literally named "worktrees" (so its .git path does too) and returns
// its working-tree root.
func newRepoUnderWorktreesAncestor(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(mustResolvePath(t, t.TempDir()), "worktrees", "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	testutil.RunGit(t, repo, "init")
	testutil.ConfigureRepo(t, repo)
	testutil.WriteFile(t, repo, "f", "f")
	testutil.RunGit(t, repo, "add", ".")
	testutil.RunGit(t, repo, "commit", "-m", "init")
	return repo
}
