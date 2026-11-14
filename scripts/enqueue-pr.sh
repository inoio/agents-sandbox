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

# `gh pr checks --watch` exits 8 while checks are pending rather than 0, so a
# failed watch is only a check failure once the PR's state confirms it: the
# queue is refused while the PR is merged, implies the checks passed, or while
# it is closed, means nothing will run again.
watch_checks() {
  local status=0
  gh pr checks "$pr" --watch --fail-fast || status=$?
  case "$status" in
    0) return 0 ;;
    8) printf 'enqueue-pr: PR #%s is not ready: its checks are still pending\n' "$pr" >&2; return 1 ;;
    *)
      check_pr_state
      printf 'enqueue-pr: PR #%s checks did not pass\n' "$pr" >&2
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

# Prints the PR's state (or UNKNOWN), never failing: it is the diagnostic a
# failed watch reaches for, and it must not mask that failure.
check_pr_state() {
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
