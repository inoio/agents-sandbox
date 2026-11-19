package scripts

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/testutil/fakecmd"
	"github.com/inoio/agents-sandbox/internal/testutil/gittest"
)

// fakeGhHandler serves the `gh` a script under test calls, the way the bash
// harnesses' fake `gh` did: `pr view` returns the merge commit named by
// GH_MERGE_OID, `pr create` returns GH_PR_URL, and every call is appended to
// GH_ARGV. GH_BREAK_ORIGIN_AFTER_CREATE repoints the fixture's origin at
// GH_BROKEN_ORIGIN on create, so the script's follow-up push fails.
//
// It runs in the re-executed test process, so it reads its knobs from the
// environment the harness set, not from a closure.
func fakeGhHandler(args []string) (string, string, int) {
	if argvPath := envOption("GH_ARGV"); argvPath != "" {
		if err := appendLine(argvPath, strings.Join(args, " ")); err != nil {
			return "", "fake gh: record argv: " + err.Error() + "\n", 1
		}
	}
	if len(args) >= 2 && args[0] == "pr" && args[1] == "view" {
		return envOption("GH_MERGE_OID") + "\n", "", 0
	}
	if len(args) >= 2 && args[0] == "pr" && args[1] == "create" {
		if envOption("GH_BREAK_ORIGIN_AFTER_CREATE") != "" {
			if err := rewriteOriginURL(envOption("BACKPORT_REPO"), envOption("GH_BROKEN_ORIGIN")); err != nil {
				return "", "fake gh: repoint origin: " + err.Error() + "\n", 1
			}
		}
		return envOption("GH_PR_URL"), "", 0
	}
	return "", "fake gh: unexpected call: " + strings.Join(args, " ") + "\n", 1
}

// envOption reads a value the harness set for the script and its fakes. The
// fake runs in a re-executed process, so it reads its canned responses from the
// environment rather than from a closure.
func envOption(name string) string {
	return os.Getenv(name) //testenv:allow the harness forwards its environment to the script and the fakes it runs
}

// rewriteOriginURL points the repository's origin remote at url.
func rewriteOriginURL(repoDir, url string) error {
	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		return err
	}
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	for _, remote := range cfg.Remotes {
		if remote.Name == "origin" {
			remote.URLs = []string{url}
		}
	}
	return repo.Storer.SetConfig(cfg)
}

