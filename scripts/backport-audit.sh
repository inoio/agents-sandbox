#!/usr/bin/env bash
#
# backport-audit.sh - report fixes merged to the default branch but never
# ported to the latest release branch.
#
# A fix is authored on the default branch and cherry-picked onto the newest
# release/<major>.<minor> line. This finds a merged PR labeled `needs-backport`
# whose commits are not all present on that line, so the port was forgotten.
#
# Usage:
#   backport-audit.sh
#
# Exit status: 0 when no backport debt is outstanding, 1 when at least one
# labeled PR is missing from the release branch, 2 on a usage error, when an
# expected-set query came back full (truncated), when a merged PR's merge commit
# is not in the local clone, or when a merged PR's commits cannot be read — the
# caller must treat every 2 as an error, never as clean.
#
# Env (all overridable):
#   BACKPORT_AUDIT_REPO            repository to inspect (default: .)
#   BACKPORT_AUDIT_DEFAULT_BRANCH  base branch fixes merge into (default: main)
#   BACKPORT_AUDIT_LABEL           label marking a fix for backporting
#                                  (default: needs-backport)
#   BACKPORT_AUDIT_GRACE_SECONDS   skip a PR merged this recently (default: 7200)
#   BACKPORT_AUDIT_NOW             current time in epoch seconds (default: date +%s)
#   BACKPORT_AUDIT_PAGE_LIMIT      rows requested per expected-set query, and the
#                                  count at which the page fails as truncated
#                                  (default: 5000)
#
# Contract:
#   Latest line: the highest-semver release/<major>.<minor> among local
#   refs/heads/release/* and refs/remotes/origin/release/*. With none it prints
#   "backport-audit: no release branch; nothing to audit" and exits 0.
#
#   Expected set (union, deduplicated by PR number):
#   A. gh pr list --state merged --base "$DEFAULT" --label "$LABEL"
#      --json number,title,url,mergedAt,mergeCommit --limit "$PAGE_LIMIT".
#   B. every merged PR that closes a `$LABEL`-labeled issue:
#      gh issue list --state all --label "$LABEL"
#      --json number,closedByPullRequestsReferences --limit "$PAGE_LIMIT",
#      then
#      gh pr view <n> --json number,title,url,mergedAt,mergeCommit,state for
#      each referenced PR, keeping only state MERGED. The issue label is the
#      durable record; enqueue-pr.sh also copies it onto the PR, but a merge
#      path that skips the copy (the raw mutation, "Merge when ready") must not
#      hide a fix.
#
#   Complete set: gh returns the newest N rows and drops the rest with no
#   signal, so a truncated window would read as clean and close the rolling
#   issue. Each source is therefore requested with --limit
#   BACKPORT_AUDIT_PAGE_LIMIT (default 5000) and a page that comes back with
#   that many rows or more fails loudly (exit 2) instead of being audited. The
#   open-backport suppression query carries the same cap and guard. The limit is
#   a lifetime ceiling over both cumulative queries, not a per-run one, so the
#   default is set past any realistic accumulation; raise the knob when a
#   repository outgrows it. No date window narrows the queries: it would drop
#   old debt silently, which is the failure this guard exists to prevent. The
#   cost is paging — gh fetches --limit 5000 internally, roughly 50 requests per
#   source per run.
#
#   Landed test: the release branch's patch-id set is
#   git log -p --no-merges "<release>" | git patch-id --stable | awk '{print $1}' | sort -u.
#
#   Port test: a PR's commits are
#   git log --no-merges --reverse --format=%H "<merge>^1..<merge>"; each commit's
#   patch-id is git show "<sha>" | git patch-id --stable | awk '{print $1}'.
#   The PR is debt when any of its patch-ids is absent from the branch set. An
#   empty patch-id means the commit changes nothing (an empty commit), so it
#   needs no port and is never debt.
#
#   Unanswerable query: a merge commit the local clone does not have (a stale
#   clone, or the API's merge oid not fetched yet) cannot be walked, so the run
#   aborts with 2 and says to fetch and rerun. Treating the empty range as
#   "nothing missing" would report a false clean.
#
#   Noise control (skip, not debt): mergedAt strictly within GRACE_SECONDS of
#   NOW, or an open PR targeting the release branch whose body references this
#   PR's #<number> or URL.
#
# Output: one "#<number>\t<title>\t<url>\tmissing:<n>" line per debt PR.

set -euo pipefail

