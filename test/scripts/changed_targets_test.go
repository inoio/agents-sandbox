package scripts

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/testutil/gittest"
)

// goDocs is the target set a changed Go file selects.
var goDocs = []string{"compile-all", "docs-linkcheck", "lint", "test", "validate-fmt"}

// TestChangedTargets ports every case of the bash harness scripts/test-changed-targets.sh.
func TestChangedTargets(t *testing.T) {
	t.Run("no changes selects nothing", func(t *testing.T) {
		repo := newMapRepo(t)
		assertSorted(t, "no changes", runMap(t, repo.Dir(), "local", "", nil).Stdout)
	})

	localCases := []struct {
		name    string
		path    string
		content string
		want    []string
	}{
		{"modified Go under internal", "internal/foo/foo.go", "package foo // x", goDocs},
		{"modified Go under cmd unions docs", "cmd/app/main.go", "package main // x", goDocs},
		{"modified docs page", "docs/guide.md", "# d2", []string{"docs-linkcheck"}},
		{"modified root markdown", "README.md", "# t2", []string{"docs-linkcheck"}},
		{"added changelog fragment", "changelog.d/999.changed.md", "note", []string{"changelog-check"}},
		{
			"modified docs checker",
			"ci/check-docs.sh",
			"# x",
			[]string{"docs-linkcheck", "docs-linkcheck-test", "shellcheck"},
		},
		{
			"modified changelog checker",
			"ci/check-changelog.sh",
			"# x",
			[]string{"changelog-check", "changelog-check-test", "ci-guards-check", "shellcheck"},
		},
		{"modified deadcode checker", "ci/check-deadcode.sh", "# x", []string{"deadcode-test", "shellcheck"}},
		{
			"modified changed-files script",
			"scripts/changed-files.sh",
			"# x",
			[]string{"changed-files-test", "shellcheck"},
		},
		{
			"modified changed-files test",
			"test/scripts/changed_files_test.go",
			"# x",
			[]string{"changed-files-test", "compile-all", "lint", "test", "validate-fmt"},
		},
		{"modified check runner", "scripts/check.sh", "# x", []string{"check-test", "shellcheck"}},
		{"modified check runner test", "scripts/test-check.sh", "# x", []string{"check-test", "shellcheck"}},
		{
			"modified upstream-drift script",
			"scripts/upstream-drift.sh",
			"# x",
			[]string{"shellcheck", "upstream-drift-test"},
		},
		{
			"modified rebase-status script",
			"scripts/rebase-status.sh",
			"# x",
			[]string{"rebase-status-test", "shellcheck"},
		},
		{"modified enqueue-pr script", "scripts/enqueue-pr.sh", "# x", []string{"enqueue-pr-test", "shellcheck"}},
		{
			"modified backport-audit script",
			"scripts/backport-audit.sh",
			"# x",
			[]string{"backport-audit-test", "shellcheck"},
		},
		{
			"modified backport-audit Go harness",
			"test/scripts/backport_audit_test.go",
			"# x",
			[]string{"backport-audit-test", "compile-all", "lint", "test", "validate-fmt"},
		},
		{
			"modified open-backport-pr script",
			"scripts/open-backport-pr.sh",
			"# x",
			[]string{"open-backport-pr-test", "shellcheck"},
		},
		{
			"modified open-backport-pr Go harness",
			"test/scripts/open_backport_pr_test.go",
			"# x",
			[]string{"open-backport-pr-test", "compile-all", "lint", "test", "validate-fmt"},
		},
		{"modified envelope test", "scripts/test-envelope.sh", "# x", []string{"envelope-test", "shellcheck"}},
		{
			"modified cleanup script",
			"scripts/cleanup-merged-branches.sh",
			"# x",
			[]string{"cleanup-branches-test", "shellcheck"},
		},
		{
			"modified ci-guards checker",
			"ci/check-ci-guards.sh",
			"# x",
			[]string{"ci-guards-check", "ci-guards-check-test", "shellcheck"},
		},
		{
			"modified change classifier",
			"ci/classify-changes.sh",
			"# x",
			[]string{"ci-guards-check", "classify-changes-test", "shellcheck"},
		},
		{
			"modified base-SHA resolver",
			"ci/event-base.sh",
			"# x",
			[]string{"ci-guards-check", "event-base-test", "shellcheck"},
		},
		{"modified workflow", ".github/workflows/ci.yml", "# x", []string{"actionlint", "ci-guards-check"}},
		{
			"modified surface-sweep script",
			"scripts/surface-sweep.sh",
			"# x",
			[]string{"shellcheck", "surface-sweep-test"},
		},
		{
			"modified changed-targets script",
			"scripts/changed-targets.sh",
			"# x",
			[]string{"changed-targets-test", "shellcheck"},
		},
		{
			"modified changed-targets Go harness",
			"test/scripts/changed_targets_test.go",
			"# x",
			[]string{"changed-targets-test", "compile-all", "lint", "test", "validate-fmt"},
		},
		{"modified dev-VM auth script", ".agents-sandbox/github-auth.sh", "# x", []string{"shellcheck"}},
		{
			"modified image-data shim",
			"internal/sandbox/image/data/xdg-open.sh",
			"# x",
			[]string{"compile-all", "lint", "shellcheck", "test", "validate-fmt"},
		},
		{"nested markdown selects nothing", ".github/PULL_REQUEST_TEMPLATE.md", "# x", nil},
		{"dev-VM dockerfile selects nothing", ".agents-sandbox/Dockerfile", "FROM x", nil},
		{"opencode config selects nothing", "opencode.jsonc", "{}", nil},
		{"operator brain selects nothing", ".operator-shared/specs/x.md", "# b2", nil},
	}
	for _, tt := range localCases {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMapRepo(t)
			repo.Write(tt.path, tt.content)
			assertSorted(t, tt.name, runMap(t, repo.Dir(), "local", "", nil).Stdout, tt.want...)
		})
	}

	t.Run("unrecognized Makefile selects all", func(t *testing.T) {
		repo := newMapRepo(t)
		repo.Write("Makefile", "all:\n")
		assertSorted(t, "Makefile selects all", runMap(t, repo.Dir(), "local", "", nil).Stdout, fullCheckTargets(t)...)
	})

	t.Run("deleted tracked Go", func(t *testing.T) {
		repo := newMapRepo(t)
		repo.Remove("internal/foo/foo.go")
		assertSorted(t, "deleted tracked Go", runMap(t, repo.Dir(), "local", "", nil).Stdout, goDocs...)
	})

	t.Run("untracked new Go", func(t *testing.T) {
		repo := newMapRepo(t)
		repo.Write("internal/new/new.go", "package new\n")
		assertSorted(t, "untracked new Go", runMap(t, repo.Dir(), "local", "", nil).Stdout, goDocs...)
	})

	t.Run("failing ls-files is an error", func(t *testing.T) {
		repo := newMapRepo(t)
		shimDir := writeFailingLSFilesGitShim(t)
		result := runMap(t, repo.Dir(), "local", "", map[string]string{
			"PATH": shimDir + string(os.PathListSeparator) + envOption("PATH"),
		})
		if result.Status == 0 {
			t.Errorf("a failing git ls-files exited 0, want non-zero")
		}
	})

	t.Run("rename selects the source class", func(t *testing.T) {
		repo := newMapRepo(t)
		repo.Rename("docs/guide.md", ".operator-shared/guide.md")
		assertSorted(
			t,
			"rename selects the source class",
			runMap(t, repo.Dir(), "local", "", nil).Stdout,
			"docs-linkcheck",
		)
	})

	t.Run("branch uses the merge base", func(t *testing.T) {
		repo := newMapDivergedRepo(t)
		assertSorted(t, "branch merge base", runMap(t, repo.Dir(), "branch", "main", nil).Stdout, "docs-linkcheck")
	})

	t.Run("branch with missing base selects all", func(t *testing.T) {
		repo := newMapDivergedRepo(t)
		assertSorted(
			t,
			"branch missing base",
			runMap(t, repo.Dir(), "branch", "no-such-ref", nil).Stdout,
			fullCheckTargets(t)...)
	})

	t.Run("usage", func(t *testing.T) {
		repo := newMapRepo(t)
		if result := runMap(t, repo.Dir(), "", "", nil); result.Status != 2 {
			t.Errorf("no mode exit status = %d, want 2", result.Status)
		}
		if result := runMap(t, repo.Dir(), "bogus", "", nil); result.Status != 2 {
			t.Errorf("unknown mode exit status = %d, want 2", result.Status)
		}
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		result := testutil.RunScript(
			t,
			t.TempDir(),
			nil,
			repositoryScript(t, "changed-targets.sh"),
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
			repositoryScript(t, "changed-targets.sh"),
			"--help",
		); result.Status != 0 {
			t.Errorf("--help exit status = %d, want 0", result.Status)
		}
	})
}

