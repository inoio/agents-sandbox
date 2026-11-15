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
# labeled PR is missing from the release branch, 2 on a usage error.
#
# Env (all overridable):
#   BACKPORT_AUDIT_REPO            repository to inspect (default: .)
#   BACKPORT_AUDIT_DEFAULT_BRANCH  base branch fixes merge into (default: main)
#   BACKPORT_AUDIT_LABEL           label marking a fix for backporting
#                                  (default: needs-backport)
#   BACKPORT_AUDIT_GRACE_SECONDS   skip a PR merged this recently (default: 7200)
#   BACKPORT_AUDIT_NOW             current time in epoch seconds (default: date +%s)
#
# Contract:
#   Latest line: the highest-semver release/<major>.<minor> among local
#   refs/heads/release/* and refs/remotes/origin/release/*. With none it prints
#   "backport-audit: no release branch; nothing to audit" and exits 0.
#
#   Expected set: gh pr list --state merged --base "$DEFAULT" --label "$LABEL"
#   --json number,title,url,mergedAt,mergeCommit --limit 200.
#
#   Landed test: the release branch's patch-id set is
#   git log -p --no-merges "<release>" | git patch-id --stable | awk '{print $1}' | sort -u.
#
#   Port test: a PR's commits are
#   git log --no-merges --reverse --format=%H "<merge>^1..<merge>"; each commit's
#   patch-id is git show "<sha>" | git patch-id --stable | awk '{print $1}'.
#   The PR is debt when any of its patch-ids is absent from the branch set.
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
# --base wants. A local branch is preferred over an equally-versioned remote.
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

# The merged labeled PRs, one "<number>\t<title>\t<url>\t<mergedAt>\t<merge-oid>"
# record per line. gh's built-in jq flattens the JSON so no external parser is
# needed; a null mergeCommit leaves the last field empty and the PR is skipped.
merged="$(gh pr list --state merged --base "$default_branch" --label "$label" \
  --json number,title,url,mergedAt,mergeCommit --limit 200 \
  --jq '.[] | [(.number|tostring), .title, .url, .mergedAt, .mergeCommit.oid] | @tsv')"
if [[ -z "$merged" ]]; then
  exit 0
fi

branch_ids="$(branch_patch_ids)"
open_bodies="$(gh pr list --state open --base "$release_name" --json number,body --jq '.[].body')"

# A shipped backport PR targeting the release branch suppresses its source PR's
# debt: the body references the source as #<number> or by its full URL.
references_open_backport() {
  if printf '%s\n' "$open_bodies" | grep -qwF -- "#$1"; then return 0; fi
  if [[ -n "$2" ]] && printf '%s\n' "$open_bodies" | grep -qwF -- "$2"; then return 0; fi
  return 1
}

debt="$(
  printf '%s\n' "$merged" | while IFS=$'\t' read -r number title url merged_at merge_oid; do
    [[ -n "$number" ]] || continue
    within_grace "$merged_at" && continue
    references_open_backport "$number" "$url" && continue

    missing=0
    if [[ -n "$merge_oid" ]]; then
      commits="$(git -C "$repo" log --no-merges --reverse --format=%H "${merge_oid}^1..${merge_oid}" 2>/dev/null || true)"
      while IFS= read -r sha; do
        [[ -n "$sha" ]] || continue
        patch_id="$(git -C "$repo" show "$sha" | git patch-id --stable | awk '{print $1}')"
        if ! printf '%s\n' "$branch_ids" | grep -qxF -- "$patch_id"; then
          missing=$((missing + 1))
        fi
      done <<<"$commits"
    fi

    if [[ "$missing" -gt 0 ]]; then
      printf '#%s\t%s\t%s\tmissing:%s\n' "$number" "$title" "$url" "$missing"
    fi
  done
)"

if [[ -n "$debt" ]]; then
  printf '%s\n' "$debt"
  exit 1
fi
exit 0
