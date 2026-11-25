package scripts

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestEnqueuePr ports every case of the bash harness scripts/test-enqueue-pr.sh,
// asserting the same exit codes, output substrings, recorded gh argv, and call
// ordering. The script itself creates no git fixtures, so the cases differ only
// in the canned gh responses.
func TestEnqueuePr(t *testing.T) {
	t.Parallel()
	t.Run("a failure on a head that moved is watched again", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_15"}`, nil, map[string]string{
			"GH_HEAD_MOVES_AFTER":    "1",
			"GH_HEAD_READS_FILE":     filepath.Join(h.workDir, "head_reads"),
			"GH_CHECKS_FAIL_WATCHES": "1",
			"GH_CHECKS_WATCHES_FILE": filepath.Join(h.workDir, "watches"),
		}, "42")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "its head moved to new-head; watching the new commit")
		assertContains(t, "argv", h.argv(), "sha=new-head")
	})

	t.Run("a failure on an unchanged head stands", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_16","headRefOid":"same-head"}`, nil, map[string]string{
			"GH_CHECKS_FAIL_WATCHES": "1",
			"GH_CHECKS_WATCHES_FILE": filepath.Join(h.workDir, "watches"),
		}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "checks did not pass")
		assertNotContains(t, "stdout", result.Stdout, "head moved")
		assertNotContains(t, "argv", h.argv(), "merge-async")
	})

	t.Run("usage errors", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)
		for name, args := range map[string][]string{
			"no PR":                 {},
			"two PRs without stack": {"41", "42"},
			"stack without PRs":     {"--stack"},
			"not a number":          {"--stack", "41", "x"},
		} {
			if result := h.run(`{}`, nil, nil, args...); result.Status != 2 {
				t.Errorf("%s: exit %d, want 2", name, result.Status)
			}
		}
	})

	t.Run("stack lands bottom-up, retargeting the upper PR", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.runStack(map[string]string{
			"41": `{"state":"OPEN","id":"PR_41","baseRefName":"main","headRefName":"feat-a","headRefOid":"a41"}`,
			"42": `{"state":"OPEN","id":"PR_42","baseRefName":"feat-a","headRefName":"feat-b","headRefOid":"b42"}`,
		}, map[string]string{"GH_STACK_MERGE": "1"}, "41", "42")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "stack landed: 41 42")
		assertContains(t, "argv", h.argv(), "pr edit 42 --base main")
		assertNotContains(t, "argv", h.argv(), "pr edit 41 --base")
		assertEnqueuePrCallOrder(t, h.argv(), "pulls/41/merge-async", "pr edit 42 --base main")
		assertEnqueuePrCallOrder(t, h.argv(), "pr edit 42 --base main", "pulls/42/merge-async")
		assertContains(t, "argv", h.argv(), "sha=b42")
	})

	t.Run("stack stops when the retarget fails", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.runStack(map[string]string{
			"41": `{"state":"OPEN","id":"PR_41","baseRefName":"main","headRefName":"feat-a","headRefOid":"a41"}`,
			"42": `{"state":"OPEN","id":"PR_42","baseRefName":"feat-a","headRefName":"feat-b","headRefOid":"b42"}`,
		}, map[string]string{"GH_STACK_MERGE": "1", "GH_PR_EDIT_EXIT": "1"}, "41", "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "retargeting to main failed")
		assertNotContains(t, "argv", h.argv(), "pulls/42/merge-async")
	})

	t.Run("stack stops on a conflict found while waiting for checks", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.runStack(map[string]string{
			"41": `{"state":"OPEN","id":"PR_41","baseRefName":"main","headRefName":"feat-a","headRefOid":"a41",` +
				`"mergeable":"CONFLICTING","mergeStateStatus":"DIRTY"}`,
		}, map[string]string{"GH_STACK_MERGE": "1"}, "41")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "has merge conflicts")
		assertNotContains(t, "argv", h.argv(), "pr checks 41 --watch")
		assertNotContains(t, "argv", h.argv(), "merge-async")
	})

	t.Run("a conflict that appears while checks register is fatal", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		// load_pr, then watch_checks' first conflict probe see a clean PR; the
		// probe inside wait_for_checks' loop sees the conflict.
		result := h.run(`[`+
			`{"state":"OPEN","id":"PR_14"},`+
			`{"state":"OPEN","id":"PR_14"},`+
			`{"state":"OPEN","id":"PR_14","mergeable":"CONFLICTING","mergeStateStatus":"DIRTY"}]`,
			nil, map[string]string{"GH_CHECKS_NOREPORT": "5"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "has merge conflicts")
		assertNotContains(t, "argv", h.argv(), "pr checks 42 --watch")
		assertNotContains(t, "argv", h.argv(), "merge-async")
	})

	t.Run("stack refuses a base outside the stack", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.runStack(map[string]string{
			"42": `{"state":"OPEN","id":"PR_42","baseRefName":"feat-a","headRefName":"feat-b","headRefOid":"b42"}`,
		}, map[string]string{"GH_STACK_MERGE": "1"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "is based on feat-a, which is not the previous PR in the stack")
		assertNotContains(t, "argv", h.argv(), "merge-async")
		assertNotContains(t, "argv", h.argv(), "pr edit 42 --base")
	})

	t.Run("stack stops when the queue drops a PR", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.runStack(map[string]string{
			"41": `{"state":"OPEN","id":"PR_41","baseRefName":"main","headRefName":"feat-a","headRefOid":"a41","isInMergeQueue":false}`,
			"42": `{"state":"OPEN","id":"PR_42","baseRefName":"feat-a","headRefName":"feat-b","headRefOid":"b42"}`,
		}, nil, "41", "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "PR #41 was dropped from the merge queue")
		assertNotContains(t, "argv", h.argv(), "pulls/42/merge-async")
	})

	t.Run("already merged", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"MERGED","id":"PR_1"}`, nil, nil, "42")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "already merged")
		assertNotContains(t, "argv", h.argv(), "merge-async")
	})

	t.Run("checks pass", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_2","headRefOid":"abc123"}`, nil, nil, "42")

		assertStatus(t, result, 0)
		assertContains(t, "argv", h.argv(), "pr view 42 --json state -q .state")
		assertContains(t, "argv", h.argv(), "pr checks 42 --watch --fail-fast")
		assertContains(t, "argv", h.argv(), "api -X PUT repos/{owner}/{repo}/pulls/42/merge-async")
		assertContains(t, "argv", h.argv(), "merge_action=merge_queue")
		assertContains(t, "argv", h.argv(), "sha=abc123")
		assertContains(t, "stdout", result.Stdout, "enqueued (pending)")
		assertNotContains(t, "stdout", result.Stdout, "fake-tests")
	})

	t.Run("linked issue needs a backport", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(
			`{"state":"OPEN","id":"PR_10","closingIssuesReferences":[{"number":7}]}`,
			map[string][]string{"7": {"needs-backport"}},
			nil,
			"42",
		)

		assertStatus(t, result, 0)
		assertContains(
			t,
			"argv",
			h.argv(),
			"pr view 42 --json closingIssuesReferences -q .closingIssuesReferences[].number",
		)
		assertContains(t, "argv", h.argv(), "issue view 7 --json labels -q .labels[].name")
		assertContains(t, "argv", h.argv(), "pr edit 42 --add-label needs-backport")
		assertContains(
			t,
			"stdout",
			result.Stdout,
			"marked needs-backport (a backport PR will be opened by the backport audit)",
		)
		assertContains(t, "argv", h.argv(), "merge-async")
		assertEnqueuePrCallOrder(t, h.argv(), "merge-async", "pr edit 42 --add-label needs-backport")
	})

	t.Run("marker copy is refused", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(
			`{"state":"OPEN","id":"PR_13","closingIssuesReferences":[{"number":7}]}`,
			map[string][]string{"7": {"needs-backport"}},
			map[string]string{"GH_PR_EDIT_EXIT": "1"},
			"42",
		)

		assertStatus(t, result, 0)
		assertContains(t, "stderr", result.Stderr, "could not copy the marker; the issue label still governs the audit")
		assertContains(t, "argv", h.argv(), "merge-async")
		assertEnqueuePrCallOrder(t, h.argv(), "merge-async", "pr edit 42 --add-label needs-backport")
		assertNotContains(t, "stdout", result.Stdout, "marked needs-backport")
	})

	t.Run("linked issue does not need a backport", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(
			`{"state":"OPEN","id":"PR_11","closingIssuesReferences":[{"number":7}]}`,
			map[string][]string{"7": {}},
			nil,
			"42",
		)

		assertStatus(t, result, 0)
		assertNotContains(t, "argv", h.argv(), "pr edit")
		assertNotContains(t, "stdout", result.Stdout, "needs-backport")
	})

	t.Run("no closing issues", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_12","closingIssuesReferences":[]}`, nil, nil, "42")

		assertStatus(t, result, 0)
		assertNotContains(t, "argv", h.argv(), "pr edit")
		assertNotContains(t, "argv", h.argv(), "issue view")
	})

	t.Run("checks not yet registered", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_8"}`, nil, map[string]string{"GH_CHECKS_NOREPORT": "2"}, "42")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "enqueued (pending)")
	})

	t.Run("checks fail", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_3"}`, nil, map[string]string{"GH_CHECKS_EXIT": "1"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "checks did not pass")
		assertNotContains(t, "argv", h.argv(), "merge-async")
	})

	t.Run("checks pending", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_4"}`, nil, map[string]string{"GH_CHECKS_EXIT": "8"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "checks are still pending")
		assertNotContains(t, "argv", h.argv(), "merge-async")
	})

	t.Run("closed while waiting", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)
		// The watch reaches pr_state on the sixth view probe: the state, two
		// conflict probes, and the head read before and after the failed watch
		// (same head, so the failure stands) come first.
		views := `[` +
			`{"state":"OPEN","id":"PR_5"},` +
			`{"state":"OPEN","id":"PR_5"},` +
			`{"state":"OPEN","id":"PR_5"},` +
			`{"state":"OPEN","id":"PR_5","headRefOid":"h5"},` +
			`{"state":"OPEN","id":"PR_5","headRefOid":"h5"},` +
			`{"state":"CLOSED","id":"PR_5"}]`

		result := h.run(views, nil, map[string]string{"GH_CHECKS_EXIT": "1"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "closed while waiting")
		assertNotContains(t, "argv", h.argv(), "merge-async")
	})

	t.Run("closed", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"CLOSED","id":"PR_6"}`, nil, nil, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "PR #42 is closed")
		assertNotContains(t, "argv", h.argv(), "pr checks")
	})

	t.Run("merge conflict", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(
			`{"state":"OPEN","id":"PR_9","mergeable":"CONFLICTING","mergeStateStatus":"DIRTY"}`,
			nil,
			nil,
			"42",
		)

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "merge conflicts")
		assertNotContains(t, "argv", h.argv(), "merge-async")
		assertNotContains(t, "argv", h.argv(), "pr checks")
	})

	t.Run("enqueue fails", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_7"}`, nil, map[string]string{"GH_ENQUEUE_EXIT": "1"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "enqueue request failed")
		assertContains(t, "argv", h.argv(), "merge-async")
	})

	t.Run("usage", func(t *testing.T) {
		t.Parallel()
		h := newEnqueuePrHarness(t)

		if result := h.run(`{}`, nil, nil, "--help"); result.Status != 0 {
			t.Errorf("--help exit status = %d, want 0", result.Status)
		}
		if result := h.run(`{}`, nil, nil); result.Status != 2 {
			t.Errorf("no argument exit status = %d, want 2", result.Status)
		}
		if result := h.run(`{}`, nil, nil, "not-a-number"); result.Status != 2 {
			t.Errorf("non-numeric argument exit status = %d, want 2", result.Status)
		}
	})
}

