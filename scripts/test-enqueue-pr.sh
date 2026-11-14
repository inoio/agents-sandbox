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
# `gh pr view --json ...` probes, and GH_CHECKS_EXIT / GH_ENQUEUE_EXIT drive the
# watch and the mutation. A canned view field can be an array, one JSON document
# per call, so a test can change the PR's state between probes.
fake_bin="$fixture_root/bin"
mkdir -p "$fake_bin"
cat >"$fake_bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${GH_ARGV:?}"
case " $* " in
  *" pr checks "*)
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
    # Honour gh's --jq/-q, the way the real CLI does, so the helper can select
    # fields exactly as it would against GitHub.
    query=""
    for ((i = 1; i <= $#; i++)); do
      if [[ "${!i}" == "-q" || "${!i}" == "--jq" ]]; then
        j=$((i + 1))
        query="${!j}"
      fi
    done
    if [[ -n "$query" ]]; then
      printf '%s' "$doc" | jq -r "$query"
    else
      printf '%s' "$doc"
    fi
    exit 0
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
unset GH_CHECKS_EXIT GH_ENQUEUE_EXIT

# run_script seeds the view payload, clears the argv recorder, and captures both
# streams into out and err, returning the helper's exit status.
run_script() {
  printf '%s' "$1" >"$GH_PR_VIEW"
  rm -f "$GH_ARGV" "$GH_PR_VIEW_INDEX"
  shift
  set +e
  out="$("$ENQUEUE" "$@" 2>"$fixture_root/stderr")"
  status=$?
  set -e
  err="$(cat "$fixture_root/stderr")"
}
argv() { [[ -f "$GH_ARGV" ]] && cat "$GH_ARGV" || true; }

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

echo "# enqueue-pr.sh (closed while waiting)"
export GH_CHECKS_EXIT=1
run_script '[{"state":"OPEN","id":"PR_5"},{"state":"CLOSED","id":"PR_5"}]' 42
if [[ "$status" -ne 0 ]]; then ok "closed during the watch exits non-zero"; else bad "closed during the watch exits non-zero"; fi
assert_contains "reports the PR is closed" "$err" "checks did not pass"
assert_not_contains "does not enqueue a closed PR" "$(argv)" "api graphql"
unset GH_CHECKS_EXIT

echo "# enqueue-pr.sh (closed)"
run_script '{"state":"CLOSED","id":"PR_6"}' 42
if [[ "$status" -ne 0 ]]; then ok "a closed PR exits non-zero"; else bad "a closed PR exits non-zero"; fi
assert_contains "says the PR is closed" "$err" "PR #42 is closed"
assert_not_contains "does not watch a closed PR" "$(argv)" "pr checks"

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
