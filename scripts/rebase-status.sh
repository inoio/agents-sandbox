#!/usr/bin/env bash
#
# rebase-status.sh - one-call summary of a rebase in progress: the step counter,
# the unmerged paths, and any conflict markers still left in the working tree.
#
# Replaces the several `git status` / `git log` / marker-scan calls a conflict
# resolution otherwise needs after every step. Safe to run whether or not a
# rebase is active.
#
# Usage:
#   rebase-status.sh [options]
#
# Options:
#   --repo <dir>    Repository worktree to inspect (default: the CWD).
#   -h, --help      Show this help.
#
# Exit status: 0 on success, 2 on a usage or repository error.

set -euo pipefail

repo="$PWD"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) repo="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) printf 'rebase-status: unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  printf 'rebase-status: not a git repository: %s\n' "$repo" >&2
  exit 2
fi

printf 'rebase status: %s\n' "$repo"

# --path-format=absolute: --git-path otherwise returns a path relative to the
# target repo root, which the directory tests below would resolve against the
# caller's CWD when --repo points elsewhere.
rebase_merge="$(git -C "$repo" rev-parse --path-format=absolute --git-path rebase-merge)"
rebase_apply="$(git -C "$repo" rev-parse --path-format=absolute --git-path rebase-apply)"

if [[ -d "$rebase_merge" || -d "$rebase_apply" ]]; then
  dir="$rebase_merge"
  [[ -d "$dir" ]] || dir="$rebase_apply"
  step="?"
  total="?"
  [[ -f "$dir/msgnum" ]] && step="$(<"$dir/msgnum")"
  [[ -f "$dir/end" ]] && total="$(<"$dir/end")"
  # Read the rebase target from the stored `onto`, not HEAD: HEAD detaches and
  # advances as commits replay, so after the first one it names the last applied
  # commit rather than the branch being rebased onto.
  onto="?"
  if [[ -f "$dir/onto" ]]; then
    onto="$(git -C "$repo" rev-parse --short "$(<"$dir/onto")" 2>/dev/null || echo '?')"
  fi
  head_name=""
  [[ -f "$dir/head-name" ]] && head_name="$(<"$dir/head-name")"
  printf '  state: rebase in progress (step %s/%s), onto %s%s\n' \
    "$step" "$total" "$onto" "${head_name:+  ($head_name)}"
else
  printf '  state: no rebase in progress\n'
fi

unmerged="$(git -C "$repo" diff --name-only --diff-filter=U)"
unmerged_count="$(printf '%s' "$unmerged" | grep -c . || true)"
printf '  unmerged paths: %s\n' "${unmerged_count:-0}"
[[ -n "$unmerged" ]] && printf '%s\n' "$unmerged" | sed 's/^/    /'

# Conflict markers left in the working tree (a file can keep them after it was
# staged, so scan the tree, not just the unmerged set). Prefer ripgrep; fall back
# to grep when it is absent.
if command -v rg >/dev/null 2>&1; then
  markers="$(rg -l --hidden --glob '!.git' --no-messages -e '^(<<<<<<<|=======|>>>>>>>)' "$repo" || true)"
else
  markers="$(grep -rlE --exclude-dir=.git '^(<<<<<<<|=======|>>>>>>>)' "$repo" || true)"
fi
marker_count="$(printf '%s' "$markers" | grep -c . || true)"
printf '  files with conflict markers: %s\n' "${marker_count:-0}"
[[ -n "$markers" ]] && printf '%s\n' "$markers" | sed "s|^$repo/|    |"

status="$(git -C "$repo" status --short)"
staged="$(printf '%s' "$status" | grep -c '^[MADRC]' || true)"
untracked="$(printf '%s' "$status" | grep -c '^??' || true)"
printf '  working tree: %s staged, %s untracked\n' "${staged:-0}" "${untracked:-0}"

exit 0
