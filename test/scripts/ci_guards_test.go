package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCIGuards ports every case of the bash harness ci/test-check-ci-guards.sh.
// It drives the real ci/check-ci-guards.sh against throwaway fixture roots whose
// four exempt-path sites agree, then mutates one site at a time (or a
// workflow-condition / release invariant) and asserts the checker's exit status
// and diagnostics. It creates no git fixtures, so the template's poison run is
// skipped (the harness never runs git).
func TestCIGuards(t *testing.T) {
	t.Parallel()
	t.Run("canonical fixture passes", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		ciGuardsExpect(t, f.root, 0, "")
	})

	t.Run("mutation.yml dropping an entry", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.removeLines(".github/workflows/mutation.yml", "Formula/**", false)
		ciGuardsExpect(t, f.root, 1, "mutation.yml")
	})

	t.Run("mutation.yml dropping .agents-sandbox", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.removeLines(".github/workflows/mutation.yml", ".agents-sandbox/**", false)
		ciGuardsExpect(t, f.root, 1, "mutation.yml")
	})

	t.Run("codeql.yml adding an entry", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(
			".github/workflows/codeql.yml",
			"      - '.gitignore'",
			"      - '.gitignore'\n      - 'src/**'",
		)
		ciGuardsExpect(t, f.root, 1, "codeql.yml")
	})

	t.Run("codeql.yml drifting one paths-ignore block", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.removeLines(".github/workflows/codeql.yml", ".gitignore", true)
		ciGuardsExpect(t, f.root, 1, "codeql.yml")
	})

	t.Run("codeql.yml losing a whole paths-ignore block", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(".github/workflows/codeql.yml", ciGuardsCodeQLPullRequestBlock, "")
		ciGuardsExpect(t, f.root, 1, "expected 2 paths-ignore block")
	})

	t.Run("classifier regex renaming an entry", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll("ci/classify-changes.sh", "Formula/", "Formulae/")
		ciGuardsExpect(t, f.root, 1, "ci/classify-changes.sh")
	})

	t.Run("check-changelog.sh dropping the *_test.go exemption", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.removeLines("ci/check-changelog.sh", "_test", false)
		ciGuardsExpect(t, f.root, 1, "check-changelog.sh")
	})

	t.Run("classifier regex gaining an unmodeled alternative", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace("ci/classify-changes.sh", `^\.gitignore$`, `^Makefile$|^\.gitignore$`)
		ciGuardsExpect(t, f.root, 1, "unrecognized alternative")
	})

	t.Run("derivation diagnostics are counted and kept out of the glob set", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace("ci/classify-changes.sh", `^\.gitignore$`, `^\.gitignore$|^Foo$|^Bar$`)
		ciGuardsExpect(t, f.root, 1, "2 violation(s) found")
	})

	t.Run("check-changelog.sh dropping the .gitignore clause", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace("ci/check-changelog.sh", ` && [[ "$line" != ".gitignore" ]]`, "")
		ciGuardsExpect(t, f.root, 1, "check-changelog.sh")
	})

	t.Run("check-changelog.sh reversing the .md exemption", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace("ci/check-changelog.sh", `[[ ! "$line" =~ \.md$ ]]`, `[[ "$line" =~ \.md$ ]]`)
		ciGuardsExpect(t, f.root, 1, "un-negated exempt test")
	})

	t.Run("check-changelog.sh reversing the *_test.go exemption", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace("ci/check-changelog.sh", `[[ ! "$line" =~ _test\.go$ ]]`, `[[ "$line" =~ _test\.go$ ]]`)
		ciGuardsExpect(t, f.root, 1, "un-negated exempt test")
	})

	t.Run("undeclared github.event_name literal", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(
			".github/workflows/ci.yml",
			"github.event_name != 'merge_group'",
			"github.event_name != 'scheduled'",
		)
		ciGuardsExpect(t, f.root, 1, "not declared in on")
	})

	t.Run("declared event with a trailing comment", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(".github/workflows/ci.yml", "  merge_group:\n", "  merge_group:  # queue ref\n")
		ciGuardsExpect(t, f.root, 0, "")
	})

	t.Run("pull_request.types dropping a default", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(
			".github/workflows/ci.yml",
			"types: [opened, synchronize, reopened, labeled]",
			"types: [opened, labeled]",
		)
		ciGuardsExpect(t, f.root, 1, "default type 'synchronize'")
	})

	t.Run("pull_request.types dropping labeled", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(
			".github/workflows/ci.yml",
			"types: [opened, synchronize, reopened, labeled]",
			"types: [opened, synchronize, reopened]",
		)
		ciGuardsExpect(t, f.root, 1, "labeled")
	})

	t.Run("pull_request.types block-sequence spelling", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(
			".github/workflows/ci.yml",
			"    types: [opened, synchronize, reopened, labeled]",
			"    types:\n      - opened\n      - synchronize\n      - reopened\n      - labeled",
		)
		ciGuardsExpect(t, f.root, 0, "")
	})

	t.Run("pull_request.types block-sequence dropping a default", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(
			".github/workflows/ci.yml",
			"    types: [opened, synchronize, reopened, labeled]",
			"    types:\n      - opened\n      - labeled",
		)
		ciGuardsExpect(t, f.root, 1, "default type 'synchronize'")
	})

	t.Run("pull_request with no types declared", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.removeLines(".github/workflows/ci.yml", "    types: [opened", true)
		ciGuardsExpect(t, f.root, 1, "declares no types")
	})

	t.Run("classifier exec rule losing its ^docs/ anchor", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace("ci/classify-changes.sh", "grep -qE '^docs/'", "grep -qE 'docs/'")
		ciGuardsExpect(t, f.root, 1, "missing the exec rule")
	})

	t.Run("classifier exec rule no longer derived from code", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace("ci/classify-changes.sh", `[ "$code" = true ]`, `[ "$code" = false ]`)
		ciGuardsExpect(t, f.root, 1, "does not derive exec from code")
	})

	t.Run("changes dropping the classifier invocation", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(
			".github/workflows/ci.yml",
			"ci/classify-changes.sh /tmp/changed.txt",
			`grep -vqE "x" /tmp/changed.txt`,
		)
		ciGuardsExpect(t, f.root, 1, "does not call ci/classify-changes.sh")
	})

	t.Run("classifier re-inlining a producer into grep -q", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(
			"ci/classify-changes.sh",
			"code=false\n",
			"printf \"%s\\n\" \"$changed_files\" | grep -q x || true\ncode=false\n",
		)
		ciGuardsExpect(t, f.root, 1, "pipes a producer into grep -q")
	})

	t.Run("classifier piping cat into grep -vq", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(
			"ci/classify-changes.sh",
			"code=false\n",
			"cat \"$changed_files\" | grep -vq x || true\ncode=false\n",
		)
		ciGuardsExpect(t, f.root, 1, "pipes a producer into grep -q")
	})

	t.Run("changes step piping a git diff into grep -q", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(
			".github/workflows/ci.yml",
			`output="$(ci/classify-changes.sh /tmp/changed.txt)"`,
			`output="$(git diff --no-renames --name-only "$base" "$GITHUB_SHA" | grep -q foo)"`,
		)
		ciGuardsExpect(t, f.root, 1, "pipes a producer into grep -q")
	})

	t.Run("changes dropping the forced-full-set tag rule", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.removeLines(".github/workflows/ci.yml", "== refs/tags/", false)
		ciGuardsExpect(t, f.root, 1, "refs/tags/")
	})

	t.Run("changes hiding a failed diff behind || true", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(
			".github/workflows/ci.yml",
			`git diff --no-renames --name-only "$base" "$GITHUB_SHA" > /tmp/changed.txt`,
			`git diff --no-renames --name-only "$base" "$GITHUB_SHA" > /tmp/changed.txt || true`,
		)
		ciGuardsExpect(t, f.root, 1, "hides a failed diff")
	})

	t.Run("changes dropping --no-renames", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(".github/workflows/ci.yml", "git diff --no-renames --name-only", "git diff --name-only")
		ciGuardsExpect(t, f.root, 1, "--no-renames")
	})

	t.Run("a code consumer gating on code == 'true'", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceInJob(
			"vulncheck",
			"needs.changes.outputs.code != 'false'",
			"needs.changes.outputs.code == 'true'",
			true,
		)
		ciGuardsExpect(t, f.root, 1, "outputs.code != 'false'")
	})

	t.Run("integration whole-block code gate reverted to == 'true'", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceInJob(
			"integration",
			"needs.changes.outputs.code != 'false'",
			"needs.changes.outputs.code == 'true'",
			true,
		)
		ciGuardsExpect(t, f.root, 1, "'integration' if: is missing needs.changes.outputs.code != 'false'")
	})

	t.Run("a gated job dropping needs: changes", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.removeLines(".github/workflows/ci.yml", "    needs: [changes]", true)
		ciGuardsExpect(t, f.root, 1, "does not include 'changes'")
	})

	t.Run("a Go job job-level if: dropping !cancelled()", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(
			".github/workflows/ci.yml",
			"    if: ${{ !cancelled() }}\n",
			"    if: ${{ success() }}\n",
		)
		ciGuardsExpect(t, f.root, 1, "'lint' job-level if: is missing !cancelled()")
	})

	t.Run("body-step gate dropping merge_group", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(".github/workflows/ci.yml", "github.event_name == 'merge_group' || ", "")
		ciGuardsExpect(t, f.root, 1, "missing merge_group")
	})

	t.Run("one gate in a multi-gate job dropping merge_group", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(
			".github/workflows/ci.yml",
			"      - run: make coverage-junit\n"+
				"        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}\n",
			"      - run: make coverage-junit\n"+
				"        if: ${{ needs.changes.outputs.exec != 'false' }}\n",
		)
		ciGuardsExpect(t, f.root, 1, "missing merge_group")
	})

	t.Run("job missing timeout-minutes", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.removeLines(".github/workflows/ci.yml", "    timeout-minutes:", true)
		ciGuardsExpect(t, f.root, 1, "missing a job-level timeout-minutes")
	})

	t.Run("changes re-inlining the merge-base", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replace(".github/workflows/ci.yml", "ci/event-base.sh", `git merge-base origin/main "$GITHUB_SHA"`)
		ciGuardsExpect(t, f.root, 1, "'changes' does not call ci/event-base.sh")
	})

	t.Run("changelog not calling ci/event-base.sh", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceNth(".github/workflows/ci.yml", "ci/event-base.sh", `git merge-base origin/main "$GITHUB_SHA"`, 2)
		ciGuardsExpect(t, f.root, 1, "'changelog' does not call ci/event-base.sh")
	})

	t.Run("on.push.branches dropping release/*", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(".github/workflows/ci.yml", ", 'release/*'", "")
		ciGuardsExpect(t, f.root, 1, "release/*")
	})

	t.Run("deleting the tag-guard job", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.removeJob("tag-guard")
		ciGuardsExpect(t, f.root, 1, "the 'tag-guard' job is missing")
	})

	t.Run("tag-guard if: dropping the tag gate", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceInJob("tag-guard", "if: startsWith(github.ref, 'refs/tags/')", "if: true", false)
		ciGuardsExpect(t, f.root, 1, "startsWith(github.ref, 'refs/tags/')")
	})

	t.Run("tag-guard leaving its gate token only in a comment", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceInJob(
			"tag-guard",
			"    if: startsWith(github.ref, 'refs/tags/')",
			"    # startsWith(github.ref, 'refs/tags/')\n    if: true",
			false,
		)
		ciGuardsExpect(t, f.root, 1, "startsWith(github.ref, 'refs/tags/')")
	})

	t.Run("release.needs dropping tag-guard", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(".github/workflows/ci.yml", "needs: [tag-guard, ", "needs: [")
		ciGuardsExpect(t, f.root, 1, "'release' needs: does not include 'tag-guard'")
	})

	t.Run("release.needs dropping integration", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(
			".github/workflows/ci.yml",
			"needs: [tag-guard, lint, test, integration, integration-podman, hermetic]",
			"needs: [tag-guard, lint, test, integration-podman, hermetic]",
		)
		ciGuardsExpect(t, f.root, 1, "'release' needs: does not include 'integration'")
	})

	t.Run("release.needs dropping hermetic", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(
			".github/workflows/ci.yml",
			"needs: [tag-guard, lint, test, integration, integration-podman, hermetic]",
			"needs: [tag-guard, lint, test, integration, integration-podman]",
		)
		ciGuardsExpect(t, f.root, 1, "'release' needs: does not include 'hermetic'")
	})

	t.Run("cache writer losing the main-only guard", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceAll(
			".github/workflows/ci.yml",
			"github.ref == 'refs/heads/main'",
			"startsWith(github.ref, 'refs/heads/')",
		)
		ciGuardsExpect(t, f.root, 1, "refs/heads/main")
	})

	t.Run("release-push clause dropping the code gate", func(t *testing.T) {
		t.Parallel()
		f := newCIGuardsFixture(t)
		f.replaceInJob("integration", " && needs.changes.outputs.code != 'false'", "", false)
		ciGuardsExpect(t, f.root, 1, "release-push clause is missing")
	})

	t.Run("this repository", func(t *testing.T) {
		t.Parallel()
		ciGuardsExpect(t, repositoryRoot(t), 0, "")
	})
}

