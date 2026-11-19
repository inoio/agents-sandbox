package scripts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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
	// The audit derives a PR's commits from the local clone, never the API: fail
	// any request whose --json field list names `commits`, so a regression to an
	// API commit fetch is caught in every case instead of passing silently.
	if requestsJSONField(args, "commits") {
		return "", "fake gh: the audit must not request commits: " + strings.Join(args, " ") + "\n", 1
	}
	doc, served, err := ghDocument(args)
	if err != nil {
		return "", "fake gh: " + err.Error() + "\n", 1
	}
	if served {
		return serveDocument(args, doc)
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

// ghDocument selects the canned JSON document a query reads, reporting whether
// the call serves one. `pr view` serves the per-PR file only when GH_PR_VIEW_DIR
// is set (backport-audit); open-backport-pr's simpler `pr view` returns
// GH_MERGE_OID instead.
func ghDocument(args []string) (string, bool, error) {
	if len(args) < 2 {
		return "", false, nil
	}
	switch {
	case args[0] == "issue" && args[1] == "list":
		return readDocument(envOption("GH_ISSUES_JSON"))
	case args[0] == "pr" && args[1] == "view" && envOption("GH_PR_VIEW_DIR") != "" && len(args) >= 3:
		return prViewDocument(envOption("GH_PR_VIEW_DIR"), args[2])
	case args[0] == "pr" && args[1] == "list":
		return prListDocument(args)
	}
	return "", false, nil
}

// prViewDocument returns the per-PR view document, or an empty object when the
// fixture wrote none.
func prViewDocument(dir, number string) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "pr-"+number+".json"))
	if err == nil {
		return string(data), true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "{}\n", true, nil
	}
	return "", false, err
}

// prListDocument picks the document for a `pr list` call from its flags, the way
// the bash fake's case did.
func prListDocument(args []string) (string, bool, error) {
	joined := " " + strings.Join(args, " ") + " "
	switch {
	case strings.Contains(joined, " --state open "):
		return readDocument(envOption("GH_OPEN_JSON"))
	case strings.Contains(joined, " --label "):
		if rows := envOption("GH_MERGED_ROWS"); rows != "" {
			return manyRows(rows)
		}
		return readDocument(envOption("GH_MERGED_JSON"))
	case strings.Contains(joined, " --state merged "):
		if rows := envOption("GH_MERGED_BACKPORTS_ROWS"); rows != "" {
			return manyRows(rows)
		}
		return readDocument(envOption("GH_MERGED_BACKPORTS_JSON"))
	}
	return readDocument(envOption("GH_MERGED_BACKPORTS_JSON"))
}

// readDocument returns the content of a document file.
func readDocument(path string) (string, bool, error) {
	if path == "" {
		return "", false, fmt.Errorf("no document path configured")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// manyRows builds a page of rows with no merge commit, to model a truncated
// (full) page.
func manyRows(rows string) (string, bool, error) {
	count, err := strconv.Atoi(rows)
	if err != nil {
		return "", false, err
	}
	records := make([]map[string]any, 0, count)
	for i := range count {
		records = append(records, map[string]any{
			"number":      1000 + i,
			"title":       "t",
			"url":         "u",
			"mergedAt":    "2020-01-01T00:00:00Z",
			"mergeCommit": map[string]any{"oid": ""},
		})
	}
	data, err := json.Marshal(records)
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// serveDocument applies the query gh was given through its real jq, matching
// gh's --jq/-q.
func serveDocument(args []string, doc string) (string, string, int) {
	query := jqQuery(args)
	if query == "" {
		return doc, "", 0
	}
	command := exec.Command("jq", "-r", query)
	command.Stdin = strings.NewReader(doc)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", "fake gh: jq " + query + ": " + err.Error() + " " + stderr.String(), 1
	}
	return stdout.String(), "", 0
}

// requestsJSONField reports whether a gh call asks for name in its --json field
// list (a comma-separated value), so the fake can reject a request for a field
// the audit must never fetch.
func requestsJSONField(args []string, name string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--json" {
			continue
		}
		for field := range strings.SplitSeq(args[i+1], ",") {
			if strings.TrimSpace(field) == name {
				return true
			}
		}
	}
	return false
}

// jqQuery returns the expression following -q or --jq, or "".
func jqQuery(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-q" || args[i] == "--jq" {
			return args[i+1]
		}
	}
	return ""
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
		binDir:   BinDir(t, "gh"),
		argvPath: filepath.Join(t.TempDir(), "argv.txt"),
		prURL:    "https://github.com/o/r/pull/99",
	}
}

// run executes the script with dir as BACKPORT_REPO and args, returning its
// captured result. extra overlays the harness's base environment.
func (h *openHarness) run(dir, mergeOID string, extra map[string]string, args ...string) ScriptResult {
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
	return RunScript(h.t, dir, env, repositoryScript(h.t, "open-backport-pr.sh"), args...)
}

// argv returns the recorded gh argv log.
func (h *openHarness) argv() string {
	return readText(h.t, h.argvPath)
}

// openFixture is a repository plus the hashes a backport case needs.
type openFixture struct {
	repo   *Repo
	origin string
	merge  plumbing.Hash
	first  plumbing.Hash
	second plumbing.Hash
}

