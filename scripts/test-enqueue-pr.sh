#!/usr/bin/env bash
# Tests for scripts/enqueue-pr.sh.
#
# A fake `gh` first on PATH records its argv and serves canned JSON and exit
# codes, so every branch of the helper is exercised without a network or a real
# pull request. Never touches this repository. Run directly or
# `make enqueue-pr-test`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENQUEUE="$SCRIPT_DIR/enqueue-pr.sh"

pass=0
fail=0
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/enqueue-fixtures.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }
assert_contains()     { if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1 (missing: $3)"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then bad "$1 (unexpected: $3)"; else ok "$1"; fi; }

# The fake `gh`: gh_argv.txt records one call per line, GH_PR_VIEW serves the
# `gh pr view --json ...` probes, GH_ISSUE_LABELS (an issue number -> label-name
# array map) serves `gh issue view --json labels`, and GH_CHECKS_EXIT /
# GH_ENQUEUE_EXIT drive the watch and the mutation. A canned view field can be an
# array, one JSON document per call, so a test can change the PR's state between
# probes. `gh pr edit` only records argv.
fake_bin="$fixture_root/bin"
mkdir -p "$fake_bin"
cat >"$fake_bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${GH_ARGV:?}"

# Honour gh's --jq/-q the way the real CLI does, so the helper can select fields
# exactly as it would against GitHub.
apply_jq() {
  local query=""
  for ((i = 1; i <= $#; i++)); do
    if [[ "${!i}" == "-q" || "${!i}" == "--jq" ]]; then
      j=$((i + 1))
      query="${!j}"
    fi
  done
  if [[ -n "$query" ]]; then
    printf '%s' "$1" | jq -r "$query"
  else
    printf '%s' "$1"
  fi
}

case " $* " in
  *" pr checks "*)
    if [[ "${GH_CHECKS_NOREPORT:-0}" -gt 0 ]]; then
      n=0
      [[ -f "${GH_CHECKS_NOREPORT_FILE:?}" ]] && n="$(cat "$GH_CHECKS_NOREPORT_FILE")"
      if [[ "$n" -lt "$GH_CHECKS_NOREPORT" ]]; then
        echo "$((n + 1))" >"$GH_CHECKS_NOREPORT_FILE"
        echo "no checks reported on the 'x' branch" >&2
        exit 1
      fi
    fi
    echo "fake-tests	pass	pass	1m	https://example.test/checks"
    exit "${GH_CHECKS_EXIT:-0}"
    ;;
  *" pr view "*)
    fields="$(cat "${GH_PR_VIEW:?}")"
    if [[ "$fields" == \[* ]]; then
      index_file="${GH_PR_VIEW_INDEX:?}"
      index=0
      if [[ -f "$index_file" ]]; then index="$(cat "$index_file")"; fi
      echo "$((index + 1))" >"$index_file"
      doc="$(printf '%s' "$fields" | jq -c --argjson i "$index" '.[$i]')"
    else
      doc="$fields"
    fi
    apply_jq "$doc" "$@"
    exit 0
    ;;
  *" issue view "*)
    number=""
    for ((i = 1; i <= $#; i++)); do
      if [[ "${!i}" =~ ^[0-9]+$ ]]; then number="${!i}"; break; fi
    done
    doc="$(jq -c --arg n "$number" '(.[$n] // []) | {labels: map({name: .})}' "${GH_ISSUE_LABELS:?}")"
    apply_jq "$doc" "$@"
    exit 0
    ;;
  *" pr edit "*)
    exit "${GH_PR_EDIT_EXIT:-0}"
    ;;
  *" api graphql "*)
    if [[ "${GH_ENQUEUE_EXIT:-0}" -ne 0 ]]; then
      echo "GraphQL: Pull request is not mergeable" >&2
      exit 1
    fi
    echo '{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"position":3}}}}'
    exit 0
    ;;
esac
echo "fake gh: unexpected call: $*" >&2
exit 1
GH
chmod +x "$fake_bin/gh"
export PATH="$fake_bin:$PATH"
export GH_ARGV="$fixture_root/argv.txt"
export GH_PR_VIEW="$fixture_root/pr_view.json"
export GH_PR_VIEW_INDEX="$fixture_root/pr_view_index"
export GH_ISSUE_LABELS="$fixture_root/issue_labels.json"
printf '{}' >"$GH_ISSUE_LABELS"
unset GH_CHECKS_EXIT GH_ENQUEUE_EXIT GH_PR_EDIT_EXIT
export ENQUEUE_PR_RETRY_SECONDS=0

# run_script seeds the view payload, clears the argv recorder, and captures both
# streams into out and err, returning the helper's exit status. issue_labels sets
# the closing issues' labels for the next run (an issue number -> label array
# map); it defaults to none, so a case opts in.
run_script() {
  printf '%s' "$1" >"$GH_PR_VIEW"
  printf '%s' "${issue_labels:-}" >"$GH_ISSUE_LABELS"
  [[ -s "$GH_ISSUE_LABELS" ]] || printf '{}' >"$GH_ISSUE_LABELS"
  rm -f "$GH_ARGV" "$GH_PR_VIEW_INDEX"
  shift
  set +e
  out="$("$ENQUEUE" "$@" 2>"$fixture_root/stderr")"
  status=$?
  set -e
  err="$(cat "$fixture_root/stderr")"
}
argv() { if [[ -f "$GH_ARGV" ]]; then cat "$GH_ARGV"; fi; }

# assert_call_order NAME BEFORE AFTER asserts the BEFORE call was made before the
# AFTER call, from the recorded argv (one line per gh invocation).
assert_call_order() {
  local recorded before_line after_line
  recorded="$(argv)"
  before_line="$(grep -nF -- "$2" <<<"$recorded" | head -n1 | cut -d: -f1)"
  after_line="$(grep -nF -- "$3" <<<"$recorded" | head -n1 | cut -d: -f1)"
  if [[ -n "$before_line" && -n "$after_line" && "$before_line" -lt "$after_line" ]]; then
    ok "$1"
  else
    bad "$1 (before line ${before_line:-none}, after line ${after_line:-none})"
  fi
}

echo "# enqueue-pr.sh (already merged)"
run_script '{"state":"MERGED","id":"PR_1"}' 42
if [[ "$status" -eq 0 ]]; then ok "already merged exits 0"; else bad "already merged exits 0 (got $status)"; fi
assert_contains "says the PR is already merged" "$out" "already merged"
assert_not_contains "does not enqueue a merged PR" "$(argv)" "api graphql"

echo "# enqueue-pr.sh (checks pass)"
run_script '{"state":"OPEN","id":"PR_2"}' 42
if [[ "$status" -eq 0 ]]; then ok "success exits 0"; else bad "success exits 0 (got $status)"; fi
assert_contains "resolves the node id" "$(argv)" "pr view 42 --json state,id -q [.state, .id] | @tsv"
assert_contains "watches the checks" "$(argv)" "pr checks 42 --watch --fail-fast"
assert_contains "sends the enqueue mutation" "$(argv)" "enqueuePullRequest(input:{pullRequestId:\$id})"
assert_contains "passes the node id" "$(argv)" "id=PR_2"
assert_contains "prints the queue position" "$out" "enqueued at position 3"
assert_not_contains "does not dump the live check table" "$out" "fake-tests"

echo "# enqueue-pr.sh (linked issue needs a backport)"
export issue_labels='{"7":["needs-backport"]}'
run_script '{"state":"OPEN","id":"PR_10","closingIssuesReferences":[{"number":7}]}' 42
if [[ "$status" -eq 0 ]]; then ok "a marked issue still enqueues"; else bad "a marked issue still enqueues (got $status)"; fi
assert_contains "reads the PR's closing issues" "$(argv)" "pr view 42 --json closingIssuesReferences -q .closingIssuesReferences[].number"
assert_contains "reads the linked issue's labels" "$(argv)" "issue view 7 --json labels -q .labels[].name"
assert_contains "copies the marker onto the PR" "$(argv)" "pr edit 42 --add-label needs-backport"
assert_contains "prints the backport hint" "$out" "marked needs-backport (a backport PR will be opened by the backport audit)"
assert_contains "still enqueues" "$(argv)" "api graphql"
assert_call_order "copies the marker only after the enqueue" "api graphql" "pr edit 42 --add-label needs-backport"
unset issue_labels

echo "# enqueue-pr.sh (marker copy is refused)"
export issue_labels='{"7":["needs-backport"]}'
export GH_PR_EDIT_EXIT=1
run_script '{"state":"OPEN","id":"PR_13","closingIssuesReferences":[{"number":7}]}' 42
if [[ "$status" -eq 0 ]]; then ok "a refused marker copy still exits 0"; else bad "a refused marker copy still exits 0 (got $status)"; fi
assert_contains "warns that the copy failed" "$err" "could not copy the marker; the issue label still governs the audit"
assert_contains "still enqueues" "$(argv)" "api graphql"
assert_call_order "a refused copy is still attempted after the enqueue" "api graphql" "pr edit 42 --add-label needs-backport"
assert_not_contains "claims the marker was copied" "$out" "marked needs-backport"
unset issue_labels GH_PR_EDIT_EXIT

echo "# enqueue-pr.sh (linked issue does not need a backport)"
export issue_labels='{"7":[]}'
run_script '{"state":"OPEN","id":"PR_11","closingIssuesReferences":[{"number":7}]}' 42
if [[ "$status" -eq 0 ]]; then ok "an unmarked issue still enqueues"; else bad "an unmarked issue still enqueues (got $status)"; fi
assert_not_contains "does not label the PR" "$(argv)" "pr edit"
assert_not_contains "prints no backport hint" "$out" "needs-backport"
unset issue_labels

echo "# enqueue-pr.sh (no closing issues)"
run_script '{"state":"OPEN","id":"PR_12","closingIssuesReferences":[]}' 42
if [[ "$status" -eq 0 ]]; then ok "a PR with no closing issues enqueues"; else bad "a PR with no closing issues enqueues (got $status)"; fi
assert_not_contains "labels nothing to backport" "$(argv)" "pr edit"
assert_not_contains "reads no issue" "$(argv)" "issue view"

echo "# enqueue-pr.sh (checks not yet registered)"
export GH_CHECKS_NOREPORT=2
export GH_CHECKS_NOREPORT_FILE="$fixture_root/noreport"
rm -f "$GH_CHECKS_NOREPORT_FILE"
run_script '{"state":"OPEN","id":"PR_8"}' 42
if [[ "$status" -eq 0 ]]; then ok "waits for checks to register"; else bad "waits for checks to register (got $status)"; fi
assert_contains "enqueues once the checks appear" "$out" "enqueued at position 3"
unset GH_CHECKS_NOREPORT GH_CHECKS_NOREPORT_FILE

echo "# enqueue-pr.sh (checks fail)"
export GH_CHECKS_EXIT=1
run_script '{"state":"OPEN","id":"PR_3"}' 42
if [[ "$status" -ne 0 ]]; then ok "failing checks exit non-zero"; else bad "failing checks exit non-zero"; fi
assert_contains "names the check failure" "$err" "checks did not pass"
assert_not_contains "does not enqueue on failure" "$(argv)" "api graphql"
unset GH_CHECKS_EXIT

echo "# enqueue-pr.sh (checks pending)"
export GH_CHECKS_EXIT=8
run_script '{"state":"OPEN","id":"PR_4"}' 42
if [[ "$status" -ne 0 ]]; then ok "pending checks exit non-zero"; else bad "pending checks exit non-zero"; fi
assert_contains "names the pending state" "$err" "checks are still pending"
assert_not_contains "does not enqueue while pending" "$(argv)" "api graphql"
unset GH_CHECKS_EXIT

# The watch reaches pr_state on the fourth probe (resolve, conflict, conflict,
# then the failure diagnostic); the marker copy no longer consumes one, because
# it runs after the enqueue.
echo "# enqueue-pr.sh (closed while waiting)"
export GH_CHECKS_EXIT=1
run_script '[{"state":"OPEN","id":"PR_5"},{"state":"OPEN","id":"PR_5"},{"state":"OPEN","id":"PR_5"},{"state":"CLOSED","id":"PR_5"}]' 42
if [[ "$status" -ne 0 ]]; then ok "closed during the watch exits non-zero"; else bad "closed during the watch exits non-zero"; fi
assert_contains "reports the PR is closed" "$err" "closed while waiting"
assert_not_contains "does not enqueue a closed PR" "$(argv)" "api graphql"
unset GH_CHECKS_EXIT

echo "# enqueue-pr.sh (closed)"
run_script '{"state":"CLOSED","id":"PR_6"}' 42
if [[ "$status" -ne 0 ]]; then ok "a closed PR exits non-zero"; else bad "a closed PR exits non-zero"; fi
assert_contains "says the PR is closed" "$err" "PR #42 is closed"
assert_not_contains "does not watch a closed PR" "$(argv)" "pr checks"

echo "# enqueue-pr.sh (merge conflict)"
run_script '{"state":"OPEN","id":"PR_9","mergeable":"CONFLICTING","mergeStateStatus":"DIRTY"}' 42
if [[ "$status" -ne 0 ]]; then ok "a conflicted PR exits non-zero"; else bad "a conflicted PR exits non-zero"; fi
assert_contains "names the conflict" "$err" "merge conflicts"
assert_not_contains "does not enqueue a conflicted PR" "$(argv)" "api graphql"
assert_not_contains "does not watch a conflicted PR" "$(argv)" "pr checks"

echo "# enqueue-pr.sh (enqueue fails)"
export GH_ENQUEUE_EXIT=1
run_script '{"state":"OPEN","id":"PR_7"}' 42
if [[ "$status" -ne 0 ]]; then ok "enqueue failure exits non-zero"; else bad "enqueue failure exits non-zero"; fi
assert_contains "reports the enqueue failure" "$err" "enqueue request failed"
assert_contains "attempts the mutation" "$(argv)" "api graphql"
unset GH_ENQUEUE_EXIT

echo "# enqueue-pr.sh (usage)"
if "$ENQUEUE" --help >/dev/null 2>&1; then ok "--help exits 0"; else bad "--help exits 0"; fi
if "$ENQUEUE" >/dev/null 2>&1; then bad "no argument is a usage error"; else ok "no argument is a usage error"; fi
if "$ENQUEUE" not-a-number >/dev/null 2>&1; then bad "a non-numeric argument is a usage error"; else ok "a non-numeric argument is a usage error"; fi

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
