package scripts

import (
	"maps"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// TestEventBase ports every case of the bash harness ci/test-check-event-base.sh,
// asserting the same exit codes and stdout/stderr substrings for ci/event-base.sh.
func TestEventBase(t *testing.T) {
	t.Parallel()
	// The resolver decides the base SHA from the GitHub event environment and the
	// git repo at the current directory. The fixture is A -> B on main, with a
	// feature branch forked at B adding C, pushed to a local bare origin so the
	// pull_request fetch and merge base resolve offline.
	t.Run("push with a real before", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "push", map[string]string{
			"GITHUB_BEFORE": f.base.String(),
			"GITHUB_SHA":    f.tip.String(),
		})

		assertStatus(t, result, 0)
		if result.Stdout != f.base.String()+"\n" {
			t.Errorf("stdout = %q, want %q", result.Stdout, f.base.String()+"\n")
		}
	})

	t.Run("push whose before is the zero SHA", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "push", map[string]string{
			"GITHUB_BEFORE": "0000000000000000000000000000000000000000",
			"GITHUB_SHA":    f.tip.String(),
		})

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "push without a resolvable 'before' commit")
	})

	t.Run("push whose before is absent", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "push", map[string]string{
			"GITHUB_SHA": f.tip.String(),
		})

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "push without a resolvable 'before' commit")
	})

	t.Run("schedule is undecidable", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "schedule", map[string]string{
			"GITHUB_SHA": f.tip.String(),
		})

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "unsupported event 'schedule'")
	})

	t.Run("workflow_dispatch is undecidable", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "workflow_dispatch", map[string]string{
			"GITHUB_SHA": f.tip.String(),
		})

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "unsupported event 'workflow_dispatch'")
	})

	t.Run("merge_group uses its base_sha", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "merge_group", map[string]string{
			"GITHUB_MERGE_GROUP_BASE_SHA": f.tip.String(),
		})

		assertStatus(t, result, 0)
		if result.Stdout != f.tip.String()+"\n" {
			t.Errorf("stdout = %q, want %q", result.Stdout, f.tip.String()+"\n")
		}
	})

	t.Run("merge_group without a base_sha", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "merge_group", map[string]string{
			"GITHUB_MERGE_GROUP_BASE_SHA": "",
		})

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "merge_group without GITHUB_MERGE_GROUP_BASE_SHA")
	})

	t.Run("pull_request uses the merge base", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "pull_request", map[string]string{
			"GITHUB_BASE_REF": "main",
			"GITHUB_SHA":      f.feature.String(),
		})

		assertStatus(t, result, 0)
		if result.Stdout != f.tip.String()+"\n" {
			t.Errorf("stdout = %q, want %q", result.Stdout, f.tip.String()+"\n")
		}
	})

	// GitHub evaluates a stacked pull request as if it targets the stack base and
	// sets GITHUB_BASE_REF to that trunk. The changelog job passes the branch the
	// pull request directly targets as GH_PR_BASE_REF so its own changeset — not
	// the whole stack's — is diffed.
	t.Run("pull_request prefers the branch it directly targets", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "pull_request", map[string]string{
			"GITHUB_BASE_REF": "main",
			"GH_PR_BASE_REF":  "feature",
			"GITHUB_SHA":      f.child.String(),
		})

		assertStatus(t, result, 0)
		if result.Stdout != f.feature.String()+"\n" {
			t.Errorf("stdout = %q, want %q", result.Stdout, f.feature.String()+"\n")
		}
	})

	t.Run("pull_request falls back to GITHUB_BASE_REF without an own base", func(t *testing.T) {
		t.Parallel()
		f := seedEventBaseFixture(t)

		result := runEventBase(t, f, "pull_request", map[string]string{
			"GITHUB_BASE_REF": "main",
			"GH_PR_BASE_REF":  "",
			"GITHUB_SHA":      f.child.String(),
		})

		assertStatus(t, result, 0)
		if result.Stdout != f.tip.String()+"\n" {
			t.Errorf("stdout = %q, want %q", result.Stdout, f.tip.String()+"\n")
		}
	})
}

// eventBaseFixture is the repository and commit hashes the resolver's cases need:
// base (A) and tip (B) on main, feature (C) forked at B, and child (D) forked at
// C — the top of a stack whose own base is feature while GitHub's GITHUB_BASE_REF
// carries main.
type eventBaseFixture struct {
	repo    *Repo
	base    plumbing.Hash
	tip     plumbing.Hash
	feature plumbing.Hash
	child   plumbing.Hash
}

// seedEventBaseFixture builds A -> B on main with a feature branch forked at B
// adding C and a child branch forked at C adding D, pushed to a local bare
// origin so the pull_request fetch resolves offline.
func seedEventBaseFixture(tb testing.TB) eventBaseFixture {
	tb.Helper()
	repo := New(tb)
	repo.Write("file", "a\n")
	base := repo.Commit("a")
	repo.Write("file", "b\n")
	tip := repo.Commit("b")
	repo.AddBareOrigin()
	repo.SetBranch("feature", tip)
	repo.Checkout("feature")
	repo.Write("file", "c\n")
	feature := repo.Commit("c")
	repo.SetBranch("child", feature)
	repo.Checkout("child")
	repo.Write("file", "d\n")
	child := repo.Commit("d")
	repo.Checkout("main")
	repo.Push(tb)
	return eventBaseFixture{repo: repo, base: base, tip: tip, feature: feature, child: child}
}

// runEventBase runs ci/event-base.sh in the fixture with a scrubbed event
// environment, overlaying the event's variables.
func runEventBase(t *testing.T, f eventBaseFixture, event string, extra map[string]string) ScriptResult {
	t.Helper()
	env := map[string]string{
		"GITHUB_EVENT_NAME":           event,
		"GITHUB_BASE_REF":             "",
		"GH_PR_BASE_REF":              "",
		"GITHUB_BEFORE":               "",
		"GITHUB_MERGE_GROUP_BASE_SHA": "",
		"GITHUB_SHA":                  "",
	}
	maps.Copy(env, extra)
	script := filepath.Join(repositoryRoot(t), "ci", "event-base.sh")
	return RunScript(t, f.repo.Dir(), env, script)
}
