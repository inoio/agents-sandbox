package scripts

import (
	"strings"
	"testing"
)

// TestBackportAudit ports every case of the bash harness scripts/test-backport-audit.sh,
// asserting the same exit codes, output substrings, gh argv, and debt lines.
func TestBackportAudit(t *testing.T) {
	t.Parallel()
	t.Run("no release branch", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo := New(t)
		repo.Write("f", "a\n")
		repo.Commit("base")
		repo.Write("g", "b\n")
		repo.Commit("fix")

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "no release branch; nothing to audit")
		if argv := h.argv(); argv != "" {
			t.Errorf("asked gh without a release branch: %q", argv)
		}
	})

	t.Run("unported PR", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 1)
		assertContains(t, "stdout", result.Stdout, "#7")
		assertContains(t, "stdout", result.Stdout, "missing:1")
		assertContains(t, "argv", h.argv(), "--label needs-backport")
		assertContains(t, "argv", h.argv(), "--json number,title,url,mergedAt,mergeCommit")
		assertContains(t, "argv", h.argv(), "--limit 5000")
	})

	t.Run("truncated expected set", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, _ := seedAuditRepository(t)

		result := h.run(repo.Dir(), map[string]string{"BACKPORT_AUDIT_PAGE_LIMIT": "5", "GH_MERGED_ROWS": "5"})

		assertStatus(t, result, 2)
		assertContains(t, "stderr", result.Stderr, "gh pr list (merged, labeled)")
		assertContains(t, "stderr", result.Stderr, "truncated")
		assertContains(t, "stderr", result.Stderr, "--limit 5 cap")
		assertContains(t, "argv", h.argv(), "--limit 5")
		if result.Stdout != "" {
			t.Errorf("printed a clean result: %q", result.Stdout)
		}
	})

	t.Run("one row below the cap", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, _ := seedAuditRepository(t)

		result := h.run(repo.Dir(), map[string]string{"BACKPORT_AUDIT_PAGE_LIMIT": "5", "GH_MERGED_ROWS": "4"})

		assertStatus(t, result, 0)
		assertContains(t, "argv", h.argv(), "--state open")
		assertContains(t, "argv", h.argv(), "--limit 5")
		assertNotContains(t, "stderr", result.Stderr, "truncated")
	})

	t.Run("merge commit not in the local clone", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		absent := "0123456789abcdef0123456789abcdef01234567"
		h.writeMerged("2020-01-01T00:00:00Z", absent)

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 2)
		assertContains(t, "stderr", result.Stderr, absent)
		assertContains(t, "stderr", result.Stderr, "#7")
		assertContains(t, "stderr", result.Stderr, "fetch")
		if result.Stdout != "" {
			t.Errorf("printed a clean result: %q", result.Stdout)
		}

		h.writeMerged("2020-01-01T00:00:00Z", merge.String())
		result = h.run(repo.Dir(), nil)

		assertStatus(t, result, 1)
		assertContains(t, "stdout", result.Stdout, "#7")
	})

	t.Run("empty commit", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, _ := seedAuditRepository(t)
		empty := repo.Commit("empty")
		h.writeMerged("2020-01-01T00:00:00Z", empty.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 0)
		assertNotContains(t, "stdout", result.Stdout, "#7")
	})

	t.Run("ported PR", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		repo.Checkout("release/0.1")
		portSameDiff(t, repo, merge, "g", "b\n")
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 0)
		assertNotContains(t, "stdout", result.Stdout, "#7")
	})

	t.Run("merged within grace", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		const now = int64(1700000000)
		h.writeMerged(isoFromEpoch(now-60), merge.String())

		result := h.run(repo.Dir(), map[string]string{
			"BACKPORT_AUDIT_NOW":           "1700000000",
			"BACKPORT_AUDIT_GRACE_SECONDS": "7200",
		})

		assertStatus(t, result, 0)
	})

	t.Run("open backport PR is not a clearance", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		h.writeOpenBackports(auditPull{Number: 8, URL: "https://github.com/o/r/pull/8", Body: "Backport of #7"})
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 1)
		assertContains(t, "stdout", result.Stdout, "open:https://github.com/o/r/pull/8")
	})

	t.Run("merged backport PR clears an adapted port", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		repo.Checkout("release/0.1")
		repo.Write("g", "c\n")
		repo.Commit("adapted g")
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())

		result := h.run(repo.Dir(), nil)
		assertStatus(t, result, 1)

		h.writeMergedBackports(auditPull{Number: 8, URL: "https://github.com/o/r/pull/8", Body: "Backport of #7"})
		result = h.run(repo.Dir(), nil)

		assertStatus(t, result, 0)
		assertContains(t, "argv", h.argv(), "pr list --state merged --base release/0.1")
	})

	t.Run("two open backport PRs", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		h.writeOpenBackports(
			auditPull{Number: 8, URL: "https://github.com/o/r/pull/8", Body: "Backport of #7"},
			auditPull{Number: 9, URL: "https://github.com/o/r/pull/9", Body: "Backport of #7"},
		)
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 1)
		assertContains(t, "stdout", result.Stdout, "open:https://github.com/o/r/pull/8")
	})

	t.Run("a reference alone on a line clears", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())
		h.writeMergedBackports(auditPull{
			Number: 8,
			URL:    "https://github.com/o/r/pull/8",
			Body:   "Hand port:\nhttps://github.com/o/r/pull/7\ncontext differed.",
		})

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 0)
	})

	t.Run("an open reference alone on a line is annotated", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		h.writeOpenBackports(auditPull{
			Number: 8,
			URL:    "https://github.com/o/r/pull/8",
			Body:   "Hand port:\n#7\ncontext differed.",
		})
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 1)
		assertContains(t, "stdout", result.Stdout, "open:https://github.com/o/r/pull/8")
	})

	t.Run("truncated merged backports", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())

		result := h.run(
			repo.Dir(),
			map[string]string{"BACKPORT_AUDIT_PAGE_LIMIT": "5", "GH_MERGED_BACKPORTS_ROWS": "5"},
		)

		assertStatus(t, result, 2)
		assertContains(t, "stderr", result.Stderr, "gh pr list (merged backports)")
	})

	t.Run("unlabeled merged PR", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, _ := seedAuditRepository(t)
		writeDoc(t, h.mergedJSONPath, "[]\n")

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 0)
	})

	t.Run("duplicate patch-id", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		repo.Checkout("release/0.1")
		portSameDiff(t, repo, merge, "g", "b\n")
		repo.Remove("g")
		repo.Commit("revert")
		portSameDiff(t, repo, merge, "g", "b\n")
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 0)
	})

	t.Run("adapted port", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		repo.Checkout("release/0.1")
		repo.Write("g", "c\n")
		repo.Commit("adapted g")
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 1)
		assertContains(t, "stdout", result.Stdout, "#7")
	})

	t.Run("issue label, unlabeled PR", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		writeDoc(t, h.mergedJSONPath, "[]\n")
		h.writeIssueLinks(7)
		h.writePRView("MERGED", merge.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 1)
		assertContains(t, "stdout", result.Stdout, "#7")
		assertContains(t, "stdout", result.Stdout, "missing:1")
		assertContains(t, "argv", h.argv(), "issue list")
		assertContains(t, "argv", h.argv(), "pr view 7")
	})

	t.Run("issue label, ported PR", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		writeDoc(t, h.mergedJSONPath, "[]\n")
		h.writeIssueLinks(7)
		h.writePRView("MERGED", merge.String())
		repo.Checkout("release/0.1")
		portSameDiff(t, repo, merge, "g", "b\n")

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 0)
		assertNotContains(t, "stdout", result.Stdout, "#7")
	})

	t.Run("issue and PR label, one record", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		h.writeMerged("2020-01-01T00:00:00Z", merge.String())
		h.writeIssueLinks(7)
		h.writePRView("MERGED", merge.String())

		result := h.run(repo.Dir(), nil)

		if got := strings.Count(result.Stdout, "#7"); got != 1 {
			t.Errorf("debt lines naming #7 = %d, want 1\nstdout:\n%s", got, result.Stdout)
		}
	})

	t.Run("issue references an unmerged PR", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		repo, merge := seedAuditRepository(t)
		writeDoc(t, h.mergedJSONPath, "[]\n")
		h.writeIssueLinks(7)
		h.writePRView("OPEN", merge.String())

		result := h.run(repo.Dir(), nil)

		assertStatus(t, result, 0)
	})

	t.Run("usage", func(t *testing.T) {
		t.Parallel()
		h := newAuditHarness(t)
		dir := t.TempDir()

		if result := h.run(dir, nil, "--help"); result.Status != 0 {
			t.Errorf("--help exit status = %d, want 0", result.Status)
		}
		if result := h.run(dir, nil, "bogus"); result.Status != 2 {
			t.Errorf("unknown argument exit status = %d, want 2", result.Status)
		}
	})
}
