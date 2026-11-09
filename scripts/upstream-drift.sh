#!/usr/bin/env bash
#
# upstream-drift.sh - report how far the upstream branch has moved past this
# branch and whether it touched the same files.
#
# Answers the two questions a long-lived branch keeps asking: "did origin/main
# move?" and "did it touch my files?" It prints the commits and files each side
# added since their merge-base, then the overlap - the branch's likely conflict
# sites on the next rebase.
#
# This is a reporting tool: it never rebases, and it always exits 0 so it can be
# run freely in a loop. For the mechanics of a rebase in progress, use
# rebase-status.sh.
#
# Usage:
#   upstream-drift.sh [options]
#
# Options:
#   --base <ref>    Upstream ref to compare against (default: origin/main).
#   --repo <dir>    Repository worktree to inspect (default: the CWD).
#   --no-fetch      Do not run `git fetch origin` first (offline / already fresh).
#   --quiet         Print only the summary counts and the overlap paths.
#   -h, --help      Show this help.
#
# Exit status: 0 on success (drift or not), 2 on a usage or repository error.

set -euo pipefail

base="origin/main"
repo="$PWD"
do_fetch=true
quiet=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base) base="$2"; shift 2 ;;
    --repo) repo="$2"; shift 2 ;;
    --no-fetch) do_fetch=false; shift ;;
    --quiet) quiet=true; shift ;;
    -h|--help)
      sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) printf 'upstream-drift: unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  printf 'upstream-drift: not a git repository: %s\n' "$repo" >&2
  exit 2
fi

if "$do_fetch"; then
  git -C "$repo" fetch --quiet origin || {
    printf 'upstream-drift: git fetch origin failed (use --no-fetch to compare against local refs)\n' >&2
    exit 2
  }
fi

if ! git -C "$repo" rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  printf 'upstream-drift: upstream ref not found: %s\n' "$base" >&2
  exit 2
fi

merge_base="$(git -C "$repo" merge-base "$base" HEAD)"
if [[ -z "$merge_base" ]]; then
  printf 'upstream-drift: no merge-base between %s and HEAD\n' "$base" >&2
  exit 2
fi

upstream_commits="$(git -C "$repo" log --oneline "$merge_base..$base")"
branch_commits="$(git -C "$repo" log --oneline "$merge_base..HEAD")"
upstream_files="$(git -C "$repo" diff --name-only "$merge_base..$base" | sort)"
branch_files="$(git -C "$repo" diff --name-only "$merge_base..HEAD" | sort)"

overlap="$(comm -12 <(printf '%s\n' "$upstream_files") <(printf '%s\n' "$branch_files"))"

count_lines() {
  if [[ -z "$1" ]]; then printf '0'; else printf '%s' "$1" | grep -c .; fi
}

upstream_commit_count="$(count_lines "$upstream_commits")"
branch_commit_count="$(count_lines "$branch_commits")"
upstream_file_count="$(count_lines "$upstream_files")"
branch_file_count="$(count_lines "$branch_files")"
overlap_count="$(count_lines "$overlap")"

printf 'upstream drift: base=%s merge-base=%s\n' "$base" "${merge_base:0:12}"
printf '  upstream commits since merge-base: %s\n' "$upstream_commit_count"
printf '  branch commits since merge-base:   %s\n' "$branch_commit_count"
printf '  upstream files changed:            %s\n' "$upstream_file_count"
printf '  branch files changed:              %s\n' "$branch_file_count"
printf '  files changed on both sides:       %s\n' "$overlap_count"

if [[ "$overlap_count" -gt 0 ]]; then
  printf '\nLikely conflict sites (changed on both sides):\n'
  printf '%s\n' "$overlap" | sed 's/^/  /'
else
  printf '\nNo file changed on both sides in the final trees - likely a clean rebase.\n'
fi

if ! "$quiet"; then
  if [[ "$upstream_commit_count" -gt 0 ]]; then
    printf '\nUpstream commits:\n%s\n' "$(printf '%s\n' "$upstream_commits" | sed 's/^/  /')"
  fi
  if [[ "$overlap_count" -gt 0 ]]; then
    printf '\nUpstream commits touching the overlap:\n'
    overlap_paths=()
    while IFS= read -r path; do
      [[ -n "$path" ]] && overlap_paths+=("$path")
    done <<<"$overlap"
    git -C "$repo" log --oneline "$merge_base..$base" -- "${overlap_paths[@]}" | sed 's/^/  /'
  fi
fi

exit 0
