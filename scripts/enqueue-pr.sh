#!/usr/bin/env bash
#
# enqueue-pr.sh - put pull requests into the merge queue: wait for their
# required checks, then enqueue them; with --stack, land a stack of PRs bottom-up.
#
# `main` merges only through the merge queue and repo auto-merge is disabled, so
# `gh pr merge` is refused. The request goes through the REST async merge
# (`PUT /repos/{owner}/{repo}/pulls/<n>/merge-async`, `merge_action=merge_queue`,
# pinned to the PR's head SHA): unlike the GraphQL `enqueuePullRequest`
# mutation, it also accepts a PR that was stacked on another one. It fails until
# the required checks have passed, so the script waits for them first.
#
# A single PR is enqueued and the script returns; it does not wait for the queue
# to merge it. A stack is landed one PR at a time: each PR whose base is the
# previous PR's branch is retargeted to the default branch once that PR merged,
# enqueued, and waited for until it merges, so a still-open PR below it is never
# taken along. The stack stops at the first PR that fails, is closed, or is
# dropped from the queue.
#
# Usage:
#   enqueue-pr.sh <PR_NUMBER>
#   enqueue-pr.sh --stack <PR_NUMBER> <PR_NUMBER>...   (bottom first)
#
# A stack's list starts at its bottom PR, the one based on the default branch;
# the script cannot tell which PR a merged base branch belonged to.
#
# Exit status: 0 when every PR is merged or was enqueued (with --stack: merged),
# 1 when a PR's checks failed, a PR has merge conflicts, was closed, has a base
# outside the stack, was dropped from the queue, or the enqueue request failed,
# 2 on a usage error.

set -euo pipefail

# Polling interval while waiting for GitHub to register a freshly pushed PR's
# checks; overridable so a test or tight loop does not sleep.
ENQUEUE_PR_RETRY_SECONDS="${ENQUEUE_PR_RETRY_SECONDS:-10}"

# The REST API version the async merge is requested under.
ENQUEUE_PR_API_VERSION="2026-03-10"
# Polling interval while a stacked PR waits in the merge queue; overridable so a
# test does not sleep.
ENQUEUE_PR_POLL_SECONDS="${ENQUEUE_PR_POLL_SECONDS:-30}"
# How long a failed check watch waits before it re-reads the PR's head, so a
# push GitHub has not registered yet shows; overridable so a test does not sleep.
ENQUEUE_PR_SETTLE_SECONDS="${ENQUEUE_PR_SETTLE_SECONDS:-20}"

usage() {
  sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
}

stack=0
if [[ "${1:-}" == "--stack" ]]; then
  stack=1
  shift