// enqueuePrHarness drives scripts/enqueue-pr.sh with the specialized fake gh on
// PATH: a per-case view payload, an issue-label map, and knobs for the checks and
// mutation exits.
type enqueuePrHarness struct {
	t               *testing.T
	binDir          string
	workDir         string
	argvPath        string
	prViewPath      string
	prViewIndexPath string
	issueLabelsPath string
	noReportPath    string
}

func newEnqueuePrHarness(t *testing.T) *enqueuePrHarness {
	t.Helper()
	root := t.TempDir()
	return &enqueuePrHarness{
		t:               t,
		binDir:          BinDir(t, "gh"),
		workDir:         root,
		argvPath:        filepath.Join(root, "argv.txt"),
		prViewPath:      filepath.Join(root, "pr_view.json"),
		prViewIndexPath: filepath.Join(root, "pr_view_index"),
		issueLabelsPath: filepath.Join(root, "issue_labels.json"),
		noReportPath:    filepath.Join(root, "noreport"),
	}
}

// run seeds the view payload and issue labels, clears the per-case recorders, and
// executes the script with args. extra overlays the harness's base environment.
// labels maps an issue number to its label names; nil means none.
func (h *enqueuePrHarness) run(
	view string,
	labels map[string][]string,
	extra map[string]string,
	args ...string,
) ScriptResult {
	h.t.Helper()
	writeDoc(h.t, h.prViewPath, view)
	if labels == nil {
		labels = map[string][]string{}
	}
	writeJSON(h.t, h.issueLabelsPath, labels)
	_ = os.Remove(h.argvPath)
	_ = os.Remove(h.prViewIndexPath)
	_ = os.Remove(h.noReportPath)
	env := map[string]string{
		"ENQUEUE_PR_FAKE_GH":        "1",
		"ENQUEUE_PR_RETRY_SECONDS":  "0",
		"ENQUEUE_PR_SETTLE_SECONDS": "0",
		"GH_ARGV":                   h.argvPath,
		"GH_PR_VIEW":                h.prViewPath,
		"GH_PR_VIEW_INDEX":          h.prViewIndexPath,
		"GH_ISSUE_LABELS":           h.issueLabelsPath,
		"GH_CHECKS_NOREPORT_FILE":   h.noReportPath,
		"PATH":                      h.binDir + string(os.PathListSeparator) + envOption("PATH"),
	}
	maps.Copy(env, extra)
	return RunScript(h.t, h.workDir, env, repositoryScript(h.t, "enqueue-pr.sh"), args...)
}

