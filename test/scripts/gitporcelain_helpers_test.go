package scripts

// Exec-git fixtures for the two porcelain harnesses (rebase-status and
// cleanup-merged-branches). Their state needs real-git porcelain go-git does
// not implement — an in-progress rebase, `worktree add`, `clone --bare`,
// `cherry-pick`/`merge`, `push --force-with-lease` refs — so the fixtures drive
// the real git binary. Every command runs through porcelainGit, which applies
// GitIsolationEnvironment: the same chokepoint the go-git harnesses get from
// RunScript, so the developer's git and ssh configuration can never reach a
// fixture. This file holds the only sanctioned ambient-git exec in test/scripts.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// porcelainGit runs git in dir and returns its combined output and error. The
// environment is isolated from the developer's git and ssh configuration.
func porcelainGit(tb testing.TB, dir string, args ...string) (string, error) {
	tb.Helper()
	command := exec.Command(
		"git",
		args...) //testenv:allow the two porcelain fixtures need the real git the tools under test drive
	command.Dir = dir
	command.Env = testutil.GitIsolationEnvironment(os.Environ())
	out, err := command.CombinedOutput()
	return string(out), err
}

// gitRun runs git in dir and fails the test on a non-zero exit.
func gitRun(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
	out, err := porcelainGit(tb, dir, args...)
	if err != nil {
		tb.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return out
}

// gitInit initializes a repository at dir.
func gitInit(tb testing.TB, dir string) {
	tb.Helper()
	gitRun(tb, dir, "init", "-q")
}

// gitConfig sets a repository-local config value.
func gitConfig(tb testing.TB, dir, key, value string) {
	tb.Helper()
	gitRun(tb, dir, "config", key, value)
}

// gitWriteAndCommit stages every change in dir and commits it.
func gitWriteAndCommit(tb testing.TB, dir, message string) {
	tb.Helper()
	gitRun(tb, dir, "add", "-A")
	gitRun(tb, dir, "commit", "-q", "-m", message)
}

// gitCommit stages every change in dir and commits it (the gitWriteAndCommit
// alias kept for harness readability).
func gitCommit(tb testing.TB, dir, message string) {
	tb.Helper()
	gitWriteAndCommit(tb, dir, message)
}

// gitCommitAllowEmpty records an empty commit.
func gitCommitAllowEmpty(tb testing.TB, dir, message string) {
	tb.Helper()
	gitRun(tb, dir, "commit", "-q", "--allow-empty", "-m", message)
}

// gitCommitAmendNoEdit folds the staged change into the tip commit.
func gitCommitAmendNoEdit(tb testing.TB, dir string) {
	tb.Helper()
	gitRun(tb, dir, "commit", "-q", "--amend", "--no-edit")
}

// gitRenameBranchTo renames the current branch (git branch -M).
func gitRenameBranchTo(tb testing.TB, dir, name string) {
	tb.Helper()
	gitRun(tb, dir, "branch", "-M", name)
}

// gitBranch creates branch at rev, or at HEAD when rev is empty.
func gitBranch(tb testing.TB, dir, name, rev string) {
	tb.Helper()
	if rev == "" {
		gitRun(tb, dir, "branch", name)
		return
	}
	gitRun(tb, dir, "branch", name, rev)
}

// gitBranchDelete force-deletes branches.
func gitBranchDelete(tb testing.TB, dir string, names ...string) {
	tb.Helper()
	gitRun(tb, dir, append([]string{"branch", "-D"}, names...)...)
}

// gitCheckout switches the working tree to name.
func gitCheckout(tb testing.TB, dir, name string) {
	tb.Helper()
	gitRun(tb, dir, "checkout", "-q", name)
}

// gitCheckoutNew creates and checks out branch name starting at start.
func gitCheckoutNew(tb testing.TB, dir, name, start string) {
	tb.Helper()
	gitRun(tb, dir, "checkout", "-q", "-b", name, start)
}

// gitSymbolicRefHead points HEAD at ref.
func gitSymbolicRefHead(tb testing.TB, dir, ref string) {
	tb.Helper()
	gitRun(tb, dir, "symbolic-ref", "HEAD", ref)
}

// gitSymbolicRef points ref at target.
func gitSymbolicRef(tb testing.TB, dir, ref, target string) {
	tb.Helper()
	gitRun(tb, dir, "symbolic-ref", ref, target)
}

// gitBareClone clones seed into a bare repository at into.
func gitBareClone(tb testing.TB, seed, into string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(into), 0o750); err != nil {
		tb.Fatalf("create %s: %v", filepath.Dir(into), err)
	}
	gitRun(tb, filepath.Dir(into), "clone", "-q", "--bare", seed, into)
}

// gitWorktreeAdd adds a linked worktree at path checked out to rev.
func gitWorktreeAdd(tb testing.TB, dir, path, rev string) {
	tb.Helper()
	gitRun(tb, dir, "worktree", "add", "-q", path, rev)
}

// gitWorktreeAddBranch adds a linked worktree at path on a new branch.
func gitWorktreeAddBranch(tb testing.TB, dir, path, branch, rev string) {
	tb.Helper()
	gitRun(tb, dir, "worktree", "add", "-q", path, "-b", branch, rev)
}

// gitWorktreeAddDetached adds a detached linked worktree at path.
func gitWorktreeAddDetached(tb testing.TB, dir, path, rev string) {
	tb.Helper()
	gitRun(tb, dir, "worktree", "add", "-q", "--detach", path, rev)
}

// gitWorktreeRemove removes a linked worktree.
func gitWorktreeRemove(tb testing.TB, dir, path string) {
	tb.Helper()
	gitRun(tb, dir, "worktree", "remove", path)
}

// gitUpdateRef points ref at rev.
func gitUpdateRef(tb testing.TB, dir, ref, rev string) {
	tb.Helper()
	gitRun(tb, dir, "update-ref", ref, rev)
}

// gitUpdateRefDelete deletes ref.
func gitUpdateRefDelete(tb testing.TB, dir, ref string) {
	tb.Helper()
	gitRun(tb, dir, "update-ref", "-d", ref)
}

// gitCherryPick cherry-picks rev onto the current branch.
func gitCherryPick(tb testing.TB, dir, rev string) {
	tb.Helper()
	gitRun(tb, dir, "cherry-pick", rev)
}

// gitMerge merges rev into the current branch with a merge commit.
func gitMerge(tb testing.TB, dir, rev string) {
	tb.Helper()
	gitRun(tb, dir, "merge", "-q", "--no-edit", rev)
}

// gitRebase rebases the current branch onto onto, returning the error of an
// expected conflict rather than failing.
func gitRebase(tb testing.TB, dir, onto string) error {
	tb.Helper()
	_, err := porcelainGit(tb, dir, "rebase", onto)
	return err
}

// gitRebaseAbort aborts an in-progress rebase, ignoring a no-op failure.
func gitRebaseAbort(tb testing.TB, dir string) {
	tb.Helper()
	_, _ = porcelainGit(tb, dir, "rebase", "--abort")
}

// gitRevParse returns the resolved object name of rev.
func gitRevParse(tb testing.TB, dir, rev string) string {
	tb.Helper()
	return strings.TrimSpace(gitRun(tb, dir, "rev-parse", rev))
}

// gitRevParseShort returns the abbreviated object name of rev.
func gitRevParseShort(tb testing.TB, dir, rev string) string {
	tb.Helper()
	return strings.TrimSpace(gitRun(tb, dir, "rev-parse", "--short", rev))
}
