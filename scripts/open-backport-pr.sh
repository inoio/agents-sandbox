#!/usr/bin/env bash
#
# open-backport-pr.sh - port a fix merged to the default branch onto the newest
# release line and open the backport pull request.
#
# A fix is authored on the default branch and cherry-picked onto the newest
# release/<major>.<minor> line (backport-audit.sh reports the ones that was
# forgotten for). This performs one such port: it cherry-picks the merged PR's
# commits onto a fresh branch off the release line, pushes it, and opens the PR.
#
# Usage:
#   open-backport-pr.sh <PR_NUMBER>
#
# Exit status: 0 when the backport branch is pushed and the PR is opened; 1 when
# the cherry-pick conflicts (nothing is pushed), the PR is unknown or unmerged,
# or there is no release branch; 2 on a usage error; 3 when nothing needed
# porting (every source commit is already on the release line, or every
# remaining pick came out empty), so nothing is pushed and no PR is opened.
#
# Env (all overridable):
#   BACKPORT_REPO            repository to operate on (default: .)
#   BACKPORT_RELEASE_BRANCH  release branch to port onto
#                            (default: the latest release/<major>.<minor> line)
#   BACKPORT_BRANCH_PREFIX   prefix for the backport branch (default: backport/)
#
# Contract:
#   Latest line: the highest-semver release/<major>.<minor> among local
#   refs/heads/release/* and refs/remotes/origin/release/*, re-derived here with
#   the same rule backport-audit.sh uses (kept duplicated: a shared helper is not
#   yet warranted).
#
#   Source commits: the merged PR's commits are
#   git log --no-merges --reverse --format=%H "<merge>^1..<merge>", where <merge>
#   is `gh pr view <n> --json mergeCommit -q .mergeCommit.oid` (the merge queue
#   rebases a PR, so the API's original SHAs may not exist locally).
#
#   Port: git fetch origin "<release>"; the release branch's patch-id set is
#   git log -p --no-merges "origin/<release>" | git patch-id --stable
#   | awk '{print $1}' | sort -u (the same construction backport-audit.sh
#   uses); every source commit already in that set is skipped, so a partially
#   ported PR (a commit hand-ported earlier, an adapted port) does not abort on
#   an empty cherry-pick. A source commit whose patch-id is empty changes
#   nothing (an empty commit) and is skipped too, because there is nothing to
#   port. Then git checkout -b "<prefix><n>-<line>" "origin/<release>"; git
#   cherry-pick -x each remaining commit; git push -u origin <branch>; gh pr
#   create --base "<release>" --head "<branch>" --title "Backport #<n> to
#   <line>" --body "<one line per ported commit + 'Backport of #<n>.'>". When
#   every source commit is already present, or every remaining pick turns out
#   empty, it prints that the PR is fully ported and exits 3 without touching the
#   remote.
#
#   Safety: a conflicting cherry-pick runs git cherry-pick --abort, prints why,
#   and exits 1 without pushing - a half-applied port never reaches the remote.
#   A patch-id match is "already applied" and is skipped; only a real conflict
#   (different content at the same place) aborts. A pick that git reports as now
#   empty is aborted and skipped like a patch-id match: the port has nothing to
#   apply there, so it is not a conflict and must not strand the PR.

set -euo pipefail

repo="${BACKPORT_REPO:-.}"
branch_prefix="${BACKPORT_BRANCH_PREFIX:-backport/}"
release_override="${BACKPORT_RELEASE_BRANCH:-}"

