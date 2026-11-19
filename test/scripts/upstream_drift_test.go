package scripts

import (
	"path/filepath"
	"testing"
)

// TestUpstreamDrift ports every case of the bash harness scripts/test-upstream-drift.sh,
// asserting the same exit codes and output substrings. The bash harness's
// git-isolation head is dropped: the fixture is go-git and RunScript strips the
// ambient git settings from the script's environment itself.
func TestUpstreamDrift(t *testing.T) {
	t.Run("counts, overlap, and names", func(t *testing.T) {
		repo := seedDriftRepo(t)

		result := runDrift(t, repo.Dir(), "--base", "main", "--no-fetch")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "upstream commits since merge-base: 2")
		assertContains(t, "stdout", result.Stdout, "branch commits since merge-base:   2")
		assertContains(t, "stdout", result.Stdout, "files changed on both sides:       1")
		assertContains(t, "stdout", result.Stdout, "  fileA")
		assertNotContains(t, "stdout", result.Stdout, "  fileC")
		assertContains(t, "stdout", result.Stdout, "main-a")
	})

	t.Run("disjoint branch reports a clean rebase", func(t *testing.T) {
		repo := seedDriftRepo(t)
		repo.Checkout("main")
		mainHead, err := repo.Head()
		if err != nil {
			t.Fatalf("read main: %v", err)
		}
		repo.SetBranch("disjoint", mainHead)
		repo.Checkout("disjoint")
		repo.Write("fileZ", "z\n")
		repo.Commit("feat-z")

		result := runDrift(t, repo.Dir(), "--base", "main", "--no-fetch")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "No file changed on both sides")
	})

	t.Run("quiet omits the upstream commit list", func(t *testing.T) {
		repo := seedDriftRepo(t)

		result := runDrift(t, repo.Dir(), "--base", "main", "--no-fetch", "--quiet")

		assertStatus(t, result, 0)
		assertNotContains(t, "stdout", result.Stdout, "Upstream commits:")
	})

	t.Run("usage", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		result := RunScript(
			t,
			t.TempDir(),
			nil,
			repositoryScript(t, "upstream-drift.sh"),
			"--repo",
			missing,
			"--no-fetch",
		)
		assertStatus(t, result, 2)
		assertContains(t, "stderr", result.Stderr, "not a git repository")

		if result := runDrift(t, t.TempDir(), "--help"); result.Status != 0 {
			t.Errorf("--help exit status = %d, want 0", result.Status)
		}
	})
}

// seedDriftRepo builds the bash harness's new_repo fixture: main advances by two
// commits touching fileA and fileB while feature diverges from the base with an
// overlapping change to fileA and a branch-only change to fileC. HEAD lands on
// feature.
func seedDriftRepo(tb testing.TB) *Repo {
	tb.Helper()
	repo := New(tb)
	repo.Write("fileA", "a\n")
	repo.Write("fileB", "b\n")
	repo.Write("fileC", "c\n")
	base := repo.Commit("base")
	repo.SetBranch("feature", base)
	repo.Write("fileA", "a-main\n")
	repo.Commit("main-a")
	repo.Write("fileB", "b-main\n")
	repo.Commit("main-b")
	repo.Checkout("feature")
	repo.Write("fileA", "a-feature\n")
	repo.Commit("feat-a")
	repo.Write("fileC", "c-feature\n")
	repo.Commit("feat-c")
	return repo
}

// runDrift runs scripts/upstream-drift.sh with repoDir as both its working
// directory and its --repo target.
func runDrift(t *testing.T, repoDir string, args ...string) ScriptResult {
	t.Helper()
	full := append([]string{"--repo", repoDir}, args...)
	return RunScript(t, repoDir, nil, repositoryScript(t, "upstream-drift.sh"), full...)
}
