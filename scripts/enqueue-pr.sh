#!/usr/bin/env bash
#
# enqueue-pr.sh - put a pull request into the merge queue: wait for its checks,
# then enqueue it.
#
# `main` merges only through the merge queue and repo auto-merge is disabled, so
# `gh pr merge` is refused and the request goes through the GraphQL
# `enqueuePullRequest` mutation instead. That mutation fails until the required
# checks have passed, so this collapses the hand-rolled
# `gh pr checks --watch` + `gh api graphql` watcher into one call.
#
# This is a helper, not a gate: it enqueues the PR and returns; it does not wait
# for the queue to merge it.
#
# Usage:
#   enqueue-pr.sh <PR_NUMBER>
#
# Exit status: 0 when the PR is already merged or was enqueued, 1 when the PR's
# checks failed, the PR was closed, the enqueue request failed, or the PR was
# closed while waiting, 2 on a usage error.

set -euo pipefail

# Polling interval while waiting for GitHub to register a freshly pushed PR's
# checks; overridable so a test or tight loop does not sleep.
ENQUEUE_PR_RETRY_SECONDS="${ENQUEUE_PR_RETRY_SECONDS:-10}"

if [[ $# -ne 1 || "$1" == "-h" || "$1" == "--help" ]]; then
  sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
  [[ $# -eq 1 ]] && exit 0
  exit 2
fi

pr="$1"
if [[ ! "$pr" =~ ^[1-9][0-9]*$ ]]; then
  printf 'enqueue-pr: expected a pull request number: %s\n' "$pr" >&2
  exit 2
fi

# `gh pr checks` exits non-zero both when a check failed and when no checks are
# reported yet (a push GitHub has not registered). Wait the latter out so a
# freshly pushed PR is not mistaken for a failure; any other result returns at
# once, so a real failure still reaches the watch below.
wait_for_checks() {
  local attempt result
  for ((attempt = 0; attempt < 30; attempt++)); do
    if result="$(gh pr checks "$pr" 2>&1)"; then return 0; fi
    [[ "$result" == *"no checks reported"* ]] || return 0
    sleep "$ENQUEUE_PR_RETRY_SECONDS"
  done
}

# `gh pr checks --watch` redraws the whole check table every few seconds and
# exits 8 while checks are still pending rather than 0. Keep that redraw out of
# the helper's output — it is meant to be read as a few lines — and classify the
# non-zero exit by the PR's state: a merged or closed PR will never report again.
watch_checks() {
  wait_for_checks
  local status=0
  gh pr checks "$pr" --watch --fail-fast >/dev/null 2>&1 || status=$?
  case "$status" in
    0) return 0 ;;
    8)
      printf 'enqueue-pr: PR #%s is not ready: its checks are still pending\n' "$pr" >&2
      return 1
      ;;
    *)
      if [[ "$(pr_state)" == "CLOSED" ]]; then
        printf 'enqueue-pr: PR #%s was closed while waiting for its checks\n' "$pr" >&2
      else
        printf 'enqueue-pr: PR #%s checks did not pass\n' "$pr" >&2
      fi
      return 1
      ;;
  esac
}

# Sets pr_state and pr_node_id. Prints why it cannot proceed and returns 1 for a
# state the queue will not accept, so a merged or closed PR never reaches the
# mutation.
load_pr() {
  local fields
  fields="$(gh pr view "$pr" --json state,id -q '[.state, .id] | @tsv')"
  IFS=$'\t' read -r pr_state pr_node_id <<<"$fields"

  case "$pr_state" in
    MERGED)
      printf 'enqueue-pr: PR #%s is already merged\n' "$pr"
      return 1
      ;;
    CLOSED)
      printf 'enqueue-pr: PR #%s is closed\n' "$pr" >&2
      return 1
      ;;
    OPEN) return 0 ;;
    *)
      printf 'enqueue-pr: PR #%s is in an unexpected state: %s\n' "$pr" "$pr_state" >&2
      return 1
      ;;
  esac
}

# The PR's state, or UNKNOWN: the diagnostic a failed watch reaches for, and it
# must not mask that failure.
pr_state() {
  gh pr view "$pr" --json state -q .state 2>/dev/null || printf 'UNKNOWN'
}

printf 'enqueue-pr: PR #%s: resolving\n' "$pr"
if ! load_pr; then
  # A merged PR is the goal state, not a failure; a closed one is not.
  [[ "${pr_state:-}" == "MERGED" ]] && exit 0
  if [[ "${pr_state:-}" == "CLOSED" ]]; then
    exit 1
  fi
  exit 1
fi

printf 'enqueue-pr: PR #%s: waiting for checks\n' "$pr"
watch_checks

printf 'enqueue-pr: PR #%s: enqueueing\n' "$pr"
mutation='mutation($id:ID!){ enqueuePullRequest(input:{pullRequestId:$id}){ mergeQueueEntry { position } } }'
if ! enqueue_result="$(gh api graphql -f query="$mutation" -f id="$pr_node_id")"; then
  printf 'enqueue-pr: PR #%s: enqueue request failed (its required checks must have passed)\n' "$pr" >&2
  exit 1
fi

position="$(printf '%s' "$enqueue_result" | jq -r '.data.enqueuePullRequest.mergeQueueEntry.position // empty' 2>/dev/null || true)"
if [[ -n "$position" ]]; then
  printf 'enqueue-pr: PR #%s: enqueued at position %s\n' "$pr" "$position"
else
  printf 'enqueue-pr: PR #%s: enqueued\n' "$pr"
fi