// fullCheckTargets reads the full `make check` target set the map falls back to,
// independently of the map: the fail-safe selection must equal what make
// actually runs, so it cannot be validated against the map's own output.
func fullCheckTargets(t *testing.T) []string {
	t.Helper()
	root := repositoryRoot(t)
	// Read the Makefile so a change to it busts the test cache.
	if _, err := os.ReadFile(filepath.Join(root, "Makefile")); err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	out, err := makePrintCheckTargets(root)
	if err != nil {
		t.Fatalf("make print-check-targets: %v", err)
	}
	targets := strings.Fields(out)
	slices.Sort(targets)
	return targets
}

// makePrintCheckTargets runs the Makefile's print-check-targets with MAKEFLAGS
// cleared, the way scripts/changed-targets.sh does, so a parent make cannot leak
// a jobserver or nested-make flag into the read.
func makePrintCheckTargets(root string) (string, error) {
	//testenv:allow the helper forwards the parent environment to make, minus MAKEFLAGS
	environ := os.Environ()
	env := make([]string, 0, len(environ))
	for _, entry := range environ {
		if !strings.HasPrefix(entry, "MAKEFLAGS=") {
			env = append(env, entry)
		}
	}
	command := exec.Command("make", "-s", "--no-print-directory", "-C", root, "print-check-targets")
	env = append(env, "MAKEFLAGS=")
	command.Env = env
	out, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, out)
	}
	return string(out), nil
}

