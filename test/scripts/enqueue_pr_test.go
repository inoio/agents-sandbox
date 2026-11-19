package scripts

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEnqueuePr ports every case of the bash harness scripts/test-enqueue-pr.sh,
// asserting the same exit codes, output substrings, recorded gh argv, and call
// ordering. The script itself creates no git fixtures, so the cases differ only
// in the canned gh responses.
func TestEnqueuePr(t *testing.T) {
	t.Run("already merged", func(t *testing.T) {
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"MERGED","id":"PR_1"}`, nil, nil, "42")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "already merged")
		assertNotContains(t, "argv", h.argv(), "api graphql")
	})

	t.Run("checks pass", func(t *testing.T) {
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_2"}`, nil, nil, "42")

		assertStatus(t, result, 0)
		assertContains(t, "argv", h.argv(), "pr view 42 --json state,id -q [.state, .id] | @tsv")
		assertContains(t, "argv", h.argv(), "pr checks 42 --watch --fail-fast")
		assertContains(t, "argv", h.argv(), "enqueuePullRequest(input:{pullRequestId:$id})")
		assertContains(t, "argv", h.argv(), "id=PR_2")
		assertContains(t, "stdout", result.Stdout, "enqueued at position 3")
		assertNotContains(t, "stdout", result.Stdout, "fake-tests")
	})

	t.Run("linked issue needs a backport", func(t *testing.T) {
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
		assertContains(t, "argv", h.argv(), "api graphql")
		assertEnqueuePrCallOrder(t, h.argv(), "api graphql", "pr edit 42 --add-label needs-backport")
	})

	t.Run("marker copy is refused", func(t *testing.T) {
		h := newEnqueuePrHarness(t)

		result := h.run(
			`{"state":"OPEN","id":"PR_13","closingIssuesReferences":[{"number":7}]}`,
			map[string][]string{"7": {"needs-backport"}},
			map[string]string{"GH_PR_EDIT_EXIT": "1"},
			"42",
		)

		assertStatus(t, result, 0)
		assertContains(t, "stderr", result.Stderr, "could not copy the marker; the issue label still governs the audit")
		assertContains(t, "argv", h.argv(), "api graphql")
		assertEnqueuePrCallOrder(t, h.argv(), "api graphql", "pr edit 42 --add-label needs-backport")
		assertNotContains(t, "stdout", result.Stdout, "marked needs-backport")
	})

	t.Run("linked issue does not need a backport", func(t *testing.T) {
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
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_12","closingIssuesReferences":[]}`, nil, nil, "42")

		assertStatus(t, result, 0)
		assertNotContains(t, "argv", h.argv(), "pr edit")
		assertNotContains(t, "argv", h.argv(), "issue view")
	})

	t.Run("checks not yet registered", func(t *testing.T) {
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_8"}`, nil, map[string]string{"GH_CHECKS_NOREPORT": "2"}, "42")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "enqueued at position 3")
	})

	t.Run("checks fail", func(t *testing.T) {
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_3"}`, nil, map[string]string{"GH_CHECKS_EXIT": "1"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "checks did not pass")
		assertNotContains(t, "argv", h.argv(), "api graphql")
	})

	t.Run("checks pending", func(t *testing.T) {
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_4"}`, nil, map[string]string{"GH_CHECKS_EXIT": "8"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "checks are still pending")
		assertNotContains(t, "argv", h.argv(), "api graphql")
	})

	t.Run("closed while waiting", func(t *testing.T) {
		h := newEnqueuePrHarness(t)
		// The watch reaches pr_state on the fourth view probe; the marker copy no
		// longer consumes one, because it runs after the enqueue.
		views := `[` +
			`{"state":"OPEN","id":"PR_5"},` +
			`{"state":"OPEN","id":"PR_5"},` +
			`{"state":"OPEN","id":"PR_5"},` +
			`{"state":"CLOSED","id":"PR_5"}]`

		result := h.run(views, nil, map[string]string{"GH_CHECKS_EXIT": "1"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "closed while waiting")
		assertNotContains(t, "argv", h.argv(), "api graphql")
	})

	t.Run("closed", func(t *testing.T) {
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"CLOSED","id":"PR_6"}`, nil, nil, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "PR #42 is closed")
		assertNotContains(t, "argv", h.argv(), "pr checks")
	})

	t.Run("merge conflict", func(t *testing.T) {
		h := newEnqueuePrHarness(t)

		result := h.run(
			`{"state":"OPEN","id":"PR_9","mergeable":"CONFLICTING","mergeStateStatus":"DIRTY"}`,
			nil,
			nil,
			"42",
		)

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "merge conflicts")
		assertNotContains(t, "argv", h.argv(), "api graphql")
		assertNotContains(t, "argv", h.argv(), "pr checks")
	})

	t.Run("enqueue fails", func(t *testing.T) {
		h := newEnqueuePrHarness(t)

		result := h.run(`{"state":"OPEN","id":"PR_7"}`, nil, map[string]string{"GH_ENQUEUE_EXIT": "1"}, "42")

		assertStatus(t, result, 1)
		assertContains(t, "stderr", result.Stderr, "enqueue request failed")
		assertContains(t, "argv", h.argv(), "api graphql")
	})

	t.Run("usage", func(t *testing.T) {
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
		"ENQUEUE_PR_FAKE_GH":       "1",
		"ENQUEUE_PR_RETRY_SECONDS": "0",
		"GH_ARGV":                  h.argvPath,
		"GH_PR_VIEW":               h.prViewPath,
		"GH_PR_VIEW_INDEX":         h.prViewIndexPath,
		"GH_ISSUE_LABELS":          h.issueLabelsPath,
		"GH_CHECKS_NOREPORT_FILE":  h.noReportPath,
		"PATH":                     h.binDir + string(os.PathListSeparator) + envOption("PATH"),
	}
	maps.Copy(env, extra)
	return RunScript(h.t, h.workDir, env, repositoryScript(h.t, "enqueue-pr.sh"), args...)
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
		stdout, stderr, status = enqueuePrChecks()
	case args[0] == "pr" && args[1] == "edit":
		status = enqueuePrEnvInt("GH_PR_EDIT_EXIT")
	case args[0] == "issue" && args[1] == "view":
		stdout, stderr, status = enqueuePrIssueView(args)
	case args[0] == "api" && args[1] == "graphql":
		stdout, stderr, status = enqueuePrGraphql()
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

// enqueuePrChecks serves `gh pr checks`: while GH_CHECKS_NOREPORT is greater than
// the calls already served (counted in GH_CHECKS_NOREPORT_FILE), it reports "no
// checks reported" and exits 1; otherwise it prints a passing check line and
// exits GH_CHECKS_EXIT.
func enqueuePrChecks() (string, string, int) {
	if threshold := enqueuePrEnvInt("GH_CHECKS_NOREPORT"); threshold > 0 {
		path := envOption("GH_CHECKS_NOREPORT_FILE")
		count := enqueuePrReadCount(path)
		if count < threshold {
			_ = enqueuePrWriteCount(path, count+1)
			return "", "no checks reported on the 'x' branch\n", 1
		}
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

// enqueuePrGraphql serves the enqueue mutation: a failure prints gh's GraphQL
// diagnostic when GH_ENQUEUE_EXIT is set, otherwise it returns a queue position.
func enqueuePrGraphql() (string, string, int) {
	if enqueuePrEnvInt("GH_ENQUEUE_EXIT") != 0 {
		return "", "GraphQL: Pull request is not mergeable\n", 1
	}
	return `{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"position":3}}}}` + "\n", "", 0
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