// seedFixture builds a repo with a release/0.1 branch on the base commit and one
// fix commit on main (the merged PR), pushed to a bare origin.
func seedFixture(tb testing.TB) *openFixture {
	tb.Helper()
	repo := New(tb)
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
	repo := New(tb)
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
	repo := New(tb)
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
	repo := New(tb)
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
	repo := New(tb)
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
func portSameDiff(tb testing.TB, repo *Repo, source plumbing.Hash, file, content string) {
	tb.Helper()
	repo.Write(file, content)
	repo.Commit("port\n\n(cherry picked from commit " + source.String() + ")")
}

// assertStatus fails when the script's exit status differs from want.
func assertStatus(t *testing.T, result ScriptResult, want int) {
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

// auditHarness drives backport-audit.sh with a fake gh and the canned gh
// documents the audit reads.
type auditHarness struct {
	t                   *testing.T
	binDir              string
	argvPath            string
	mergedJSONPath      string
	openJSONPath        string
	mergedBackportsPath string
	issuesJSONPath      string
	prViewDir           string
}

func newAuditHarness(t *testing.T) *auditHarness {
	t.Helper()
	root := t.TempDir()
	h := &auditHarness{
		t:                   t,
		binDir:              BinDir(t, "gh"),
		argvPath:            filepath.Join(root, "argv.txt"),
		mergedJSONPath:      filepath.Join(root, "merged.json"),
		openJSONPath:        filepath.Join(root, "open.json"),
		mergedBackportsPath: filepath.Join(root, "merged-backports.json"),
		issuesJSONPath:      filepath.Join(root, "issues.json"),
		prViewDir:           filepath.Join(root, "pr-view"),
	}
	if err := os.MkdirAll(h.prViewDir, 0o750); err != nil {
		t.Fatalf("create pr-view dir: %v", err)
	}
	writeDoc(t, h.openJSONPath, "[]\n")
	writeDoc(t, h.mergedBackportsPath, "[]\n")
	writeDoc(t, h.issuesJSONPath, "[]\n")
	return h
}

func (h *auditHarness) run(repoDir string, extra map[string]string, args ...string) ScriptResult {
	h.t.Helper()
	_ = os.Remove(h.argvPath)
	env := map[string]string{
		"BACKPORT_AUDIT_REPO":      repoDir,
		"GH_ARGV":                  h.argvPath,
		"GH_MERGED_JSON":           h.mergedJSONPath,
		"GH_OPEN_JSON":             h.openJSONPath,
		"GH_MERGED_BACKPORTS_JSON": h.mergedBackportsPath,
		"GH_ISSUES_JSON":           h.issuesJSONPath,
		"GH_PR_VIEW_DIR":           h.prViewDir,
		"PATH":                     h.binDir + string(os.PathListSeparator) + envOption("PATH"),
	}
	maps.Copy(env, extra)
	return RunScript(h.t, repoDir, env, repositoryScript(h.t, "backport-audit.sh"), args...)
}

func (h *auditHarness) argv() string {
	return readText(h.t, h.argvPath)
}

// auditPull is one row of a gh pull document.
type auditPull struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	MergedAt    string `json:"mergedAt"`
	Body        string `json:"body,omitempty"`
	State       string `json:"state,omitempty"`
	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

// auditReference is one closedByPullRequestsReferences entry.
type auditReference struct {
	Number int `json:"number"`
}

// auditIssue is one row of a gh issue document.
type auditIssue struct {
	Number   int              `json:"number"`
	ClosedBy []auditReference `json:"closedByPullRequestsReferences"`
}

func writeDoc(tb testing.TB, path, content string) {
	tb.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatalf("write %s: %v", path, err)
	}
}

func writeJSON(tb testing.TB, path string, value any) {
	tb.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		tb.Fatalf("marshal %s: %v", path, err)
	}
	writeDoc(tb, path, string(data)+"\n")
}

func (h *auditHarness) writeMerged(mergedAt, oid string) {
	pull := auditPull{Number: 7, Title: "Fix the bug", URL: "https://github.com/o/r/pull/7", MergedAt: mergedAt}
	pull.MergeCommit.OID = oid
	writeJSON(h.t, h.mergedJSONPath, []auditPull{pull})
}

func (h *auditHarness) writeOpenBackports(pulls ...auditPull) {
	writeJSON(h.t, h.openJSONPath, pulls)
}

func (h *auditHarness) writeMergedBackports(pulls ...auditPull) {
	writeJSON(h.t, h.mergedBackportsPath, pulls)
}

func (h *auditHarness) writePRView(state, oid string) {
	pull := auditPull{
		Number:   7,
		Title:    "Fix the bug",
		URL:      "https://github.com/o/r/pull/7",
		MergedAt: "2020-01-01T00:00:00Z",
		State:    state,
	}
	pull.MergeCommit.OID = oid
	writeJSON(h.t, filepath.Join(h.prViewDir, "pr-7.json"), pull)
}

func (h *auditHarness) writeIssueLinks(numbers ...int) {
	issue := auditIssue{Number: 5}
	for _, number := range numbers {
		issue.ClosedBy = append(issue.ClosedBy, auditReference{Number: number})
	}
	writeJSON(h.t, h.issuesJSONPath, []auditIssue{issue})
}

// seedAuditRepository builds a repo with a base on main, a release/0.1 branch on
// the base, and one fix commit on main, returning the fix commit.
func seedAuditRepository(tb testing.TB) (*Repo, plumbing.Hash) {
	tb.Helper()
	repo := New(tb)
	repo.Write("f", "a\n")
	base := repo.Commit("base")
	repo.SetBranch("release/0.1", base)
	repo.Write("g", "b\n")
	merge := repo.Commit("fix")
	return repo, merge
}

// isoFromEpoch formats an epoch as the UTC ISO-8601 mergedAt gh reports.
func isoFromEpoch(epoch int64) string {
	return time.Unix(epoch, 0).UTC().Format("2006-01-02T15:04:05Z")
}
