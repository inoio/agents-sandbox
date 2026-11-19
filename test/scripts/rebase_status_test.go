package scripts

import (
	"path/filepath"
	"testing"
)

// TestRebaseStatus ports every case of the bash harness
// scripts/test-rebase-status.sh, asserting the same exit codes and output
// substrings. The bash harness's git-isolation head is dropped: the fixture
// drives the real git binary through the porcelain helpers (which apply
// GitIsolationEnvironment), and RunScript strips the ambient git settings from
// the script's environment itself.
func TestRebaseStatus(t *testing.T) {
	t.Parallel()
	t.Run("clean repository reports no rebase", func(t *testing.T) {
		t.Parallel()
		repo := seedRebaseStatusRepo(t)

		result := runRebaseStatus(t, repo, "--repo", repo)

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "no rebase in progress")
		assertContains(t, "stdout", result.Stdout, "unmerged paths: 0")
		assertContains(t, "stdout", result.Stdout, "files with conflict markers: 0")
	})

	t.Run("conflicted rebase reports step, target, and markers", func(t *testing.T) {
		t.Parallel()
		repo := seedRebaseStatusRepo(t)
		gitCheckout(t, repo, "feature")

		// The rebase applies the clean commit, advancing HEAD, then stops on the
		// conflicting rewrite of conflict.txt at step 2/2.
		if err := gitRebase(t, repo, "main"); err == nil {
			t.Fatal("rebase onto main succeeded, want a conflict")
		}
		mainTip := gitRevParseShort(t, repo, "main")

		result := runRebaseStatus(t, repo, "--repo", repo)

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "rebase in progress")
		assertNotContains(t, "stdout", result.Stdout, "no rebase in progress")
		assertContains(t, "stdout", result.Stdout, "step 2/2")
		assertContains(t, "stdout", result.Stdout, "onto "+mainTip)
		assertContains(t, "stdout", result.Stdout, "unmerged paths: 1")
		assertContains(t, "stdout", result.Stdout, "conflict.txt")
		assertContains(t, "stdout", result.Stdout, "files with conflict markers: 1")
	})

	t.Run("clean again after abort", func(t *testing.T) {
		t.Parallel()
		repo := seedRebaseStatusRepo(t)
		gitCheckout(t, repo, "feature")
		if err := gitRebase(t, repo, "main"); err == nil {
			t.Fatal("rebase onto main succeeded, want a conflict")
		}
		gitRebaseAbort(t, repo)

		result := runRebaseStatus(t, repo, "--repo", repo)

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "no rebase in progress")
		assertNotContains(t, "stdout", result.Stdout, "rebase in progress (")
	})

	t.Run("missing repository is an error", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "does-not-exist")

		result := runRebaseStatus(t, t.TempDir(), "--repo", missing)

		assertStatus(t, result, 2)
		assertContains(t, "stderr", result.Stderr, "not a git repository")
	})

	t.Run("help exits zero", func(t *testing.T) {
		t.Parallel()
		result := runRebaseStatus(t, t.TempDir(), "--help")

		assertStatus(t, result, 0)
	})
}

// runRebaseStatus runs scripts/rebase-status.sh from dir with args.
func runRebaseStatus(t *testing.T, dir string, args ...string) ScriptResult {
	t.Helper()
	return RunScript(t, dir, nil, repositoryScript(t, "rebase-status.sh"), args...)
}

// seedRebaseStatusRepo builds the bash harness's fixture: a repository on main
// with conflict.txt, a feature branch that diverges with a clean commit
// (clean.txt) then a commit rewriting conflict.txt the way main also rewrote it,
// so a rebase onto main applies the first and stops on the second. HEAD lands on
// main; the conflicted cases check out feature themselves.
func seedRebaseStatusRepo(tb testing.TB) string {
	tb.Helper()
	repo := tb.TempDir()
	gitInit(tb, repo)
	gitConfig(tb, repo, "user.email", "test@example.com")
	gitConfig(tb, repo, "user.name", "test")
	gitSymbolicRefHead(tb, repo, "refs/heads/main")

	writeFixture(tb, filepath.Join(repo, "conflict.txt"), "one\ntwo\n")
	gitCommit(tb, repo, "base")
	gitBranch(tb, repo, "feature", "")

	// main rewrites the first line; feature rewrites it differently.
	writeFixture(tb, filepath.Join(repo, "conflict.txt"), "main-line\ntwo\n")
	gitRun(tb, repo, "commit", "-q", "-am", "main")

	gitCheckout(tb, repo, "feature")
	// feature has a clean commit (clean.txt) then a conflicting one (conflict.txt):
	// the rebase applies the first, advancing HEAD to its rewritten commit, then
	// stops on the second.
	writeFixture(tb, filepath.Join(repo, "clean.txt"), "feature-only\n")
	gitCommit(tb, repo, "feature-clean")
	writeFixture(tb, filepath.Join(repo, "conflict.txt"), "feature-line\ntwo\n")
	gitRun(tb, repo, "commit", "-q", "-am", "feature")

	gitCheckout(tb, repo, "main")
	return repo
}