// runStack seeds one pr view payload per PR (number to JSON) and executes the
// script in stack mode with the PR numbers in order. extra overlays the base
// environment (GH_STACK_MERGE makes an enqueued PR merge).
func (h *enqueuePrHarness) runStack(docs map[string]string, extra map[string]string, prs ...string) ScriptResult {
	h.t.Helper()
	viewDir := filepath.Join(h.workDir, "views")
	enqueuedDir := filepath.Join(h.workDir, "enqueued")
	for _, dir := range []string{viewDir, enqueuedDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			h.t.Fatal(err)
		}
	}
	for number, doc := range docs {
		writeDoc(h.t, filepath.Join(viewDir, number+".json"), doc)
	}
	stackEnv := map[string]string{
		"GH_PR_VIEW_DIR":          viewDir,
		"GH_ENQUEUED_DIR":         enqueuedDir,
		"ENQUEUE_PR_POLL_SECONDS": "0",
	}
	maps.Copy(stackEnv, extra)
	return h.run(`{}`, nil, stackEnv, append([]string{"--stack"}, prs...)...)
}

// argv returns the recorded gh argv log, one call per line.
func (h *enqueuePrHarness) argv() string {
	return readText(h.t, h.argvPath)
}

// assertEnqueuePrCallOrder fails unless the recorded argv shows before on an
// earlier line than after.
func assertEnqueuePrCallOrder(t *testing.T, argv, before, after string) {
	t.Helper()
	beforeLine := enqueuePrLineOf(argv, before)
	afterLine := enqueuePrLineOf(argv, after)
	if beforeLine == 0 || afterLine == 0 || beforeLine >= afterLine {
		t.Errorf("call order: %q at line %d, %q at line %d", before, beforeLine, after, afterLine)
	}
}

