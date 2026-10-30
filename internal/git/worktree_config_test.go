package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestIsBareReadsWorktreeConfig pins that a bare repo is still detected when
// git has moved core.bare out of the shared config into the main worktree's
// config.worktree, which it does when extensions.worktreeConfig is set (for
// example once sparse-checkout is used).
func TestIsBareReadsWorktreeConfig(t *testing.T) {
	gitDir := filepath.Join(mustResolvePath(t, t.TempDir()), ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", gitDir, err)
	}
	if err := os.WriteFile(
		filepath.Join(gitDir, "config"),
		[]byte("[core]\n\trepositoryformatversion = 1\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(gitDir, "config.worktree"),
		[]byte("[core]\n\tbare = true\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(config.worktree): %v", err)
	}

	if !isBare(gitDir) {
		t.Errorf("isBare(%q) = false, want true when core.bare is in config.worktree", gitDir)
	}
}

// TestResolveIdentityBareFromWorktreeConfig pins the resolver-level contract: a
// bare checkout whose core.bare lives only in config.worktree still resolves as
// bare, so project config is read from the bare root rather than the worktree.
func TestResolveIdentityBareFromWorktreeConfig(t *testing.T) {
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	gitDir := filepath.Join(root, ".git")
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, gitDir, "config", "worktree.useRelativePaths", "true")
	testutil.RunGit(t, gitDir, "worktree", "add", filepath.Join(root, "main"))

	// Reproduce the shape git leaves behind when extensions.worktreeConfig is
	// enabled: core.bare is gone from the shared config and lives in the main
	// worktree's config.worktree.
	if err := os.WriteFile(
		filepath.Join(gitDir, "config"),
		[]byte("[core]\n\trepositoryformatversion = 1\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(gitDir, "config.worktree"),
		[]byte("[core]\n\tbare = true\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(config.worktree): %v", err)
	}

	got := mustResolveIdentity(t, filepath.Join(root, "main"))
	if !got.Bare {
		t.Errorf("ResolveIdentity(...).Bare = false, want true with core.bare in config.worktree")
	}
	if got.BindRoot != root {
		t.Errorf("BindRoot = %q, want %q", got.BindRoot, root)
	}
}