// ciGuardsExpect runs the checker against root and asserts its exit status and,
// when non-empty, a substring of its combined output.
func ciGuardsExpect(t *testing.T, root string, wantStatus int, wantSubstring string) {
	t.Helper()
	result := runCIGuards(t, root)
	if result.Status != wantStatus {
		t.Errorf(
			"exit status = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			result.Status, wantStatus, result.Stdout, result.Stderr,
		)
		return
	}
	if wantSubstring == "" {
		return
	}
	if combined := result.Stdout + result.Stderr; !strings.Contains(combined, wantSubstring) {
		t.Errorf("output does not contain %q\nstdout:\n%s\nstderr:\n%s", wantSubstring, result.Stdout, result.Stderr)
	}
}

// runCIGuards runs the real ci/check-ci-guards.sh against root.
func runCIGuards(t *testing.T, root string) ScriptResult {
	t.Helper()
	repository := repositoryRoot(t)
	return RunScript(t, repository, nil, filepath.Join(repository, "ci", "check-ci-guards.sh"), root)
}

// ciGuardsFixture is a throwaway root whose four exempt-path sites agree.
type ciGuardsFixture struct {
	t    *testing.T
	root string
}

// newCIGuardsFixture writes the canonical fixture files into a fresh temp root.
func newCIGuardsFixture(t *testing.T) *ciGuardsFixture {
	t.Helper()
	f := &ciGuardsFixture{t: t, root: t.TempDir()}
	f.write(".github/workflows/ci.yml", ciGuardsCIContent)
	f.write("ci/classify-changes.sh", ciGuardsClassifyContent)
	f.write(".github/workflows/mutation.yml", ciGuardsMutationContent)
	f.write(".github/workflows/codeql.yml", ciGuardsCodeQLContent)
	f.write("ci/check-changelog.sh", ciGuardsChangelogContent)
	return f
}