// enqueuePrLineOf returns the 1-based line of the first occurrence of needle, or 0.
func enqueuePrLineOf(argv, needle string) int {
	for i, line := range strings.Split(argv, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}

// enqueuePrFakeGh serves the `gh` calls scripts/enqueue-pr.sh makes, when the
// harness sets ENQUEUE_PR_FAKE_GH. It is a no-op (handled=false) otherwise, so
// the other migrated harnesses keep the shared fake's behaviour. It runs in the
// re-executed test process, so it reads its knobs from the environment.
func enqueuePrFakeGh(args []string) (handled bool, stdout, stderr string, status int) {
	if envOption("ENQUEUE_PR_FAKE_GH") == "" {
		return false, "", "", 0
	}
	if len(args) < 2 {
		return true, "", "fake gh: unexpected call: " + strings.Join(args, " ") + "\n", 1
	}
	switch {
	case args[0] == "pr" && args[1] == "view":
		stdout, stderr, status = enqueuePrView(args)
	case args[0] == "pr" && args[1] == "checks":
		stdout, stderr, status = enqueuePrChecks(args)
	case args[0] == "pr" && args[1] == "edit":
		status = enqueuePrEnvInt("GH_PR_EDIT_EXIT")
	case args[0] == "issue" && args[1] == "view":
		stdout, stderr, status = enqueuePrIssueView(args)
	case args[0] == "api" && strings.Contains(strings.Join(args, " "), "/merge-async"):
		stdout, stderr, status = enqueuePrMergeAsync(args)
	case args[0] == "api" && args[1] == "graphql":
		stdout, stderr, status = enqueuePrQueueState(args)
	case args[0] == "repo" && args[1] == "view":
		stdout, stderr, status = serveDocument(args, `{"defaultBranchRef":{"name":"main"}}`)
	default:
		stderr = "fake gh: unexpected call: " + strings.Join(args, " ") + "\n"
		status = 1
	}
	return true, stdout, stderr, status
}

// enqueuePrView serves `gh pr view` from GH_PR_VIEW. The payload may be a single
// JSON document or an array indexed one document per call (via the
// GH_PR_VIEW_INDEX counter file), so a case can change the PR's state between
// probes. The --jq/-q query is applied through the real jq.
func enqueuePrView(args []string) (string, string, int) {
	if moves := enqueuePrEnvInt("GH_HEAD_MOVES_AFTER"); moves > 0 && requestsJSONField(args, "headRefOid") {
		return enqueuePrMovingHead(args, moves)
	}
	if requestsJSONField(args, "isInMergeQueue") {
		return "", "Unknown JSON field: \"isInMergeQueue\"\n", 1
	}
	if dir := envOption("GH_PR_VIEW_DIR"); dir != "" {
		return enqueuePrStackView(args, dir)
	}
	data, err := os.ReadFile(envOption("GH_PR_VIEW"))
	if err != nil {
		return "", "fake gh: read pr view payload: " + err.Error() + "\n", 1
	}
	doc := strings.TrimSpace(string(data))
	if strings.HasPrefix(doc, "[") {
		var entries []json.RawMessage
		if err := json.Unmarshal([]byte(doc), &entries); err != nil {
			return "", "fake gh: parse pr view payload: " + err.Error() + "\n", 1
		}
		indexPath := envOption("GH_PR_VIEW_INDEX")
		index := enqueuePrReadCount(indexPath)
		_ = enqueuePrWriteCount(indexPath, index+1)
		if index < len(entries) {
			doc = string(entries[index])
		} else {
			doc = "null"
		}
	}
	return serveDocument(args, doc)
}

// enqueuePrQueueState serves the merge-queue poll (a GraphQL pullRequest query
// for state and isInMergeQueue) from the stack payload of the PR in its n
// variable, wrapped the way the API nests it.
func enqueuePrQueueState(args []string) (string, string, int) {
	number := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-F" && strings.HasPrefix(args[i+1], "n=") {
			number = strings.TrimPrefix(args[i+1], "n=")
		}
	}
	view, stderr, status := enqueuePrStackView([]string{"pr", "view", number}, envOption("GH_PR_VIEW_DIR"))
	if status != 0 {
		return "", stderr, status
	}
	return serveDocument(args, `{"data":{"repository":{"pullRequest":`+strings.TrimSpace(view)+`}}}`)
}

