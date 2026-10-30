package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestResolveIdentityOutsideWorktreeReportsSharedGitDir pins what the VM needs
// to mount for an outside worktree (#426): the resolved shared git dir as the
// host source, and the path the worktree's .git file records as the guest
// target, since that is the path git follows inside the VM.
func TestResolveIdentityOutsideWorktreeReportsSharedGitDir(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	worktree := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", worktree)

	identity := mustResolveIdentity(t, worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, filepath.Join(repo, ".git"), identity.SharedGitDir)
	require.Equal(t, filepath.Join(repo, ".git"), identity.RecordedSharedGitDir)
}

// TestResolveIdentityOutsideWorktreeKeepsRecordedSymlinkPath covers a .git file
// that names the shared git dir through a symlink: git inside the VM follows
// the recorded text, so the target keeps the symlink while the source resolves.
func TestResolveIdentityOutsideWorktreeKeepsRecordedSymlinkPath(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	worktree := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", worktree)
	link := filepath.Join(mustResolvePath(t, t.TempDir()), "repo-link")
	require.NoError(t, os.Symlink(repo, link))
	writeGitdirFile(t, worktree, filepath.Join(link, ".git", "worktrees", "wt"))

	identity := mustResolveIdentity(t, worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, filepath.Join(repo, ".git"), identity.SharedGitDir)
	require.Equal(t, filepath.Join(link, ".git"), identity.RecordedSharedGitDir)
}

// TestResolveIdentityOutsideWorktreeWithRelativeGitdir covers
// worktree.useRelativePaths: the recorded path stays relative, so the caller
// can tell that no guest mount can make it resolve.
func TestResolveIdentityOutsideWorktreeWithRelativeGitdir(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	worktree := filepath.Join(mustResolvePath(t, t.TempDir()), "wt")
	testutil.RunGit(t, repo, "worktree", "add", worktree)
	relativeSharedGitDir, err := filepath.Rel(worktree, filepath.Join(repo, ".git"))
	require.NoError(t, err)
	writeGitdirFile(t, worktree, filepath.Join(relativeSharedGitDir, "worktrees", "wt"))

	identity := mustResolveIdentity(t, worktree)

	require.True(t, identity.OutsideWorktree)
	require.Equal(t, filepath.Join(repo, ".git"), identity.SharedGitDir)
	require.Equal(t, relativeSharedGitDir, identity.RecordedSharedGitDir)
}

func TestResolveIdentityInsideWorktreeHasNoSharedGitDir(t *testing.T) {
	repo := mustResolvePath(t, testutil.InitRepo(t))
	worktree := filepath.Join(repo, "wt")
	testutil.RunGit(t, repo, "worktree", "add", worktree)

	for _, dir := range []string{repo, worktree} {
		identity := mustResolveIdentity(t, dir)
		require.Empty(t, identity.SharedGitDir, dir)
		require.Empty(t, identity.RecordedSharedGitDir, dir)
	}
}
