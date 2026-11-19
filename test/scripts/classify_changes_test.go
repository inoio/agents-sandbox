package scripts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// classifyChangesLargeCount is the number of paths the bash harness wrote into
// each large fixture. At this count the code-only list is ~123 KiB, well past
// the 64 KiB Linux pipe capacity the old inline pipeline died on, so the
// regression case is exercised.
const classifyChangesLargeCount = 4000

// classifyChangesScript returns the absolute path of ci/classify-changes.sh.
func classifyChangesScript(tb testing.TB) string {
	tb.Helper()
	return filepath.Join(repositoryRoot(tb), "ci", "classify-changes.sh")
}

// classifyChangesFixture writes a changed-file list in the test's temp dir and
// returns its path.
func classifyChangesFixture(tb testing.TB, name, content string) string {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatalf("write fixture %s: %v", path, err)
	}
	return path
}

// classifyChangesNumbered builds the `seq 1 count | sed 's#^#prefix#'`-style
// list the bash harness used: classifyChangesLargeCount lines, each prefix
// followed by a 1-based index and the suffix.
func classifyChangesNumbered(prefix, suffix string) string {
	var builder strings.Builder
	for i := 1; i <= classifyChangesLargeCount; i++ {
		fmt.Fprintf(&builder, "%s%d%s\n", prefix, i, suffix)
	}
	return builder.String()
}

// runClassifyChanges runs classify-changes.sh over a changed-file list,
// returning its captured result.
func runClassifyChanges(tb testing.TB, listPath string) ScriptResult {
	tb.Helper()
	return RunScript(tb, tb.TempDir(), nil, classifyChangesScript(tb), listPath)
}

// assertClassifyChanges asserts the classifier exited 0 and printed exactly the
// wanted `code=`/`exec=` pair.
func assertClassifyChanges(t *testing.T, result ScriptResult, wantCode, wantExec bool) {
	t.Helper()
	want := fmt.Sprintf("code=%t\nexec=%t\n", wantCode, wantExec)
	assertStatus(t, result, 0)
	if result.Stdout != want {
		t.Errorf("stdout = %q, want %q\nstderr:\n%s", result.Stdout, want, result.Stderr)
	}
}

// TestClassifyChanges ports every case of the bash harness
// ci/test-check-classify-changes.sh, asserting the same exit codes and
// stdout/stderr substrings.
//
// The large code-only case is the regression guard: the old inline
// `printf ... | grep -q` classification failed open on a list larger than the
// ~64 KiB pipe buffer, silently skipping every required job.
func TestClassifyChanges(t *testing.T) {
	t.Parallel()
	t.Run("large code-only list", func(t *testing.T) {
		t.Parallel()
		content := classifyChangesNumbered("internal/sandbox/pkg/file_", ".go")
		if len(content) <= 65536 {
			t.Fatalf(
				"large-code fixture is %d bytes, want > 65536 (the regression case is not exercised)",
				len(content),
			)
		}
		list := classifyChangesFixture(t, "code-large.txt", content)

		assertClassifyChanges(t, runClassifyChanges(t, list), true, true)
	})

	t.Run("large docs-only list", func(t *testing.T) {
		t.Parallel()
		content := classifyChangesNumbered("docs/page_", ".md")
		list := classifyChangesFixture(t, "docs-large.txt", content)

		assertClassifyChanges(t, runClassifyChanges(t, list), false, true)
	})

	t.Run("large inert-only list", func(t *testing.T) {
		t.Parallel()
		content := classifyChangesNumbered("notes_", ".md")
		list := classifyChangesFixture(t, "inert-large.txt", content)

		assertClassifyChanges(t, runClassifyChanges(t, list), false, false)
	})

	t.Run("mixed code+docs list", func(t *testing.T) {
		t.Parallel()
		list := classifyChangesFixture(t, "mixed.txt", "src/main.go\ndocs/guide.md\n")

		assertClassifyChanges(t, runClassifyChanges(t, list), true, true)
	})

	t.Run("all-.operator-shared list", func(t *testing.T) {
		t.Parallel()
		content := classifyChangesNumbered(".operator-shared/note_", "")
		list := classifyChangesFixture(t, "shared.txt", content)

		assertClassifyChanges(t, runClassifyChanges(t, list), false, false)
	})

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()
		list := classifyChangesFixture(t, "empty.txt", "")

		assertClassifyChanges(t, runClassifyChanges(t, list), false, false)
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		result := runClassifyChanges(t, filepath.Join(t.TempDir(), "does-not-exist.txt"))

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "classify-changes")
		assertNotContains(t, "stdout", result.Stdout, "code=")
	})
}
