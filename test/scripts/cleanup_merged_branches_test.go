package scripts

// Go port of scripts/test-cleanup-merged-branches.sh. It builds throwaway bare
// checkouts - a config worktree bridged into the bare root with .agents-sandbox
// symlinks, main advanced past the config branch, and a merged worktree plus
// merged local branch to remove - and asserts the printed plan. The fixtures
// need real git porcelain the tool itself drives (worktree add, clone --bare,
// cherry-pick/merge), so they run through the exec-git helpers in
// gitporcelain_helpers_test.go rather than go-git.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Knobs the cleanup-merged-branches fake gh reads from the re-executed test
// process's environment: the gate (so every other harness case keeps the shared
// fake gh's behaviour) and the canned PR record lines.
const (
	cleanupMergedBranchesFakeGhEnv = "CLEANUP_MERGED_BRANCHES_FAKE_GH"
	cleanupMergedBranchesPrsEnv    = "CLEANUP_MERGED_BRANCHES_GH_PRS"
)

// cleanupMergedBranchesFakeGh reproduces the bash harness's make_gh_stub: any
// `gh repo ...` prints the repository slug, `gh pr ...` prints the canned PR
// record line(s) the case passes, and any other call fails. It is a no-op
// (handled=false) unless the cleanup tests set the gate, so fakeGhHandler's
// other consumers are untouched.
func cleanupMergedBranchesFakeGh(args []string) (handled bool, stdout, stderr string, status int) {
	if envOption(cleanupMergedBranchesFakeGhEnv) == "" {
		return false, "", "", 0
	}
	if len(args) == 0 {
		return true, "", "fake gh: unexpected call\n", 1
	}
	switch args[0] {
	case "repo":
		return true, "inoio/agents-sandbox\n", "", 0
	case "pr":
		if records := envOption(cleanupMergedBranchesPrsEnv); records != "" {
			return true, records + "\n", "", 0
		}
		return true, "", "", 0
	case "api":
		return true, "", "", 1
	default:
		return true, "", "fake gh: unexpected call: " + strings.Join(args, " ") + "\n", 1
	}
}

