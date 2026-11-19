package scripts

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPorcelainGitHelpers exercises the exec-git fixture helpers used by the
// rebase-status and cleanup-merged-branches harnesses: a worktree repo, a bare
// clone with worktrees, branch/ref manipulation, and cherry-pick/merge.
func TestPorcelainGitHelpers(t *testing.T) {
	seed := t.TempDir()
	gitInit(t, seed)
	gitConfig(t, seed, "user.email", "test@example.com")
	gitConfig(t, seed, "user.name", "test")
	gitSymbolicRefHead(t, seed, "refs/heads/main")

	writeFixture(t, filepath.Join(seed, "base.txt"), "base\n")
	gitWriteAndCommit(t, seed, "base")
	base := gitRevParse(t, seed, "HEAD")
	if len(base) != 40 {
		t.Fatalf("HEAD = %q, want 40 hex chars", base)
	}
	if got := gitRevParseShort(t, seed, "HEAD"); got == "" {
		t.Fatal("rev-parse --short returned empty")
	}
	if got := gitRevParse(t, seed, "main"); got != base {
		t.Fatalf("main = %q, want %q", got, base)
	}

	// An empty commit and an amended commit.
	gitCommitAllowEmpty(t, seed, "empty")
	writeFixture(t, filepath.Join(seed, "amended.txt"), "amended\n")
	gitRun(t, seed, "add", "-A")
	gitCommitAmendNoEdit(t, seed)

	// A feature branch, a topic branch cherry-picking the feature, and a merge.
	gitBranch(t, seed, "feature", base)
	gitCheckout(t, seed, "feature")
	writeFixture(t, filepath.Join(seed, "feature.txt"), "feature\n")
	gitWriteAndCommit(t, seed, "feature")
	featureTip := gitRevParse(t, seed, "feature")

	gitCheckoutNew(t, seed, "topic", base)
	gitCherryPick(t, seed, featureTip)
	gitBranch(t, seed, "sibling", base)
	gitCheckout(t, seed, "topic")
	gitMerge(t, seed, "sibling")

	gitRenameBranchTo(t, seed, "main")
	gitCheckout(t, seed, "main")
	gitBranchDelete(t, seed, "sibling")

	// A bare clone with linked worktrees and remote-tracking refs.
	proj := t.TempDir()
	bare := filepath.Join(proj, ".git")
	gitBareClone(t, seed, bare)
	gitConfig(t, proj, "worktree.useRelativePaths", "true")
	gitWorktreeAdd(t, proj, filepath.Join(proj, "main"), "main")
	gitBranch(t, proj, "cfg-mirror", "main")
	gitWorktreeAddBranch(t, proj, filepath.Join(proj, "oldwork"), "old-work", base)
	gitWorktreeAddDetached(t, proj, filepath.Join(proj, "detached"), base)

	gitUpdateRef(t, proj, "refs/remotes/origin/main", base)
	gitSymbolicRef(t, proj, "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	if got := gitRun(t, proj, "rev-parse", "refs/remotes/origin/main"); got == "" {
		t.Fatal("update-ref did not create the origin ref")
	}
	gitUpdateRefDelete(t, proj, "refs/remotes/origin/main")
	gitWorktreeRemove(t, proj, filepath.Join(proj, "detached"))
	if _, err := os.Stat(filepath.Join(proj, "detached")); !os.IsNotExist(err) {
		t.Errorf("worktree remove left %s (stat err = %v)", filepath.Join(proj, "detached"), err)
	}
}

// writeFixture writes content to path, creating parent directories.
func writeFixture(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