// path resolves a fixture-relative path.
func (f *ciGuardsFixture) path(rel string) string {
	return filepath.Join(f.root, rel)
}

// write stores content at a fixture-relative path, creating its parent dirs.
func (f *ciGuardsFixture) write(rel, content string) {
	f.t.Helper()
	path := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		f.t.Fatalf("create dir for %s: %v", rel, err)
	}
	writeDoc(f.t, path, content)
}

// contents reads a fixture-relative path.
func (f *ciGuardsFixture) contents(rel string) string {
	f.t.Helper()
	return readText(f.t, f.path(rel))
}

// replace rewrites the first occurrence of old with replacement.
func (f *ciGuardsFixture) replace(rel, old, replacement string) {
	f.t.Helper()
	content := f.contents(rel)
	updated := strings.Replace(content, old, replacement, 1)
	f.requireChange(rel, old, content, updated)
	f.write(rel, updated)
}

// replaceAll rewrites every occurrence of old with replacement.
func (f *ciGuardsFixture) replaceAll(rel, old, replacement string) {
	f.t.Helper()
	content := f.contents(rel)
	updated := strings.ReplaceAll(content, old, replacement)
	f.requireChange(rel, old, content, updated)
	f.write(rel, updated)
}

// replaceNth rewrites the nth (1-based) occurrence of old with replacement.
func (f *ciGuardsFixture) replaceNth(rel, old, replacement string, nth int) {
	f.t.Helper()
	content := f.contents(rel)
	parts := strings.SplitN(content, old, nth+1)
	if len(parts) < nth+1 {
		f.t.Fatalf("%s has fewer than %d occurrences of %q", rel, nth, old)
	}
	parts[nth-1] = replacement
	updated := strings.Join(parts, old)
	f.requireChange(rel, old, content, updated)
	f.write(rel, updated)
}