func TestCleanupMergedBranches(t *testing.T) {
	t.Run("config worktree protection", func(t *testing.T) {
		// Case 1 (B+A): named config, mirror branch, relative symlink.
		cleanupCheckProtection(
			t,
			"B+A named+symlinked",
			newCleanupFixture(
				t,
				"config",
				"agents-sandbox-config",
				"relative",
				false,
				false,
			),
			"config",
			"agents-sandbox-config",
		)

		// Case 2 (B only, relative target - the recipe's actual shape): other name, other branch.
		cleanupCheckProtection(t, "B other-name relative",
			newCleanupFixture(t, "mirror", "cfg-mirror", "relative", false, false), "mirror", "cfg-mirror")

		// Case 2b (B only, absolute target): other name, other branch, absolute symlink.
		cleanupCheckProtection(t, "B other-name absolute",
			newCleanupFixture(t, "mirror2", "cfg-mirror", "absolute", false, false), "mirror2", "cfg-mirror")

		// Case 3 (A only): named config, no symlink, branch is neither mirror nor merged.
		cleanupCheckProtection(t, "A named no-symlink",
			newCleanupFixture(t, "config", "cfg-mirror", "none", false, false), "config", "cfg-mirror")
	})

	t.Run("dirty config worktree kept under force", func(t *testing.T) {
		// Case 4 (dirty): protection must precede the dirty/--force check.
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", true, false)
		out := cleanupRunCleanup(t, proj, "--force")
		keepWt := cleanupSection(out.Stdout, "Remaining worktrees:")
		assertContains(t, "dirty: config worktree kept", keepWt, filepath.Join(proj, "config"))
		assertContains(t, "dirty config kept under --force", keepWt, "[config mirror; protected]")
	})

	t.Run("config branch loop protection", func(t *testing.T) {
		// The config branch and its origin ref stay put.
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, false)
		out := cleanupRunCleanup(t, proj)
		remLb := cleanupSection(out.Stdout, "Local branches to delete:")
		keepLb := cleanupSection(out.Stdout, "Remaining local branches:")
		remRb := cleanupSection(out.Stdout, "Origin branches to delete:")
		keepRb := cleanupSection(out.Stdout, "Remaining origin branches:")
		assertNotContains(t, "local config branch not deleted", remLb, "agents-sandbox-config (was")
		assertContains(t, "local config branch kept", keepLb, "[config mirror; protected]")
		assertNotContains(t, "origin config branch not deleted", remRb, "origin/agents-sandbox-config")
		assertContains(t, "origin config branch kept", keepRb, "origin/agents-sandbox-config")
		assertContains(t, "origin config branch note", keepRb, "[config mirror; protected]")
	})

	t.Run("quiet still names what it acts on", func(t *testing.T) {
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, false)
		out := cleanupRunCleanup(t, proj, "--quiet")
		assertContains(t, "quiet names worktree", out.Stdout, filepath.Join(proj, "oldwork"))
		assertContains(t, "quiet names branch", out.Stdout, "stale-branch")
		assertNotContains(t, "quiet omits remaining worktrees", out.Stdout, "Remaining worktrees:")
		assertNotContains(t, "quiet omits remaining local branches", out.Stdout, "Remaining local branches:")
		assertNotContains(t, "quiet omits remaining origin branches", out.Stdout, "Remaining origin branches:")
	})

	t.Run("plan printed before non-TTY abort", func(t *testing.T) {
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, false)
		out := cleanupRunConfirmless(t, proj)
		assertContains(t, "plan names worktree before abort", out.Stdout, filepath.Join(proj, "oldwork"))
		assertContains(t, "non-TTY aborts", out.Stderr, "refusing to delete")
		if out.Status == 0 {
			t.Error("non-TTY exit non-zero: got 0")
		}
	})

	t.Run("zero planned actions", func(t *testing.T) {
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, false)
		gitWorktreeRemove(t, proj, filepath.Join(proj, "oldwork"))
		gitBranchDelete(t, proj, "old-work", "stale-branch")
		gitUpdateRefDelete(t, proj, "refs/remotes/origin/stale-branch")
		out := cleanupRunConfirmless(t, proj)
		if out.Status != 0 {
			t.Errorf("zero actions exit 0: got %d\nstderr:\n%s", out.Status, out.Stderr)
		}
		assertNotContains(t, "zero actions no abort", out.Stderr, "refusing to delete")
	})

	t.Run("content containment", func(t *testing.T) {
		// A branch whose only own commit is patch-equivalent to a main commit
		// plus a clean auto-merge is removed; a manual-resolution merge and a
		// branch with unique commits are kept.
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, true)
		out := cleanupRunCleanup(t, proj)
		remLb := cleanupSection(out.Stdout, "Local branches to delete:")
		keepLb := cleanupSection(out.Stdout, "Remaining local branches:")
		assertContains(t, "containment: auto-merge branch removed", remLb, "containment (was")
		assertNotContains(t, "containment: manual-merge branch kept", remLb, "containment-manual (was")
		assertContains(t, "containment: manual-merge branch kept", keepLb, "containment-manual")
		assertNotContains(t, "containment: unique branch kept", remLb, "unique (was")
		assertContains(t, "containment: unique branch kept", keepLb, "unique")
	})

	t.Run("detached worktrees", func(t *testing.T) {
		// A contained one is removed, a unique one is kept, and one at the
		// default-branch tip is kept.
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, true)
		out := cleanupRunCleanup(t, proj)
		remWt := cleanupSection(out.Stdout, "Worktrees to remove:")
		keepWt := cleanupSection(out.Stdout, "Remaining worktrees:")
		assertContains(t, "detached: contained worktree removed", remWt, filepath.Join(proj, "detached-contained"))
		assertNotContains(t, "detached: unique worktree kept", remWt, filepath.Join(proj, "detached-unique"))
		assertContains(t, "detached: unique worktree kept", keepWt, filepath.Join(proj, "detached-unique"))
		assertContains(t, "detached: current worktree kept", keepWt, filepath.Join(proj, "detached-current"))
		assertContains(t, "detached: current kept as default", keepWt, "[default branch or newer; kept]")
	})

	t.Run("dirty detached worktree kept without force", func(t *testing.T) {
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", true, true)
		writeFixture(t, filepath.Join(proj, "detached-contained", "scratch.txt"), "scratch\n")
		out := cleanupRunCleanup(t, proj)
		keepWt := cleanupSection(out.Stdout, "Remaining worktrees:")
		assertContains(t, "detached: dirty kept without --force", keepWt, filepath.Join(proj, "detached-contained"))
	})

	t.Run("remote branches get the same content proof", func(t *testing.T) {
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, true)
		out := cleanupRunCleanup(t, proj)
		remRb := cleanupSection(out.Stdout, "Origin branches to delete:")
		keepRb := cleanupSection(out.Stdout, "Remaining origin branches:")
		assertContains(t, "remote: contained branch removed", remRb, "origin/containment")
		assertNotContains(t, "remote: unique branch kept", remRb, "origin/unique-remote")
		assertContains(t, "remote: unique branch kept", keepRb, "origin/unique-remote")
	})

	t.Run("kept entries carry a reason", func(t *testing.T) {
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, true)
		out := cleanupRunCleanup(t, proj)
		keepLb := cleanupSection(out.Stdout, "Remaining local branches:")
		assertContains(t, "note: unique commit count", keepLb, "[2 commits not in origin/main]")
		assertContains(t, "note: manual merge unmerged", keepLb, "[unmerged]")
	})

	t.Run("fresh detached worktree kept", func(t *testing.T) {
		// A fresh detached worktree is kept even when its content is provable
		// (not only when it is an ancestor of the default branch).
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, true)
		out := cleanupRunCleanup(t, proj, "--fresh", "30m")
		remWt := cleanupSection(out.Stdout, "Worktrees to remove:")
		keepWt := cleanupSection(out.Stdout, "Remaining worktrees:")
		assertNotContains(t, "fresh: fresh detached not removed", remWt, filepath.Join(proj, "detached-fresh"))
		assertContains(t, "fresh: fresh detached kept", keepWt, filepath.Join(proj, "detached-fresh"))
		assertContains(t, "fresh: fresh detached note", keepWt, "[fresh session; kept]")
	})

	t.Run("gh closed-unmerged PR context", func(t *testing.T) {
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, true)
		gitUpdateRef(t, proj, "refs/remotes/origin/fix-issue-94", gitRevParse(t, proj, "unique"))
		records := "98 CLOSED main fix-issue-94 1111111111111111111111111111111111111111"
		out := cleanupRunGh(t, proj, records)
		keepRb := cleanupSection(out.Stdout, "Remaining origin branches:")
		assertContains(t, "note: closed PR context", keepRb, "PR #98 closed, not merged")
	})

	t.Run("gh non-target base PR is not removal proof", func(t *testing.T) {
		proj := newCleanupFixture(t, "config", "agents-sandbox-config", "relative", false, true)
		records := fmt.Sprintf("777 MERGED feature-base unique %s", gitRevParse(t, proj, "unique"))
		out := cleanupRunGh(t, proj, records)
		remLb := cleanupSection(out.Stdout, "Local branches to delete:")
		keepLb := cleanupSection(out.Stdout, "Remaining local branches:")
		assertNotContains(t, "gh: non-target base not deleted", remLb, "unique (was")
		assertContains(t, "gh: non-target base kept", keepLb, "unique")
	})
}