if [[ $# -eq 1 && ( "$1" == "-h" || "$1" == "--help" ) ]]; then
  sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
fi
if [[ $# -ne 1 ]]; then
  printf 'open-backport-pr: expected a pull request number\n' >&2
  exit 2
fi
pr="$1"
if [[ ! "$pr" =~ ^[1-9][0-9]*$ ]]; then
  printf 'open-backport-pr: expected a pull request number: %s\n' "$pr" >&2
  exit 2
fi

if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  printf 'open-backport-pr: not a git repository: %s\n' "$repo" >&2
  exit 1
fi

# The highest-semver release/<major>.<minor> as "<major> <minor> <git-ref>
# <branch-name>", or nothing when the repository has no release branch. When the
# same version exists both locally and on origin the remote-tracking ref sorts
# last and wins the version tie.
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

if [[ -n "$release_override" ]]; then
  release_name="$release_override"
else
  release_line="$(latest_release_branch || true)"
  if [[ -z "$release_line" ]]; then
    printf 'open-backport-pr: no release branch to port onto\n' >&2
    exit 1
  fi
  read -r _major _minor _release_ref release_name <<<"$release_line"
fi
line="${release_name#release/}"

merge="$(gh pr view "$pr" --json mergeCommit -q .mergeCommit.oid 2>/dev/null || true)"
if [[ -z "$merge" || "$merge" == "null" ]]; then
  printf 'open-backport-pr: PR #%s has no merge commit (unknown or unmerged)\n' "$pr" >&2
  exit 1
fi

commits="$(git -C "$repo" log --no-merges --reverse --format=%H "${merge}^1..${merge}" 2>/dev/null || true)"
if [[ -z "$commits" ]]; then
  printf 'open-backport-pr: PR #%s has no commits to port\n' "$pr" >&2
  exit 1
fi

git -C "$repo" fetch origin "$release_name"

# The release branch's landed patch-ids, one per line (the construction
# backport-audit.sh uses). A source commit already in this set is applied.
landed_patch_ids() {
  git -C "$repo" log -p --no-merges "origin/$release_name" \
    | git patch-id --stable \
    | awk '{print $1}' \
    | sort -u
}

source_patch_id() {
  git -C "$repo" show "$1" | git patch-id --stable | awk '{print $1}'
}

branch_ids="$(landed_patch_ids)"
portable_commits=()
while IFS= read -r sha; do
  [[ -n "$sha" ]] || continue
  patch_id="$(source_patch_id "$sha")"
  # An empty patch-id is a commit that changes nothing (an empty commit): there
  # is nothing to port, and git would refuse the pick as empty.
  if [[ -z "$patch_id" ]]; then
    printf 'open-backport-pr: skipping %s (empty commit; nothing to port)\n' "$sha"
    continue
  fi
  if printf '%s\n' "$branch_ids" | grep -qxF -- "$patch_id"; then
    continue
  fi
  portable_commits+=("$sha")
done <<<"$commits"

if [[ ${#portable_commits[@]} -eq 0 ]]; then
  printf 'open-backport-pr: PR #%s is already fully ported onto %s; nothing to do\n' \
    "$pr" "$release_name"
  exit 3
fi

backport_branch="${branch_prefix}${pr}-${line}"
git -C "$repo" checkout -b "$backport_branch" "origin/$release_name"

ported_subjects=()
for sha in "${portable_commits[@]}"; do
  if ! cherry_pick_output="$(git -C "$repo" cherry-pick -x "$sha" 2>&1)"; then
    git -C "$repo" cherry-pick --abort || true
    # A pick that applied to nothing is not a conflict: the commit's change is
    # already present in another form, so skip it and keep porting the rest.
    if printf '%s\n' "$cherry_pick_output" | grep -qi 'is now empty'; then
      printf 'open-backport-pr: skipping %s (cherry pick is now empty; nothing to port)\n' "$sha"
      continue
    fi
    printf 'open-backport-pr: cherry-pick of %s onto %s conflicts; nothing was pushed. Port it by hand and rerun.\n' \
      "$sha" "$release_name" >&2
    exit 1
  fi
  ported_subjects+=("$(git -C "$repo" log -1 --format=%s "$sha")")
done

# Every pick became empty (the change is already present in another form), so
# there is nothing to push: an empty backport PR would be noise.
if [[ ${#ported_subjects[@]} -eq 0 ]]; then
  git -C "$repo" checkout --quiet -
  git -C "$repo" branch -D "$backport_branch" >/dev/null
  printf 'open-backport-pr: PR #%s is already fully ported onto %s; nothing to do\n' \
    "$pr" "$release_name"
  exit 3
fi

git -C "$repo" push -u origin "$backport_branch"

body="$(printf '%s\n' "${ported_subjects[@]}")"
body="${body}"$'\n'"Backport of #${pr}."

url="$(gh pr create --base "$release_name" --head "$backport_branch" \
  --title "Backport #${pr} to ${line}" --body "$body")"
printf 'open-backport-pr: %s\n' "$url"