repo="${BACKPORT_AUDIT_REPO:-.}"
default_branch="${BACKPORT_AUDIT_DEFAULT_BRANCH:-main}"
label="${BACKPORT_AUDIT_LABEL:-needs-backport}"
grace_seconds="${BACKPORT_AUDIT_GRACE_SECONDS:-7200}"
now="${BACKPORT_AUDIT_NOW:-$(date +%s)}"
page_limit="${BACKPORT_AUDIT_PAGE_LIMIT:-5000}"

if [[ $# -gt 0 ]]; then
  case "$1" in
    -h|--help)
      sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      printf 'backport-audit: unexpected argument: %s\n' "$1" >&2
      exit 2
      ;;
  esac
fi

if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  printf 'backport-audit: not a git repository: %s\n' "$repo" >&2
  exit 2
fi

# The highest-semver release/<major>.<minor> as "<major> <minor> <git-ref>
# <branch-name>", or nothing when the repository has no release branch. The
# git-ref is what `git log` is resolved against; the branch-name is what gh's
# --base wants. When the same version exists both locally and on origin the
# remote-tracking ref sorts last and wins the version tie.
latest_release_branch() {
  local refs refname name version major minor
  refs="$(git -C "$repo" for-each-ref --format='%(refname)' \
    'refs/heads/release/*' 'refs/remotes/origin/release/*' || true)"
  [[ -n "$refs" ]] || return 1
  while IFS= read -r refname; do
    [[ -n "$refname" ]] || continue
    case "$refname" in
      refs/heads/release/*) name="${refname#refs/heads/}" ;;
      refs/remotes/origin/release/*) name="${refname#refs/remotes/origin/}" ;;
      *) continue ;;
    esac
    version="${name#release/}"
    [[ "$version" == *.* ]] || continue
    major="${version%%.*}"
    minor="${version#*.}"
    printf '%s %s %s %s\n' "$major" "$minor" "$refname" "$name"
  done <<<"$refs" | LC_ALL=C sort -k1,1n -k2,2n | tail -n1
}

# The release branch's landed patch-ids, one per line.
branch_patch_ids() {
  git -C "$repo" log -p --no-merges "$release_ref" \
    | git patch-id --stable \
    | awk '{print $1}' \
    | sort -u
}

# merged_epoch EPOCH_OF_ISO: 0 and prints the epoch, or 1 when `date` cannot
# parse the timestamp (the grace test then does not skip the PR).
epoch_of_iso() {
  local epoch
  if epoch="$(date -u -d "$1" +%s 2>/dev/null)"; then printf '%s' "$epoch"; return 0; fi
  if epoch="$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$1" +%s 2>/dev/null)"; then printf '%s' "$epoch"; return 0; fi
  return 1
}

within_grace() {
  local merged_epoch
  merged_epoch="$(epoch_of_iso "$1")" || return 1
  [[ $((now - merged_epoch)) -lt $grace_seconds ]]
}

release_line="$(latest_release_branch || true)"
if [[ -z "$release_line" ]]; then
  printf 'backport-audit: no release branch; nothing to audit\n'
  exit 0
fi
read -r _major _minor release_ref release_name <<<"$release_line"

# gh returns the newest N rows and silently drops the rest. Every expected-set
# query asks for $PAGE_LIMIT rows and a page at the cap aborts with 2: a
# truncated set must never read as clean, because the next step would then
# close the rolling issue and erase the signal. The default sits past any
# realistic accumulation (the queries are cumulative over the repository's whole
# history); the knob is the escape hatch if one outgrows it.
#
# require_complete_page SOURCE COUNT fails when a page came back at the cap.
require_complete_page() {
  local source="$1" count="$2"
  if [[ "$count" -ge "$page_limit" ]]; then
    printf 'backport-audit: %s returned %s rows, at the --limit %s cap; the set is truncated, so this run cannot be trusted as clean\n' \
      "$source" "$count" "$page_limit" >&2
    exit 2
  fi
}

count_lines() {
  if [[ -z "$1" ]]; then printf '0'; return 0; fi
  printf '%s\n' "$1" | wc -l | tr -d ' '
}

# The expected set is the union of two sources, one
# "<number>\t<title>\t<url>\t<mergedAt>\t<merge-oid>" record per line. A null
# mergeCommit leaves the last field empty and the PR is skipped.

# Source A: PRs merged to the default branch that carry the label themselves.
pr_labeled="$(gh pr list --state merged --base "$default_branch" --label "$label" \
  --json number,title,url,mergedAt,mergeCommit --limit "$page_limit" \
  --jq '.[] | [(.number|tostring), .title, .url, .mergedAt, .mergeCommit.oid] | @tsv')"
require_complete_page "gh pr list (merged, labeled)" "$(count_lines "$pr_labeled")"

# Source B: the issue label is the durable record. enqueue-pr.sh copies it onto
# the PR, but a merge path that skips the copy (the raw mutation, "Merge when
# ready") leaves the merged PR unlabeled. Resolve each labeled issue's closing
# PRs and keep the merged ones, so no merge path hides a fix. The referenced
# field may be a list of objects or of numbers; both flatten to the number.
labeled_issues="$(gh issue list --state all --label "$label" \
  --json number,closedByPullRequestsReferences --limit "$page_limit")"
require_complete_page "gh issue list (labeled)" "$(printf '%s' "$labeled_issues" | jq 'length')"
issue_pr_numbers="$(printf '%s' "$labeled_issues" \
  | jq -r '.[] | .closedByPullRequestsReferences[]? | if type == "object" then .number else . end' \
  | sort -u)"

issue_labeled=""
while IFS= read -r number; do
  [[ -n "$number" ]] || continue
  issue_labeled+="$(gh pr view "$number" \
    --json number,title,url,mergedAt,mergeCommit,state \
    --jq 'select(.state == "MERGED") | [(.number|tostring), .title, .url, .mergedAt, .mergeCommit.oid] | @tsv')"$'\n'
done <<<"$issue_pr_numbers"

# Union the two sources, keeping the first record for any PR number.
merged="$(printf '%s\n%s\n' "$pr_labeled" "$issue_labeled" \
  | awk -F'\t' 'NF && !seen[$1]++')"
if [[ -z "$merged" ]]; then
  exit 0
fi

branch_ids="$(branch_patch_ids)"
open_backports="$(gh pr list --state open --base "$release_name" \
  --json number,body --limit "$page_limit")"
require_complete_page "gh pr list (open backports)" "$(printf '%s' "$open_backports" | jq 'length')"
open_bodies="$(printf '%s' "$open_backports" | jq -r '.[].body // ""')"

# A shipped backport PR targeting the release branch suppresses its source PR's
# debt: the body references the source as #<number> or by its full URL.
references_open_backport() {
  if printf '%s\n' "$open_bodies" | grep -qwF -- "#$1"; then return 0; fi
  if [[ -n "$2" ]] && printf '%s\n' "$open_bodies" | grep -qwF -- "$2"; then return 0; fi
  return 1
}

debt_status=0
debt="$(
  printf '%s\n' "$merged" | while IFS=$'\t' read -r number title url merged_at merge_oid; do
    [[ -n "$number" ]] || continue
    within_grace "$merged_at" && continue
    references_open_backport "$number" "$url" && continue

    missing=0
    if [[ -n "$merge_oid" ]]; then
      # A merge commit the clone does not have (its origin/main was not fetched
      # since the PR merged) makes `git log <merge>^1..<merge>` exit 128 with
      # empty stdout, which would read as "no commits missing" — a false clean.
      # The question is unanswerable, so fail closed and tell the operator how
      # to make it answerable.
      if ! git -C "$repo" cat-file -e "${merge_oid}^{commit}" 2>/dev/null; then
        printf 'backport-audit: merge commit %s for PR #%s is not in the local clone; fetch origin and the release branches, then rerun\n' \
          "$merge_oid" "$number" >&2
        exit 2
      fi
      if ! commits="$(git -C "$repo" log --no-merges --reverse --format=%H "${merge_oid}^1..${merge_oid}")"; then
        printf 'backport-audit: could not read the commits of PR #%s (merge commit %s); fetch and rerun\n' \
          "$number" "$merge_oid" >&2
        exit 2
      fi
      while IFS= read -r sha; do
        [[ -n "$sha" ]] || continue
        if ! patch_id="$(git -C "$repo" show "$sha" | git patch-id --stable | awk '{print $1}')"; then
          printf 'backport-audit: could not read the patch-id of %s (PR #%s); fetch and rerun\n' \
            "$sha" "$number" >&2
          exit 2
        fi
        # An empty patch-id is a commit that changes nothing (an empty commit),
        # so there is nothing to port and it can never be debt.
        [[ -n "$patch_id" ]] || continue
        if ! printf '%s\n' "$branch_ids" | grep -qxF -- "$patch_id"; then
          missing=$((missing + 1))
        fi
      done <<<"$commits"
    fi

    if [[ "$missing" -gt 0 ]]; then
      printf '#%s\t%s\t%s\tmissing:%s\n' "$number" "$title" "$url" "$missing"
    fi
  done
)" || debt_status=$?

# The loop above reports an unanswerable query by exiting 2 inside the
# substitution, so re-exit with that status: a non-zero here is never clean.
if [[ "$debt_status" -ne 0 ]]; then
  exit "$debt_status"
fi

if [[ -n "$debt" ]]; then
  printf '%s\n' "$debt"
  exit 1
fi
exit 0
