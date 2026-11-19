package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChangelog ports every case of the bash harness ci/test-check-changelog.sh,
// driving ci/check-changelog.sh against changelog.d/ fixtures and asserting the
// same exit codes and output substrings. The harness builds fixture trees only:
// it creates no git repository and the checker runs no git, so there is no poison
// run to make.
func TestChangelog(t *testing.T) {
	t.Parallel()
	t.Run("valid names", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "432.added.md", "431.fixed.md")
		result := runChangelog(t, dir)
		assertStatus(t, result, 0)
	})

	t.Run("valid ordinal names", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "103.added-1.md", "103.added-2.md", "103.changed-1.md")
		result := runChangelog(t, dir)
		assertStatus(t, result, 0)
	})

	t.Run("non-numeric ordinal", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "103.changed-x.md")
		result := runChangelog(t, dir)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "does not match")
	})

	t.Run("unknown type", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "432.feature.md")
		result := runChangelog(t, dir)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "unknown type")
	})

	t.Run("missing type", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "432.md")
		result := runChangelog(t, dir)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "does not match")
	})

	t.Run("wrong suffix", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "432.added.txt")
		result := runChangelog(t, dir)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "does not match")
	})

	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir)
		assertStatus(t, result, 0)
	})

	t.Run("absent directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := runChangelog(t, dir)
		assertStatus(t, result, 0)
	})

	t.Run("ignored names", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "README.md", ".gitkeep")
		result := runChangelog(t, dir)
		assertStatus(t, result, 0)
	})

	t.Run("code, no fragment", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir, changelogChangedPath(t, "internal/foo/foo.go")...)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "no changelog.d")
	})

	t.Run("code, with fragment", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "432.added.md")
		result := runChangelog(t, dir, changelogChangedPath(t, "internal/foo/foo.go", "changelog.d/432.added.md")...)
		assertStatus(t, result, 0)
	})

	t.Run("docs only", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir, changelogChangedPath(t, "docs/commands.md")...)
		assertStatus(t, result, 0)
	})

	t.Run("markdown only", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir, changelogChangedPath(t, "README.md")...)
		assertStatus(t, result, 0)
	})

	t.Run("gitignore only", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir, changelogChangedPath(t, ".gitignore")...)
		assertStatus(t, result, 0)
	})

	t.Run("release PR, all markdown", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir, changelogChangedPath(t, "CHANGELOG.md", "changelog.d/432.added.md")...)
		assertStatus(t, result, 0)
	})

	t.Run("workflow change", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir, changelogChangedPath(t, ".github/workflows/ci.yml")...)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "no changelog.d")
	})

	t.Run("code, only changelog README", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "README.md")
		result := runChangelog(t, dir, changelogChangedPath(t, "internal/foo/foo.go", "changelog.d/README.md")...)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "no changelog.d")
	})

	t.Run("code, only nested fragment", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		writeChangelogFile(t, filepath.Join(dir, "changelog.d", "sub", "432.added.md"))
		result := runChangelog(
			t,
			dir,
			changelogChangedPath(t, "internal/foo/foo.go", "changelog.d/sub/432.added.md")...)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "no changelog.d")
	})

	t.Run("test-only change", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(
			t,
			dir,
			changelogChangedPath(t, "internal/foo/foo_test.go", "internal/foo/foo_internal_test.go")...)
		assertStatus(t, result, 0)
	})

	t.Run("code plus test", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir, changelogChangedPath(t, "internal/foo/foo.go", "internal/foo/foo_test.go")...)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "no changelog.d")
	})

	t.Run("code, fragment only deleted", func(t *testing.T) {
		t.Parallel()
		// The fragment path is listed as changed but is absent from the tree.
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir, changelogChangedPath(t, "internal/foo/foo.go", "changelog.d/432.added.md")...)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "no changelog.d")
	})

	t.Run("slug matches the PR number", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "432.added.md")
		result := runChangelog(
			t,
			dir,
			append(changelogChangedPath(t, "internal/foo/foo.go", "changelog.d/432.added.md"), "--pr", "432")...)
		assertStatus(t, result, 0)
	})

	t.Run("slug is not the PR number", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t, "431.added.md")
		result := runChangelog(
			t,
			dir,
			append(changelogChangedPath(t, "internal/foo/foo.go", "changelog.d/431.added.md"), "--pr", "432")...)
		assertStatus(t, result, 1)
		assertContains(t, "output", changelogOutput(result), "not the pull-request number")
	})

	t.Run("release PR deleting fragments", func(t *testing.T) {
		t.Parallel()
		// The deleted fragment (an old PR number) must be skipped, not compared
		// to the release PR's number.
		dir := newChangelogFixture(t)
		result := runChangelog(
			t,
			dir,
			append(changelogChangedPath(t, "CHANGELOG.md", "changelog.d/432.added.md"), "--pr", "500")...)
		assertStatus(t, result, 0)
	})

	t.Run("no fragment, nothing to check", func(t *testing.T) {
		t.Parallel()
		dir := newChangelogFixture(t)
		result := runChangelog(t, dir, append(changelogChangedPath(t, "internal/foo/foo_test.go"), "--pr", "432")...)
		assertStatus(t, result, 0)
	})
}

// runChangelog runs ci/check-changelog.sh against a fixture root, with any
// remaining args appended (a `--changed` file and/or `--pr`).
func runChangelog(t *testing.T, dir string, args ...string) ScriptResult {
	t.Helper()
	return RunScript(t, dir, nil, changelogChecker(t), append([]string{dir}, args...)...)
}

// changelogOutput joins a run's streams the way the bash harness's `2>&1`
// capture did, so the substring assertions match on either stream.
func changelogOutput(result ScriptResult) string {
	return result.Stdout + result.Stderr
}

// changelogChecker returns the absolute path of ci/check-changelog.sh, resolved
// from the module root (repositoryScript only covers scripts/).
func changelogChecker(tb testing.TB) string {
	tb.Helper()
	return filepath.Join(repositoryRoot(tb), "ci", "check-changelog.sh")
}

// newChangelogFixture builds a temp root whose changelog.d/ exists and holds one
// placeholder fragment per name, mirroring the bash harness's new_fixture (which
// always `mkdir -p`s the directory, so the empty case is a present-but-empty
// changelog.d, distinct from an absent one).
func newChangelogFixture(tb testing.TB, names ...string) string {
	tb.Helper()
	dir := tb.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "changelog.d"), 0o750); err != nil {
		tb.Fatalf("create changelog.d: %v", err)
	}
	for _, name := range names {
		writeChangelogFile(tb, filepath.Join(dir, "changelog.d", name))
	}
	return dir
}

// writeChangelogFile writes a one-line placeholder fragment at path, creating
// its parent directories.
func writeChangelogFile(tb testing.TB, path string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		tb.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	writeDoc(tb, path, "- x\n")
}

// changelogChangedPath writes the listed paths, one per line as
// `git diff --name-only` emits them, to a temp file and returns the
// `--changed <file>` argument pair.
func changelogChangedPath(tb testing.TB, paths ...string) []string {
	tb.Helper()
	file := filepath.Join(tb.TempDir(), "changed.txt")
	writeDoc(tb, file, strings.Join(paths, "\n")+"\n")
	return []string{"--changed", file}
}