// newMapRepo builds the baseline repo layout the map's cases modify.
func newMapRepo(tb testing.TB) *gittest.Repo {
	tb.Helper()
	repo := gittest.New(tb)
	repo.Write("internal/foo/foo.go", "package foo\n")
	repo.Write("cmd/app/main.go", "package main\n")
	repo.Write("README.md", "# t\n")
	repo.Write("docs/guide.md", "# d\n")
	repo.Write("Makefile", "")
	repo.Write(".operator-shared/specs/x.md", "# b\n")
	repo.Commit("base")
	return repo
}

// newMapDivergedRepo builds a repo where feature changes a docs page and main
// advances after the divergence, so only the merge base is correct.
func newMapDivergedRepo(tb testing.TB) *gittest.Repo {
	tb.Helper()
	repo := newMapRepo(tb)
	base, err := repo.Head()
	if err != nil {
		tb.Fatalf("read HEAD: %v", err)
	}

	repo.SetBranch("feature", base)
	repo.Checkout("feature")
	repo.Write("docs/guide.md", "# d3\n")
	repo.Commit("feature docs")

	repo.Checkout("main")
	repo.Write("Makefile", "all:\n")
	repo.Commit("main unreconciled")

	repo.Checkout("feature")
	return repo
}

// writeFailingLSFilesGitShim writes a `git` that fails on ls-files and delegates
// everything else to the real git, returning the shim directory.
func writeFailingLSFilesGitShim(tb testing.TB) string {
	tb.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		tb.Fatalf("locate git: %v", err)
	}
	shimDir := tb.TempDir()
	shim := "#!/usr/bin/env bash\n" +
		"for arg in \"$@\"; do\n" +
		"  if [[ \"$arg\" == \"ls-files\" ]]; then\n" +
		"    printf 'simulated ls-files failure\\n' >&2\n" +
		"    exit 1\n" +
		"  fi\n" +
		"done\n" +
		"exec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		tb.Fatalf("write git shim: %v", err)
	}
	return shimDir
}

// runMap runs scripts/changed-targets.sh against repoDir.
func runMap(t *testing.T, repoDir, mode, base string, extra map[string]string) testutil.ScriptResult {
	t.Helper()
	args := []string{"--repo", repoDir}
	if base != "" {
		args = append(args, "--base", base)
	}
	args = append(args, mode)
	env := map[string]string{}
	maps.Copy(env, extra)
	return testutil.RunScript(t, repoDir, env, repositoryScript(t, "changed-targets.sh"), args...)
}

// assertSorted compares the newline-separated selected targets, sorted, to want.
func assertSorted(t *testing.T, name, stdout string, want ...string) {
	t.Helper()
	got := splitSortedLines(stdout)
	sorted := slices.Clone(want)
	slices.Sort(sorted)
	if !slices.Equal(got, sorted) {
		t.Errorf("%s: selected %v, want %v", name, got, sorted)
	}
}

// splitSortedLines splits a newline-separated list, dropping empties, sorted.
func splitSortedLines(stdout string) []string {
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	slices.Sort(lines)
	return lines
}