// enqueuePrStackView serves `gh pr view <n>` for a stack case from
// GH_PR_VIEW_DIR/<n>.json. Once the PR was enqueued and GH_STACK_MERGE is set,
// it reports the PR merged, as the queue would; without GH_STACK_MERGE an
// enqueued PR stays open and outside the queue, as if the queue dropped it.
func enqueuePrStackView(args []string, dir string) (string, string, int) {
	number := ""
	for _, arg := range args[2:] {
		if _, err := strconv.Atoi(arg); err == nil {
			number = arg
			break
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, number+".json"))
	if err != nil {
		return "", "fake gh: no stack payload for PR " + number + ": " + err.Error() + "\n", 1
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", "fake gh: parse stack payload: " + err.Error() + "\n", 1
	}
	if _, err := os.Stat(filepath.Join(envOption("GH_ENQUEUED_DIR"), number)); err == nil &&
		envOption("GH_STACK_MERGE") != "" {
		doc["state"] = "MERGED"
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", "fake gh: marshal stack payload: " + err.Error() + "\n", 1
	}
	return serveDocument(args, string(encoded))
}

// enqueuePrMovingHead serves a head-commit read for a PR whose head moves: the
// first moves reads see the old commit, later ones the new (counted in
// GH_HEAD_READS_FILE), as GitHub reports right after a push.
func enqueuePrMovingHead(args []string, moves int) (string, string, int) {
	path := envOption("GH_HEAD_READS_FILE")
	reads := enqueuePrReadCount(path)
	_ = enqueuePrWriteCount(path, reads+1)
	head := "new-head"
	if reads < moves {
		head = "old-head"
	}
	return serveDocument(args, `{"headRefOid":"`+head+`"}`)
}