// appendLine appends line and a newline to path, creating it if needed.
func appendLine(path, line string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(line + "\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// readText returns path's content, or "" when it does not exist.
func readText(tb testing.TB, path string) string {
	tb.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		tb.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// repositoryScript returns the absolute path of a script in the repository's
// scripts directory, resolved from the module root so the test is independent
// of the package's working directory.
func repositoryScript(tb testing.TB, name string) string {
	tb.Helper()
	return filepath.Join(repositoryRoot(tb), "scripts", name)
}

// repositoryRoot walks up from the working directory to the directory holding
// go.mod.
func repositoryRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}

// openHarness drives a script with a fake gh on PATH and a per-case argv log.
type openHarness struct {
	t        *testing.T
	binDir   string
	argvPath string
	prURL    string
}

// newOpenHarness builds a harness with a fresh fake-gh bin and argv log.
func newOpenHarness(t *testing.T) *openHarness {
	t.Helper()
	return &openHarness{
		t:        t,
		binDir:   fakecmd.BinDir(t, "gh"),
		argvPath: filepath.Join(t.TempDir(), "argv.txt"),
		prURL:    "https://github.com/o/r/pull/99",
	}
}

// run executes the script with dir as BACKPORT_REPO and args, returning its
// captured result. extra overlays the harness's base environment.
func (h *openHarness) run(dir, mergeOID string, extra map[string]string, args ...string) testutil.ScriptResult {
	h.t.Helper()
	_ = os.Remove(h.argvPath)
	env := map[string]string{
		"BACKPORT_REPO": dir,
		"GH_ARGV":       h.argvPath,
		"GH_PR_URL":     h.prURL,
		"GH_MERGE_OID":  mergeOID,
		"PATH":          h.binDir + string(os.PathListSeparator) + envOption("PATH"),
	}
	maps.Copy(env, extra)
	return testutil.RunScript(h.t, dir, env, repositoryScript(h.t, "open-backport-pr.sh"), args...)
}

// argv returns the recorded gh argv log.
func (h *openHarness) argv() string {
	return readText(h.t, h.argvPath)
}

// openFixture is a repository plus the hashes a backport case needs.
type openFixture struct {
	repo   *gittest.Repo
	origin string
	merge  plumbing.Hash
	first  plumbing.Hash
	second plumbing.Hash
}

// seedFixture builds a repo with a release/0.1 branch on the base commit and one
// fix commit on main (the merged PR), pushed to a bare origin.
func seedFixture(tb testing.TB) *openFixture {
	tb.Helper()
	repo := gittest.New(tb)
	repo.Write("f", "a\n")
	base := repo.Commit("base")
	repo.SetBranch("release/0.1", base)
	repo.Write("f", "b\n")
	merge := repo.Commit("fix")
	origin := repo.AddBareOrigin()
	repo.Push(tb)
	return &openFixture{repo: repo, origin: origin, merge: merge}
}

// seedMainOnly builds a repo with a fix on main and no release branch.
func seedMainOnly(tb testing.TB) *openFixture {
	tb.Helper()
	repo := gittest.New(tb)
	repo.Write("f", "a\n")
	repo.Commit("base")
	repo.Write("f", "b\n")
	merge := repo.Commit("fix")
	origin := repo.AddBareOrigin()
	repo.Push(tb)
	return &openFixture{repo: repo, origin: origin, merge: merge}
}

// seedFragmentFixture builds a repo whose fix also adds a changelog.d/7.fixed.md
// fragment, so the porter renames it for the backport PR.
func seedFragmentFixture(tb testing.TB) *openFixture {
	tb.Helper()
	repo := gittest.New(tb)
	repo.Write("f", "a\n")
	base := repo.Commit("base")
	repo.SetBranch("release/0.1", base)
	repo.Write("f", "b\n")
	repo.Write("changelog.d/7.fixed.md", "The fix.\n")
	merge := repo.Commit("fix")
	origin := repo.AddBareOrigin()
	repo.Push(tb)
	return &openFixture{repo: repo, origin: origin, merge: merge}
}

// seedTwoCommitFixture builds a repo whose #7 has two commits on a topic branch,
// merged into main with a merge commit; release/0.1 sits below both.
func seedTwoCommitFixture(tb testing.TB) *openFixture {
	tb.Helper()
	repo := gittest.New(tb)
	repo.Write("f", "a\n")
	base := repo.Commit("base")
	repo.SetBranch("release/0.1", base)
	repo.SetBranch("topic", base)
	repo.Checkout("topic")
	repo.Write("f", "b\n")
	first := repo.Commit("first")
	repo.Write("g", "c\n")
	second := repo.Commit("second")
	repo.Checkout("main")
	merge := repo.MergeCommit("Merge pull request #7", base, second)
	origin := repo.AddBareOrigin()
	repo.Push(tb)
	return &openFixture{repo: repo, origin: origin, merge: merge, first: first, second: second}
}

// seedEmptyCommitFixture builds a repo whose #7 has three commits on one topic
// branch: a real change, an empty commit, then another real change.
func seedEmptyCommitFixture(tb testing.TB) *openFixture {
	tb.Helper()
	repo := gittest.New(tb)
	repo.Write("f", "a\n")
	base := repo.Commit("base")
	repo.SetBranch("release/0.1", base)
	repo.SetBranch("topic", base)
	repo.Checkout("topic")
	repo.Write("f", "b\n")
	first := repo.Commit("first")
	repo.Commit("empty")
	repo.Write("g", "c\n")
	second := repo.Commit("second")
	repo.Checkout("main")
	merge := repo.MergeCommit("Merge pull request #7", base, second)
	origin := repo.AddBareOrigin()
	repo.Push(tb)
	return &openFixture{repo: repo, origin: origin, merge: merge, first: first, second: second}
}

// portSameDiff commits the same diff as source onto the current branch with the
// cherry-pick -x trailer, so its patch-id matches source without a cherry-pick.
func portSameDiff(tb testing.TB, repo *gittest.Repo, source plumbing.Hash, file, content string) {
	tb.Helper()
	repo.Write(file, content)
	repo.Commit("port\n\n(cherry picked from commit " + source.String() + ")")
}

// assertStatus fails when the script's exit status differs from want.
func assertStatus(t *testing.T, result testutil.ScriptResult, want int) {
	t.Helper()
	if result.Status != want {
		t.Errorf(
			"exit status = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			result.Status,
			want,
			result.Stdout,
			result.Stderr,
		)
	}
}

// assertContains fails when haystack does not contain needle.
func assertContains(t *testing.T, label, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s: %q does not contain %q", label, haystack, needle)
	}
}

// assertNotContains fails when haystack contains needle.
func assertNotContains(t *testing.T, label, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("%s: %q unexpectedly contains %q", label, haystack, needle)
	}
}