// removeLines drops every line containing substring, or only the first when
// firstOnly is set.
func (f *ciGuardsFixture) removeLines(rel, substring string, firstOnly bool) {
	f.t.Helper()
	content := f.contents(rel)
	var builder strings.Builder
	removed := false
	for line := range strings.SplitSeq(content, "\n") {
		if strings.Contains(line, substring) && (!firstOnly || !removed) {
			removed = true
			continue
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	if !removed {
		f.t.Fatalf("%s has no line containing %q", rel, substring)
	}
	// SplitSeq drops the final empty segment, so the rebuild always ends with a
	// newline; the originals do too.
	f.write(rel, builder.String())
}

// replaceInJob rewrites old with new inside the ci.yml job named job, every
// occurrence when all is set.
func (f *ciGuardsFixture) replaceInJob(job, old, replacement string, all bool) {
	f.t.Helper()
	rel := ".github/workflows/ci.yml"
	content := f.contents(rel)
	start, end, ok := ciGuardsJobBounds(content, job)
	if !ok {
		f.t.Fatalf("job %q not found", job)
	}
	block := content[start:end]
	updated := strings.Replace(block, old, replacement, 1)
	if all {
		updated = strings.ReplaceAll(block, old, replacement)
	}
	f.requireChange(job, old, block, updated)
	f.write(rel, content[:start]+updated+content[end:])
}

// removeJob deletes the ci.yml job named job.
func (f *ciGuardsFixture) removeJob(job string) {
	f.t.Helper()
	rel := ".github/workflows/ci.yml"
	content := f.contents(rel)
	start, end, ok := ciGuardsJobBounds(content, job)
	if !ok {
		f.t.Fatalf("job %q not found", job)
	}
	f.write(rel, content[:start]+content[end:])
}

// requireChange fails when a mutation left the target text unchanged.
func (f *ciGuardsFixture) requireChange(label, old, before, after string) {
	f.t.Helper()
	if before == after {
		f.t.Fatalf("mutation of %s made no change for %q", label, old)
	}
}

// ciGuardsJobBounds returns the byte offsets of the ci.yml job named job: from
// its `  <job>:` line to the next top-level job key line (or end of file).
func ciGuardsJobBounds(content, job string) (int, int, bool) {
	offset := 0
	start := -1
	for _, line := range strings.SplitAfter(content, "\n") {
		text := strings.TrimSuffix(line, "\n")
		if start < 0 {
			if text == "  "+job+":" {
				start = offset
			}
		} else if ciGuardsIsJobKey(text) {
			return start, offset, true
		}
		offset += len(line)
	}
	if start < 0 {
		return 0, 0, false
	}
	return start, len(content), true
}

// ciGuardsIsJobKey reports whether line is a two-space-indented top-level job
// key such as `  tag-guard:`.
func ciGuardsIsJobKey(line string) bool {
	if len(line) < 3 || line[0] != ' ' || line[1] != ' ' || line[2] == ' ' {
		return false
	}
	rest := line[2:]
	if !strings.HasSuffix(rest, ":") {
		return false
	}
	name := rest[:len(rest)-1]
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// ciGuardsCodeQLPullRequestBlock is codeql.yml's second paths-ignore block (the
// pull_request trigger), removed whole by one case.
const ciGuardsCodeQLPullRequestBlock = `  pull_request:
    paths-ignore:
      - '**.md'
      - 'docs/**'
      - '.operator-shared/**'
      - 'Formula/**'
      - '.github/ISSUE_TEMPLATE/**'
      - '.agents-sandbox/**'
      - '.gitignore'
`

// ciGuardsCIContent mirrors the real ci.yml's structure: job keys at two-space
// indent, their keys at four, so the checker's job-scoped extraction works and
// the one-line mutations land where intended.
const ciGuardsCIContent = `on:
  push:
    branches: [main, 'release/*']
  pull_request:
    types: [opened, synchronize, reopened, labeled]
  merge_group:
  workflow_dispatch:
  schedule:
    - cron: "43 2 * * *"

jobs:
  changes:
    name: changes
    if: github.event_name != 'merge_group'
    runs-on: ubuntu-latest
    timeout-minutes: 5
    outputs:
      code: ${{ steps.filter.outputs.code }}
      exec: ${{ steps.filter.outputs.exec }}
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - name: Classify the changeset
        id: filter
        env:
          GITHUB_EVENT_NAME: ${{ github.event_name }}
          GITHUB_REF: ${{ github.ref }}
        run: |
          set -euo pipefail
          code=false
          exec=false
          if base=$(ci/event-base.sh) && git diff --no-renames --name-only "$base" "$GITHUB_SHA" > /tmp/changed.txt; then
            output="$(ci/classify-changes.sh /tmp/changed.txt)"
            code="$(printf '%s\n' "$output" | sed -n 's/^code=//p')"
            exec="$(printf '%s\n' "$output" | sed -n 's/^exec=//p')"
          else
            code=true
            exec=true
          fi
          if [[ "${GITHUB_REF:-}" == refs/tags/* ]]; then
            code=true
            exec=true
          fi
          echo "code=$code" >> "$GITHUB_OUTPUT"
          echo "exec=$exec" >> "$GITHUB_OUTPUT"

  changelog:
    name: changelog
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/checkout@v7
      - name: Compute changed files
        run: |
          set -euo pipefail
          base=$(ci/event-base.sh || true)
          git diff --no-renames --name-only "${base:-}" "$GITHUB_SHA" > /tmp/changed.txt 2>/dev/null || true

  lint:
    name: lint
    needs: [changes]
    if: ${{ !cancelled() }}
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - uses: golangci/golangci-lint-action@v9
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}
        with:
          version: v2.15
      - run: make docs-linkcheck

  test:
    name: test
    needs: [changes]
    if: ${{ !cancelled() }}
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - run: make coverage-junit
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}
      - uses: codecov/codecov-action@v7
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}
      - name: Upload test results to Codecov
        if: ${{ !cancelled() && (github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false') }}
        uses: codecov/codecov-action@v7

  race:
    name: race
    needs: [changes]
    if: ${{ !cancelled() }}
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - run: make test-race
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}

  deadcode:
    name: deadcode
    needs: [changes]
    if: ${{ !cancelled() }}
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - run: make deadcode
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}

  vulncheck:
    name: vulncheck
    needs: [changes]
    if: >-
      !cancelled() &&
      github.event_name != 'merge_group' &&
      (github.event_name == 'workflow_dispatch' ||
      startsWith(github.ref, 'refs/tags/') ||
      needs.changes.outputs.code != 'false')
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - run: make vulncheck

  integration:
    name: integration (docker build)
    needs: [changes]
    if: >-
      !cancelled() &&
      (github.event_name == 'workflow_dispatch' ||
      startsWith(github.ref, 'refs/tags/') ||
      (github.event_name == 'push' && startsWith(github.ref, 'refs/heads/release/') && needs.changes.outputs.code != 'false') ||
      (github.event_name == 'pull_request' && needs.changes.outputs.code != 'false'))
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v7
      - run: make test-integration

  integration-cache-writer:
    name: integration (docker build, cache writer)
    needs: [changes]
    if: >-
      !cancelled() &&
      github.event_name == 'push' &&
      github.ref == 'refs/heads/main' &&
      needs.changes.outputs.code != 'false'
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v7
      - run: make test-integration

  integration-fresh:
    name: integration (docker build, fresh)
    if: github.event_name == 'schedule'
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v7
      - run: make test-integration

  hermetic:
    name: hermetic
    needs: [changes]
    if: ${{ !cancelled() && github.event_name != 'merge_group' }}
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: .go-version
          cache: true
      - name: Enable rootless user namespaces for the envelope's network namespace
        if: ${{ startsWith(github.ref, 'refs/tags/') || needs.changes.outputs.exec != 'false' }}
        run: sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
      - name: Warm the module cache for the offline envelope
        if: ${{ startsWith(github.ref, 'refs/tags/') || needs.changes.outputs.exec != 'false' }}
        run: go mod download
      - name: Run the hermetic envelope
        if: ${{ startsWith(github.ref, 'refs/tags/') || needs.changes.outputs.exec != 'false' }}
        run: make test-hermetic

  tag-guard:
    name: tag-guard
    if: startsWith(github.ref, 'refs/tags/')
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v7
      - run: make tag-guard

  release:
    name: release
    if: startsWith(github.ref, 'refs/tags/')
    needs: [tag-guard, lint, test, integration, integration-podman, hermetic]
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v7
      - run: make build-release
`

// ciGuardsClassifyContent is the fixture ci/classify-changes.sh the changes
// regex is derived from.
const ciGuardsClassifyContent = `#!/usr/bin/env bash
set -euo pipefail
changed_files="$1"
code=false
if grep -vqE '^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/|\.agents-sandbox/)|\.md$|^\.gitignore$' "$changed_files"; then
  code=true
fi
exec=false
if [ "$code" = true ] || grep -qE '^docs/' "$changed_files"; then
  exec=true
fi
printf 'code=%s\n' "$code"
printf 'exec=%s\n' "$exec"
`

// ciGuardsMutationContent is the fixture mutation.yml with one paths-ignore block.
const ciGuardsMutationContent = `on:
  pull_request:
    paths-ignore:
      - '**.md'
      - 'docs/**'
      - '.operator-shared/**'
      - 'Formula/**'
      - '.github/ISSUE_TEMPLATE/**'
      - '.agents-sandbox/**'
      - '.gitignore'
  workflow_dispatch:
`

// ciGuardsCodeQLContent is the fixture codeql.yml, carrying the same list under
// both push and pull_request.
const ciGuardsCodeQLContent = `on:
  push:
    paths-ignore:
      - '**.md'
      - 'docs/**'
      - '.operator-shared/**'
      - 'Formula/**'
      - '.github/ISSUE_TEMPLATE/**'
      - '.agents-sandbox/**'
      - '.gitignore'
  pull_request:
    paths-ignore:
      - '**.md'
      - 'docs/**'
      - '.operator-shared/**'
      - 'Formula/**'
      - '.github/ISSUE_TEMPLATE/**'
      - '.agents-sandbox/**'
      - '.gitignore'
`

// ciGuardsChangelogContent is the fixture ci/check-changelog.sh. It mirrors the
// real file's decoys: an IGNORE list naming `.gitignore` and `.md`, and a
// comment doing the same, so an unanchored whole-file grep would still see them.
const ciGuardsChangelogContent = `#!/usr/bin/env bash
set -euo pipefail
# The *.md and .gitignore mentions in this comment, and in the IGNORE list, are
# decoys: only the presence condition below grants an exemption.
IGNORE='README.md README.rst README .gitignore .gitkeep .keep'
check_presence() {
  while IFS= read -r line; do
    if [[ ! "$line" =~ ^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/|\.agents-sandbox/) ]] \
      && [[ ! "$line" =~ \.md$ ]] \
      && [[ ! "$line" =~ _test\.go$ ]] \
      && [[ "$line" != ".gitignore" ]]; then
      code=1
    fi
  done
}
`