// enqueuePrChecks serves `gh pr checks`: while GH_CHECKS_NOREPORT is greater than
// the calls already served (counted in GH_CHECKS_NOREPORT_FILE), it reports "no
// checks reported" and exits 1; otherwise it prints a passing check line and
// exits GH_CHECKS_EXIT.
func enqueuePrChecks(args []string) (string, string, int) {
	if threshold := enqueuePrEnvInt("GH_CHECKS_NOREPORT"); threshold > 0 {
		path := envOption("GH_CHECKS_NOREPORT_FILE")
		count := enqueuePrReadCount(path)
		if count < threshold {
			_ = enqueuePrWriteCount(path, count+1)
			return "", "no checks reported on the 'x' branch\n", 1
		}
	}
	if enqueuePrEnvInt("GH_CHECKS_FAIL_WATCHES") > 0 && slices.Contains(args, "--watch") {
		path := envOption("GH_CHECKS_WATCHES_FILE")
		watches := enqueuePrReadCount(path)
		_ = enqueuePrWriteCount(path, watches+1)
		if watches < enqueuePrEnvInt("GH_CHECKS_FAIL_WATCHES") {
			return "fake-tests\tfail\tfail\t1m\thttps://example.test/checks\n", "", 1
		}
		return "fake-tests\tpass\tpass\t1m\thttps://example.test/checks\n", "", 0
	}
	return "fake-tests\tpass\tpass\t1m\thttps://example.test/checks\n", "", enqueuePrEnvInt("GH_CHECKS_EXIT")
}

// enqueuePrIssueView serves `gh issue view <n> --json labels` from GH_ISSUE_LABELS,
// a JSON map of issue number to label names.
func enqueuePrIssueView(args []string) (string, string, int) {
	number := ""
	for _, arg := range args {
		if _, err := strconv.Atoi(arg); err == nil {
			number = arg
			break
		}
	}
	data, err := os.ReadFile(envOption("GH_ISSUE_LABELS"))
	if err != nil {
		return "", "fake gh: read issue labels: " + err.Error() + "\n", 1
	}
	var labelsByNumber map[string][]string
	if err := json.Unmarshal(data, &labelsByNumber); err != nil {
		return "", "fake gh: parse issue labels: " + err.Error() + "\n", 1
	}
	type issueLabel struct {
		Name string `json:"name"`
	}
	labels := []issueLabel{}
	for _, name := range labelsByNumber[number] {
		labels = append(labels, issueLabel{Name: name})
	}
	doc, err := json.Marshal(map[string]any{"labels": labels})
	if err != nil {
		return "", "fake gh: marshal issue labels: " + err.Error() + "\n", 1
	}
	return serveDocument(args, string(doc))
}

// enqueuePrMergeAsync serves the REST async merge: a failure prints gh's
// diagnostic when GH_ENQUEUE_EXIT is set; otherwise it records the PR as
// enqueued (a marker file under GH_ENQUEUED_DIR, which a stack case's pr view
// reads) and answers the queue status.
func enqueuePrMergeAsync(args []string) (string, string, int) {
	if enqueuePrEnvInt("GH_ENQUEUE_EXIT") != 0 {
		return "", "gh: Pull request is not mergeable (HTTP 422)\n", 1
	}
	if dir := envOption("GH_ENQUEUED_DIR"); dir != "" {
		if number := enqueuePrNumberIn(strings.Join(args, " ")); number != "" {
			_ = os.WriteFile(filepath.Join(dir, number), nil, 0o600)
		}
	}
	return serveDocument(args, `{"status":"pending"}`)
}

// enqueuePrNumberIn extracts the PR number from a pulls/<n>/merge-async path.
func enqueuePrNumberIn(call string) string {
	_, rest, ok := strings.Cut(call, "/pulls/")
	if !ok {
		return ""
	}
	number, _, _ := strings.Cut(rest, "/")
	return number
}

// enqueuePrEnvInt reads an integer environment knob, treating an unset or
// unparsable value as 0.
func enqueuePrEnvInt(name string) int {
	value := envOption(name)
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

// enqueuePrReadCount returns the integer stored at path, or 0 when it is absent.
func enqueuePrReadCount(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

// enqueuePrWriteCount stores value at path.
func enqueuePrWriteCount(path string, value int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(value)), 0o600)
}
