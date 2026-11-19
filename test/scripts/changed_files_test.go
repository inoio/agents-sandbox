package scripts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/testutil/gittest"
)

// TestChangedFiles ports every case of the bash harness scripts/test-changed-files.sh.
func TestChangedFiles(t *testing.T) {
	t.Run("clean tree lists nothing", func(t *testing.T) {
		repo := newChangedFilesRepo(t)
		assertFileList(t, runChangedFiles(t, repo.Dir(), "local", ""))
	})

	t.Run("modified tracked file", func(t *testing.T) {
		repo := newChangedFilesRepo(t)
		repo.Write("internal/foo/foo.go", "package foo // x\n")
		result := runChangedFiles(t, repo.Dir(), "local", "")
		assertStatus(t, result, 0)
		assertFileList(t, result, "internal/foo/foo.go")
	})

	t.Run("modified plus untracked", func(t *testing.T) {
		repo := newChangedFilesRepo(t)
		repo.Write("internal/foo/foo.go", "package foo // x\n")
		repo.Write("internal/new.go", "package new\n")
		result := runChangedFiles(t, repo.Dir(), "local", "")
		assertFileList(t, result, "internal/foo/foo.go", "internal/new.go")
	})

	t.Run("deleted tracked file", func(t *testing.T) {
		repo := newChangedFilesRepo(t)
		repo.Remove("docs/guide.md")
		result := runChangedFiles(t, repo.Dir(), "local", "")
		assertFileList(t, result, "docs/guide.md")
	})

	t.Run("rename lists both endpoints", func(t *testing.T) {
		repo := newChangedFilesRepo(t)
		renameFile(t, filepath.Join(repo.Dir(), "docs", "guide.md"), filepath.Join(repo.Dir(), "internal", "guide.md"))
		result := runChangedFiles(t, repo.Dir(), "local", "")
		assertFileList(t, result, "docs/guide.md", "internal/guide.md")
	})

	t.Run("branch uses the merge base", func(t *testing.T) {
		repo := newDivergedRepo(t)
		result := runChangedFiles(t, repo.Dir(), "branch", "main")
		assertStatus(t, result, 0)
		assertFileList(t, result, "internal/foo/foo.go", "internal/extra.go")
	})

	t.Run("branch includes untracked", func(t *testing.T) {
		repo := newDivergedRepo(t)
		repo.Write("internal/untracked.go", "package untracked\n")
		result := runChangedFiles(t, repo.Dir(), "branch", "main")
		assertFileList(t, result, "internal/foo/foo.go", "internal/extra.go", "internal/untracked.go")
	})

	t.Run("missing base exits 3", func(t *testing.T) {
		repo := newDivergedRepo(t)
		result := runChangedFiles(t, repo.Dir(), "branch", "no-such-ref")
		assertStatus(t, result, 3)
	})

	t.Run("usage", func(t *testing.T) {
		repo := newChangedFilesRepo(t)
		if result := runChangedFiles(t, repo.Dir(), "", ""); result.Status != 2 {
			t.Errorf("no mode exit status = %d, want 2", result.Status)
		}
		if result := runChangedFiles(t, repo.Dir(), "bogus", ""); result.Status != 2 {
			t.Errorf("unknown mode exit status = %d, want 2", result.Status)
		}
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		result := testutil.RunScript(
			t,
			t.TempDir(),
			nil,
			repositoryScript(t, "changed-files.sh"),
			"--repo",
			missing,
			"local",
		)
		if result.Status != 2 {
			t.Errorf("non-repository exit status = %d, want 2", result.Status)
		}
		if result := testutil.RunScript(
			t,
			t.TempDir(),
			nil,
			repositoryScript(t, "changed-files.sh"),
			"--help",
		); result.Status != 0 {
			t.Errorf("--help exit status = %d, want 0", result.Status)
		}
	})
}

// newChangedFilesRepo builds a repo with a committed set of files in nested
// directories.
func newChangedFilesRepo(tb testing.TB) *gittest.Repo {
	tb.Helper()
	repo := gittest.New(tb)
	repo.Write("internal/foo/foo.go", "package foo\n")
	repo.Write("docs/guide.md", "# d\n")
	repo.Write("README.md", "# t\n")
	repo.Commit("base")
	return repo
}

// newDivergedRepo builds a repo where feature diverges from main and main
// advances after the divergence, so a plain `diff main` would over-report and
// only the merge base is correct. It leaves feature checked out.
func newDivergedRepo(tb testing.TB) *gittest.Repo {
	tb.Helper()
	repo := gittest.New(tb)
	repo.Write("internal/foo/foo.go", "package foo\n")
	repo.Write("docs/guide.md", "# d\n")
	repo.Write("README.md", "# t\n")
	base := repo.Commit("base")

	repo.SetBranch("feature", base)
	repo.Checkout("feature")
	repo.Write("internal/foo/foo.go", "package foo // f\n")
	repo.Write("internal/extra.go", "package extra\n")
	repo.Commit("feature")

	repo.Checkout("main")
	repo.Write("README.md", "# t2\n")
	repo.Commit("main unreconciled")

	repo.Checkout("feature")
	return repo
}

// runChangedFiles runs scripts/changed-files.sh in repoDir.
func runChangedFiles(t *testing.T, repoDir, mode, base string) testutil.ScriptResult {
	t.Helper()
	args := []string{"--repo", repoDir}
	if base != "" {
		args = append(args, "--base", base)
	}
	args = append(args, mode)
	return testutil.RunScript(t, repoDir, nil, repositoryScript(t, "changed-files.sh"), args...)
}

// renameFile moves a file on disk, leaving the source deleted and the
// destination untracked, the working-tree shape a rename reports both endpoints
// for.
func renameFile(tb testing.TB, from, to string) {
	tb.Helper()
	if err := os.Rename(from, to); err != nil {
		tb.Fatalf("rename %s -> %s: %v", from, to, err)
	}
}

// assertFileList compares the script's newline-separated paths, sorted, to want,
// so order does not matter but a duplicated or missing path does.
func assertFileList(t *testing.T, result testutil.ScriptResult, want ...string) {
	t.Helper()
	got := strings.Split(strings.TrimRight(result.Stdout, "\n"), "\n")
	if len(got) == 1 && got[0] == "" {
		got = nil
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("paths = %v, want %v\nstderr:\n%s", got, want, result.Stderr)
	}
}
