package scripts

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestOpenBackportPR ports every case of the bash harness scripts/test-open-backport-pr.sh,
// asserting the same exit codes, output substrings, gh argv, and origin refs.
func TestOpenBackportPR(t *testing.T) {
	t.Run("clean port", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedFixture(t)

		result := h.run(f.repo.Dir(), f.merge.String(), nil, "7")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, h.prURL)
		assertContains(t, "argv", h.argv(), "--base release/0.1")
		assertContains(t, "argv", h.argv(), "--head backport/7-0.1")
		assertContains(t, "argv", h.argv(), "Backport #7 to 0.1")
		assertContains(t, "argv", h.argv(), "Backport of #7.")
		assertContains(t, "argv", h.argv(), "fix")
		if !f.repo.RefExists("refs/heads/backport/7-0.1") {
			t.Error("backport branch not created")
		}
		if content, ok := f.repo.FileAt("backport/7-0.1", "f"); !ok || content != "b\n" {
			t.Errorf("backport f = %q,%v, want %q", content, ok, "b\n")
		}
		refs := OriginRefs(t, f.origin)
		if !slices.Contains(refs, "refs/heads/backport/7-0.1") {
			t.Errorf("origin %v missing backport branch", refs)
		}
		if !slices.Contains(refs, "refs/heads/release/0.1") {
			t.Errorf("origin %v missing release branch", refs)
		}
	})

	t.Run("fragment named for the backport PR", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedFragmentFixture(t)

		result := h.run(f.repo.Dir(), f.merge.String(), nil, "7")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, h.prURL)
		fragment, ok := OriginFile(t, f.origin, "backport/7-0.1", "changelog.d/99.fixed.md")
		if !ok {
			t.Fatal("renamed fragment missing")
		}
		assertContains(t, "fragment", fragment, "The fix.")
		assertContains(t, "fragment", fragment, "Backport of #7 to the 0.1 line.")
		if _, ok := OriginFile(t, f.origin, "backport/7-0.1", "changelog.d/7.fixed.md"); ok {
			t.Error("source-numbered fragment not dropped")
		}
		if got := OriginCount(t, f.origin, "release/0.1", "backport/7-0.1"); got != 2 {
			t.Errorf("backport commit count = %d, want 2", got)
		}
	})

	t.Run("references the tracker", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedFragmentFixture(t)

		result := h.run(f.repo.Dir(), f.merge.String(), map[string]string{"BACKPORT_TRACKER_ISSUE": "42"}, "7")

		assertStatus(t, result, 0)
		assertContains(t, "argv", h.argv(), "Part of #42")
	})

	t.Run("no tracker reference without the knob", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedFragmentFixture(t)

		h.run(f.repo.Dir(), f.merge.String(), nil, "7")

		assertNotContains(t, "argv", h.argv(), "Part of #")
	})

	t.Run("rename push failure", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedFragmentFixture(t)
		brokenOrigin := filepath.Join(t.TempDir(), "missing.git")

		result := h.run(f.repo.Dir(), f.merge.String(), map[string]string{
			"GH_BREAK_ORIGIN_AFTER_CREATE": "1",
			"GH_BROKEN_ORIGIN":             brokenOrigin,
		}, "7")

		assertStatus(t, result, 4)
		assertContains(t, "stderr", result.Stderr, "could not push the renamed fragment")
		assertContains(t, "stdout", result.Stdout, h.prURL)
	})

	t.Run("conflict aborts without pushing", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedFixture(t)
		f.repo.Checkout("release/0.1")
		f.repo.Write("f", "c\n")
		f.repo.Commit("release-side change")
		f.repo.Push(t)
		f.repo.Checkout("main")
		before := OriginRefs(t, f.origin)

		result := h.run(f.repo.Dir(), f.merge.String(), nil, "7")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "conflict")
		if after := OriginRefs(t, f.origin); !slices.Equal(before, after) {
			t.Errorf("origin changed: %v -> %v", before, after)
		}
		assertNotContains(t, "argv", h.argv(), "pr create")
	})

	t.Run("partially ported", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedTwoCommitFixture(t)
		f.repo.Checkout("release/0.1")
		portSameDiff(t, f.repo, f.first, "f", "b\n")
		f.repo.Checkout("main")
		f.repo.Push(t)

		result := h.run(f.repo.Dir(), f.merge.String(), nil, "7")

		assertStatus(t, result, 0)
		if !slices.Contains(OriginRefs(t, f.origin), "refs/heads/backport/7-0.1") {
			t.Error("backport branch not pushed")
		}
		if got := OriginCount(t, f.origin, "release/0.1", "backport/7-0.1"); got != 1 {
			t.Errorf("ported commit count = %d, want 1", got)
		}
		messages := strings.Join(OriginMessages(t, f.origin, "release/0.1", "backport/7-0.1"), "\n")
		assertContains(t, "messages", messages, "second")
		assertContains(t, "messages", messages, "(cherry picked from commit "+f.second.String()+")")
		assertNotContains(t, "messages", messages, "(cherry picked from commit "+f.first.String()+")")
		assertContains(t, "argv", h.argv(), "pr create")
	})

	t.Run("empty commit", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedEmptyCommitFixture(t)

		result := h.run(f.repo.Dir(), f.merge.String(), nil, "7")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "nothing to port")
		assertContains(t, "argv", h.argv(), "pr create")
		if got := OriginCount(t, f.origin, "release/0.1", "backport/7-0.1"); got != 2 {
			t.Errorf("ported commit count = %d, want 2", got)
		}
		messages := strings.Join(OriginMessages(t, f.origin, "release/0.1", "backport/7-0.1"), "\n")
		assertContains(t, "messages", messages, "(cherry picked from commit "+f.first.String()+")")
		assertContains(t, "messages", messages, "(cherry picked from commit "+f.second.String()+")")
		assertNotContains(t, "messages", messages, "empty")
	})

	t.Run("change already present in another form", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedFixture(t)
		f.repo.Checkout("release/0.1")
		f.repo.Write("f", "b\n")
		f.repo.Write("h", "h\n")
		f.repo.Commit("release-side equivalent")
		f.repo.Checkout("main")
		f.repo.Push(t)
		before := OriginRefs(t, f.origin)

		result := h.run(f.repo.Dir(), f.merge.String(), nil, "7")

		assertStatus(t, result, 3)
		assertContains(t, "stdout", result.Stdout, "already fully ported onto release/0.1")
		if after := OriginRefs(t, f.origin); !slices.Equal(before, after) {
			t.Errorf("origin changed: %v -> %v", before, after)
		}
		assertNotContains(t, "argv", h.argv(), "pr create")
		if f.repo.RefExists("refs/heads/backport/7-0.1") {
			t.Error("otherwise-empty backport branch not deleted")
		}
	})

	t.Run("fully ported", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedTwoCommitFixture(t)
		f.repo.Checkout("release/0.1")
		portSameDiff(t, f.repo, f.first, "f", "b\n")
		portSameDiff(t, f.repo, f.second, "g", "c\n")
		f.repo.Checkout("main")
		f.repo.Push(t)
		before := OriginRefs(t, f.origin)

		result := h.run(f.repo.Dir(), f.merge.String(), nil, "7")

		assertStatus(t, result, 3)
		assertContains(t, "stdout", result.Stdout, "already fully ported onto release/0.1")
		if after := OriginRefs(t, f.origin); !slices.Equal(before, after) {
			t.Errorf("origin changed: %v -> %v", before, after)
		}
		assertNotContains(t, "argv", h.argv(), "pr create")
		if f.repo.RefExists("refs/heads/backport/7-0.1") {
			t.Error("fully-ported PR created a backport branch")
		}
	})

	t.Run("no release branch", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedMainOnly(t)

		result := h.run(f.repo.Dir(), f.merge.String(), nil, "7")

		assertStatus(t, result, 1)
		assertNotContains(t, "argv", h.argv(), "pr create")
	})

	t.Run("unknown PR", func(t *testing.T) {
		h := newOpenHarness(t)
		f := seedFixture(t)

		result := h.run(f.repo.Dir(), "", nil, "999")

		assertStatus(t, result, 1)
		assertNotContains(t, "argv", h.argv(), "pr create")
	})

	t.Run("usage", func(t *testing.T) {
		h := newOpenHarness(t)
		dir := t.TempDir()

		if result := h.run(dir, "", nil, "--help"); result.Status != 0 {
			t.Errorf("--help exit status = %d, want 0", result.Status)
		}
		if result := h.run(dir, "", nil); result.Status != 2 {
			t.Errorf("no argument exit status = %d, want 2", result.Status)
		}
		if result := h.run(dir, "", nil, "abc"); result.Status != 2 {
			t.Errorf("non-numeric argument exit status = %d, want 2", result.Status)
		}
	})
}