fi
if [[ $# -eq 1 && ( "$1" == "-h" || "$1" == "--help" ) ]]; then
  usage
  exit 0
fi
if [[ $# -lt 1 || ( "$stack" == 0 && $# -ne 1 ) ]]; then
  usage
  exit 2
fi
for pr in "$@"; do
  if [[ ! "$pr" =~ ^[1-9][0-9]*$ ]]; then
    printf 'enqueue-pr: expected a pull request number: %s\n' "$pr" >&2
    exit 2
  fi
done

# GitHub reports a merge conflict as `mergeable: CONFLICTING` / `mergeStateStatus:
# DIRTY`. A conflicted PR runs no checks and the merge queue ejects it, so refuse
# it here rather than waiting for checks that never arrive and reporting a queue
# entry that cannot merge.
refuse_if_conflicted() {
  local state
  state="$(gh pr view "$pr" --json mergeable,mergeStateStatus -q '.mergeable + " " + .mergeStateStatus' 2>/dev/null || true)"
  case "$state" in
    CONFLICTING*|*DIRTY)
      printf 'enqueue-pr: PR #%s has merge conflicts; rebase it before enqueueing\n' "$pr" >&2
      return 1
      ;;
  esac
}

# `gh pr checks` exits non-zero both when a check failed and when no checks are
# reported yet (a push GitHub has not registered). Wait the latter out so a
# freshly pushed PR is not mistaken for a failure; any other result returns at
# once, so a real failure still reaches the watch below. A conflict that appears
# while waiting aborts at once — its checks will never report.
#
# Every read is scoped with `--required`: the merge queue is gated by the
# ruleset's required checks, and an advisory red check (codecov/patch, mutation,
# hermetic) is not one of them, so it must not hold the enqueue back.
wait_for_checks() {
  local attempt result
  for ((attempt = 0; attempt < 30; attempt++)); do
    refuse_if_conflicted || return 1
    if result="$(gh pr checks "$pr" --required 2>&1)"; then return 0; fi
    [[ "$result" == *"no checks reported"* ]] || return 0
    sleep "$ENQUEUE_PR_RETRY_SECONDS"
  done
}

# head_commit prints the PR's head commit, or nothing when it cannot be read.
head_commit() {
  gh pr view "$pr" --json headRefOid -q .headRefOid 2>/dev/null || true
}

# `gh pr checks --watch` redraws the whole check table every few seconds and
# exits 8 while checks are still pending rather than 0. Keep that redraw out of
# the helper's output — it is meant to be read as a few lines — and classify the
# non-zero exit by the PR's state: a merged or closed PR will never report again.
#
# Right after a push, GitHub can still report the previous head commit's
# checks, green or red. So a watch counts only for the head it started on:
# after a pass the head is read again, and a moved head is watched anew; after
# a failure the script settles, watches anew when the head moved, and otherwise
# confirms the failure with a plain `gh pr checks --required` (exit 1 = a failed
# required check) before it stands. It sets watched_head to the commit whose
# checks passed, which the enqueue pins.
watch_checks() {
  refuse_if_conflicted || return 1
  wait_for_checks || return 1
  local status attempt head_before head_after
  watched_head=""
  for ((attempt = 0; attempt < 3; attempt++)); do
    head_before="$(head_commit)"
    status=0
    gh pr checks "$pr" --required --watch --fail-fast >/dev/null 2>&1 || status=$?
    if [[ "$status" == 8 ]]; then
      printf 'enqueue-pr: PR #%s is not ready: its checks are still pending\n' "$pr" >&2
      return 1
    fi
    if [[ "$status" != 0 ]]; then
      if [[ "$(pr_state)" == "CLOSED" ]]; then
        printf 'enqueue-pr: PR #%s was closed while waiting for its checks\n' "$pr" >&2
        return 1
      fi
      sleep "$ENQUEUE_PR_SETTLE_SECONDS"
    fi
    head_after="$(head_commit)"
    if [[ -n "$head_before" && "$head_after" == "$head_before" ]]; then
      if [[ "$status" == 0 ]]; then
        watched_head="$head_before"
        return 0
      fi
      status=0
      gh pr checks "$pr" --required >/dev/null 2>&1 || status=$?
      if [[ "$status" == 1 ]]; then
        printf 'enqueue-pr: PR #%s checks did not pass\n' "$pr" >&2
        return 1
      fi
      printf 'enqueue-pr: PR #%s: its checks changed after the watch; watching again\n' "$pr"
    else
      printf 'enqueue-pr: PR #%s: its head moved to %s; watching the new commit\n' "$pr" "${head_after:-?}"
    fi
    wait_for_checks || return 1
  done
  printf 'enqueue-pr: PR #%s: gave up after %s watches; its head or checks kept changing\n' "$pr" "$attempt" >&2
  return 1
}

# Sets pr_state. Prints why it cannot proceed and returns 1 for a state the
# queue will not accept, so a merged or closed PR never reaches the request.
load_pr() {
  pr_state="$(gh pr view "$pr" --json state -q .state)"

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

# A fix that must also reach the shipped release line carries the marker on its
# GitHub issue (the maintainer sets it; a bug without it is intentionally
# main-only). The nightly backport audit reads the marker from the merged PR, so
# copy it across from any linked issue before the PR merges. Adding a label the
# PR already carries is a no-op in gh, so this is safe to repeat. The copy is
# convenience only — the issue label is the durable record — so a refused copy
# warns and continues rather than aborting the enqueue.
#
# It runs after the enqueue, not before the check watch: adding a label fires the
# `labeled` pull_request event, and ci.yml cancels an in-flight run for the same
# PR (`cancel-in-progress: true`), so labelling first would restart the very run
# being watched here (Docker `integration` job included).
carry_backport_marker() {
  local issue
  while read -r issue; do
    [[ -n "$issue" ]] || continue
    # No `-q`: it exits at the first match and `pipefail` then reads the
    # producer's SIGPIPE as a non-match.
    if gh issue view "$issue" --json labels -q '.labels[].name' 2>/dev/null | grep -x 'needs-backport' >/dev/null; then
      if gh pr edit "$pr" --add-label needs-backport >/dev/null; then
        printf 'enqueue-pr: PR #%s: marked needs-backport (a backport PR will be opened by the backport audit)\n' "$pr"
      else
        printf 'enqueue-pr: PR #%s: could not copy the marker; the issue label still governs the audit\n' "$pr" >&2
      fi
      return 0
    fi
  done < <(gh pr view "$pr" --json closingIssuesReferences -q '.closingIssuesReferences[].number' 2>/dev/null || true)
}

# enqueue_pr resolves, waits for, and enqueues PR $pr. It returns 0 when the PR
# was enqueued or is already merged (merged then set to 1), and 1 otherwise.
enqueue_pr() {
  merged=0
  printf 'enqueue-pr: PR #%s: resolving\n' "$pr"
  if ! load_pr; then
    # A merged PR is the goal state, not a failure; a closed one is not.
    if [[ "${pr_state:-}" == "MERGED" ]]; then
      merged=1
      return 0
    fi
    return 1
  fi

  printf 'enqueue-pr: PR #%s: waiting for checks\n' "$pr"
  watch_checks || return 1

  # A conflict can appear while the checks run (another PR merged first); refuse
  # it before the request rather than enqueueing an entry the queue will eject.
  refuse_if_conflicted || return 1

  printf 'enqueue-pr: PR #%s: enqueueing\n' "$pr"
  local head="$watched_head" status
  # Called under `||`, so set -e does not apply here: every step checks itself.
  # The enqueue pins the head whose checks the watch saw pass.
  if [[ -z "$head" ]]; then
    printf 'enqueue-pr: PR #%s: could not read its head commit\n' "$pr" >&2
    return 1
  fi
  if ! status="$(gh api -X PUT "repos/{owner}/{repo}/pulls/$pr/merge-async" \
    -H "X-GitHub-Api-Version: $ENQUEUE_PR_API_VERSION" \
    -f merge_action=merge_queue -f sha="$head" --jq .status)"; then
    printf 'enqueue-pr: PR #%s: enqueue request failed (its required checks must have passed)\n' "$pr" >&2
    return 1
  fi
  printf 'enqueue-pr: PR #%s: enqueued (%s)\n' "$pr" "${status:-queued}"

  # After the enqueue, so its `labeled` event cannot cancel the run the watch
  # above just observed. The issue label is the audit's durable record either way.
  carry_backport_marker || true
}

# wait_merged polls PR $pr until it merges. Three polls in a row outside the
# queue mean the queue dropped it (a failed merge-group run, a conflict).
wait_merged() {
  local state queued fields outside=0
  printf 'enqueue-pr: PR #%s: waiting for the merge queue\n' "$pr"
  while :; do
    # gh pr view has no isInMergeQueue field, so ask GraphQL; gh fills in the
    # {owner} and {repo} placeholders from the current repository.
    # shellcheck disable=SC2016 # the GraphQL variables are literal, not shell expansions
    if ! fields="$(gh api graphql -F owner='{owner}' -F name='{repo}' -F n="$pr" \
      -f query='query($owner:String!,$name:String!,$n:Int!){repository(owner:$owner,name:$name){pullRequest(number:$n){state isInMergeQueue}}}' \
      --jq '.data.repository.pullRequest | [.state, .isInMergeQueue] | @tsv')"; then
      printf 'enqueue-pr: PR #%s: could not read its state while it waits in the queue\n' "$pr" >&2
      return 1
    fi
    IFS=$'\t' read -r state queued <<<"$fields"
    case "$state" in
      MERGED)
        printf 'enqueue-pr: PR #%s: merged\n' "$pr"
        return 0
        ;;
      CLOSED)
        printf 'enqueue-pr: PR #%s was closed while in the merge queue\n' "$pr" >&2
        return 1
        ;;
    esac
    if [[ "$queued" == "true" ]]; then outside=0; else outside=$((outside + 1)); fi
    if ((outside >= 3)); then
      printf 'enqueue-pr: PR #%s was dropped from the merge queue\n' "$pr" >&2
      return 1
    fi
    sleep "$ENQUEUE_PR_POLL_SECONDS"
  done
}

# retarget_onto_default points PR $pr at the default branch when its base is
# the branch of the stack's previous PR ($previous_head), which has merged. A
# base that is neither is an unmerged PR outside the stack: refuse it, since
# enqueueing would take that PR along.
retarget_onto_default() {
  local base
  if ! base="$(gh pr view "$pr" --json baseRefName -q .baseRefName)" || [[ -z "$base" ]]; then
    printf 'enqueue-pr: PR #%s: could not read its base branch\n' "$pr" >&2
    return 1
  fi
  [[ "$base" == "$default_branch" ]] && return 0
  if [[ -n "$previous_head" && "$base" == "$previous_head" ]]; then
    printf 'enqueue-pr: PR #%s: retargeting from %s to %s\n' "$pr" "$base" "$default_branch"
    if ! gh pr edit "$pr" --base "$default_branch" >/dev/null; then
      printf 'enqueue-pr: PR #%s: retargeting to %s failed; not enqueueing it on %s\n' \
        "$pr" "$default_branch" "$base" >&2
      return 1
    fi
    return 0
  fi
  printf 'enqueue-pr: PR #%s is based on %s, which is not the previous PR in the stack; land that first\n' \
    "$pr" "$base" >&2
  return 1
}

if [[ "$stack" == 0 ]]; then
  enqueue_pr
  exit $?
fi

default_branch="$(gh repo view --json defaultBranchRef -q .defaultBranchRef.name)"
previous_head=""
for pr in "$@"; do
  retarget_onto_default || exit 1
  head_branch="$(gh pr view "$pr" --json headRefName -q .headRefName)" || exit 1
  enqueue_pr || exit 1
  if [[ "$merged" == 0 ]]; then
    wait_merged || exit 1
  fi
  previous_head="$head_branch"
done
printf 'enqueue-pr: stack landed: %s\n' "$*"