// cleanupSection returns the lines under the exact header line, up to the next
// blank line - the bash harness's `section` awk.
func cleanupSection(output, header string) string {
	var lines []string
	active := false
	for line := range strings.SplitSeq(output, "\n") {
		if line == header {
			active = true
			continue
		}
		if active && line == "" {
			break
		}
		if active {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// cleanupRunCleanup runs the tool the way the bash harness's run_cleanup does:
// as a dry run inside the main worktree.
func cleanupRunCleanup(t *testing.T, proj string, extra ...string) ScriptResult {
	t.Helper()
	args := append([]string{"--dry-run", "--no-fetch", "--no-gh", "--fresh", "0", "--color", "never"}, extra...)
	return RunScript(t, filepath.Join(proj, "main"), nil, repositoryScript(t, "cleanup-merged-branches.sh"), args...)
}

// cleanupRunConfirmless runs the tool without --dry-run and without a terminal,
// so the confirm gate sees a non-TTY stdin/stdout. RunScript leaves stdin nil,
// which is the condition the abort cases need.
func cleanupRunConfirmless(t *testing.T, proj string) ScriptResult {
	t.Helper()
	return RunScript(t, filepath.Join(proj, "main"), nil, repositoryScript(t, "cleanup-merged-branches.sh"),
		"--no-fetch", "--no-gh", "--fresh", "0", "--color", "never")
}

// cleanupRunGh runs the tool with the fake gh on PATH (no --no-gh), serving the
// canned PR record line(s).
func cleanupRunGh(t *testing.T, proj, records string) ScriptResult {
	t.Helper()
	binDir := BinDir(t, "gh")
	env := map[string]string{
		cleanupMergedBranchesFakeGhEnv: "1",
		cleanupMergedBranchesPrsEnv:    records,
		"PATH":                         binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	return RunScript(t, filepath.Join(proj, "main"), env, repositoryScript(t, "cleanup-merged-branches.sh"),
		"--dry-run", "--no-fetch", "--fresh", "0", "--color", "never")
}

// cleanupCheckProtection asserts the config worktree is kept with its
// protection note while the merged worktree and stale branch are still removed.
func cleanupCheckProtection(t *testing.T, label, proj, dir, branch string) {
	t.Helper()
	out := cleanupRunCleanup(t, proj)
	remWt := cleanupSection(out.Stdout, "Worktrees to remove:")
	remLb := cleanupSection(out.Stdout, "Local branches to delete:")
	keepWt := cleanupSection(out.Stdout, "Remaining worktrees:")
	assertContains(t, label+": config worktree kept", keepWt, filepath.Join(proj, dir))
	assertContains(t, label+": protection note", keepWt, "[config mirror; protected]")
	assertNotContains(t, label+": config worktree not removed", remWt, filepath.Join(proj, dir))
	assertContains(t, label+": merged worktree still removed", remWt, filepath.Join(proj, "oldwork"))
	assertNotContains(t, label+": config branch not deleted", remLb, branch+" (was")
	assertContains(t, label+": stale branch still deleted", remLb, "stale-branch (was")
}

// newCleanupFixture builds a throwaway bare checkout under a fresh temp dir:
// a seed repo (optionally with the content-containment history), a bare clone
// serving as the project root, a config worktree bridged with an
// .agents-sandbox symlink, main advanced past the config branch, and the merged
// worktree/branch to remove. It returns the project root.
func newCleanupFixture(tb testing.TB, configDir, configBranch, symlink string, dirty, containment bool) string {
	tb.Helper()
	if configDir == "" {
		configDir = "config"
	}
	if configBranch == "" {
		configBranch = "agents-sandbox-config"
	}
	if symlink == "" {
		symlink = "relative"
	}
	root := tb.TempDir()
	seed := filepath.Join(root, "seed")
	proj := filepath.Join(root, "proj")

	writeFixture(tb, filepath.Join(seed, ".agents-sandbox", "Dockerfile"), "base\n")
	gitInit(tb, seed)
	gitConfig(tb, seed, "user.email", "test@example.com")
	gitConfig(tb, seed, "user.name", "test")
	gitWriteAndCommit(tb, seed, "init")
	gitRenameBranchTo(tb, seed, "main")
	if containment {
		cleanupBuildContainmentHistory(tb, seed)
	}

	gitBareClone(tb, seed, filepath.Join(proj, ".git"))
	gitConfig(tb, proj, "user.email", "test@example.com")
	gitConfig(tb, proj, "user.name", "test")
	gitConfig(tb, proj, "worktree.useRelativePaths", "true")
	gitWorktreeAdd(tb, proj, filepath.Join(proj, "main"), "main")
	gitBranch(tb, proj, configBranch, "main")
	gitWorktreeAdd(tb, proj, filepath.Join(proj, configDir), configBranch)

	cleanupWriteConfigSymlink(tb, proj, configDir, symlink)

	old := gitRevParse(tb, filepath.Join(proj, "main"), "HEAD")
	gitCommitAllowEmpty(tb, filepath.Join(proj, "main"), "advance")
	gitUpdateRef(tb, proj, "refs/remotes/origin/main", gitRevParse(tb, filepath.Join(proj, "main"), "HEAD"))
	gitSymbolicRef(tb, proj, "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitBranch(tb, proj, "stale-branch", old)
	gitUpdateRef(tb, proj, "refs/remotes/origin/"+configBranch, old)
	gitUpdateRef(tb, proj, "refs/remotes/origin/stale-branch", old)
	gitWorktreeAddBranch(tb, proj, filepath.Join(proj, "oldwork"), "old-work", old)
	if containment {
		gitWorktreeAddDetached(tb, proj, filepath.Join(proj, "detached-contained"), old)
		gitWorktreeAddDetached(tb, proj, filepath.Join(proj, "detached-unique"), gitRevParse(tb, proj, "unique"))
		gitWorktreeAddDetached(tb, proj, filepath.Join(proj, "detached-current"), gitRevParse(tb, proj, "main"))
		gitWorktreeAddDetached(tb, proj, filepath.Join(proj, "detached-fresh"), gitRevParse(tb, proj, "containment"))
		gitUpdateRef(tb, proj, "refs/remotes/origin/containment", gitRevParse(tb, proj, "containment"))
		gitUpdateRef(tb, proj, "refs/remotes/origin/unique-remote", gitRevParse(tb, proj, "unique"))
	}
	if dirty {
		writeFixture(tb, filepath.Join(proj, configDir, "untracked.txt"), "scratch\n")
	}
	return proj
}

// cleanupWriteConfigSymlink bridges the config worktree's .agents-sandbox into
// the bare root with the named symlink shape: a relative file link, an absolute
// one, or none at all.
func cleanupWriteConfigSymlink(tb testing.TB, proj, configDir, mode string) {
	tb.Helper()
	directory := filepath.Join(proj, ".agents-sandbox")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		tb.Fatalf("create %s: %v", directory, err)
	}
	var target string
	switch mode {
	case "none":
		return
	case "relative":
		target = filepath.Join("..", configDir, ".agents-sandbox", "Dockerfile")
	case "absolute":
		target = filepath.Join(proj, configDir, ".agents-sandbox", "Dockerfile")
	default:
		tb.Fatalf("unknown symlink mode %q", mode)
	}
	if err := os.Symlink(target, filepath.Join(directory, "Dockerfile")); err != nil {
		tb.Fatalf("symlink %s: %v", target, err)
	}
}

// cleanupBuildContainmentHistory builds the history the content-containment
// cases need: a branch whose only own commit is patch-equivalent to a main
// commit plus a clean auto-merge of a sibling branch (removable); the same with
// the merge amended by a manual edit (kept); and a branch with unique commits
// (kept).
func cleanupBuildContainmentHistory(tb testing.TB, seed string) {
	tb.Helper()
	initial := gitRevParse(tb, seed, "HEAD")
	writeFixture(tb, filepath.Join(seed, "filler.txt"), "filler\n")
	gitCommit(tb, seed, "filler")
	writeFixture(tb, filepath.Join(seed, "a.txt"), "m1\n")
	gitCommit(tb, seed, "M1")
	m1 := gitRevParse(tb, seed, "HEAD")

	gitCheckoutNew(tb, seed, "S", initial)
	writeFixture(tb, filepath.Join(seed, "s.txt"), "s\n")
	gitCommit(tb, seed, "S")
	s := gitRevParse(tb, seed, "HEAD")

	gitCheckoutNew(tb, seed, "containment", initial)
	gitCherryPick(tb, seed, m1)
	gitMerge(tb, seed, s)

	gitCheckoutNew(tb, seed, "containment-manual", initial)
	gitCherryPick(tb, seed, m1)
	gitMerge(tb, seed, s)
	writeFixture(tb, filepath.Join(seed, "man.txt"), "manual\n")
	gitRun(tb, seed, "add", "man.txt")
	gitCommitAmendNoEdit(tb, seed)

	gitCheckoutNew(tb, seed, "unique", initial)
	writeFixture(tb, filepath.Join(seed, "u.txt"), "u1\n")
	gitCommit(tb, seed, "u1")
	writeFixture(tb, filepath.Join(seed, "u.txt"), "u1\nu2\n")
	gitCommit(tb, seed, "u2")

	gitCheckout(tb, seed, "main")
	gitMerge(tb, seed, s)
}
