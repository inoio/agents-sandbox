#!/usr/bin/env bash
#
# changed-files.sh - print the paths a diff touched, one per line.
#
# The single source of truth for "what changed" in the fast inner loop: it
# resolves the same local/branch diffs `scripts/changed-targets.sh` maps to
# targets, so a consumer that needs the raw paths (the changelog presence check)
# does not re-derive the diff and drift from it.
#
# Usage:
#   changed-files.sh [options] <local|branch>
#
# Arguments:
#   local    The uncommitted working tree against HEAD, plus untracked files.
#   branch   The working tree against the merge base of --base and HEAD
#            (committed and uncommitted changes together), plus untracked files.
#
# Options:
#   --repo DIR   Repository worktree to inspect (default: the CWD).
#   --base REF   Branch-mode base ref to merge-base against (default: origin/main).
#                Ignored in local mode.
#   -h, --help   Show this help.
#
# Exit status: 0 on success (including an empty list); 3 when branch mode cannot
# resolve the base ref (the caller decides whether that is fatal — the map fails
# safe to the full target set, the changelog presence check skips); 2 on a usage
# error or a non-repository --repo; a git failure propagates git's non-zero
# status.

set -euo pipefail

repo="$PWD"
base="origin/main"
mode=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) repo="$2"; shift 2 ;;
    --base) base="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    local|branch) mode="$1"; shift ;;
    *) printf 'changed-files: unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [[ -z "$mode" ]]; then
  printf 'changed-files: expected a mode: local or branch\n' >&2
  exit 2
fi

if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  printf 'changed-files: not a git repository: %s\n' "$repo" >&2
  exit 2
fi

if [[ "$mode" == "branch" ]]; then
  merge_base="$(git -C "$repo" merge-base "$base" HEAD 2>/dev/null || true)"
  if [[ -z "$merge_base" ]]; then
    printf 'changed-files: cannot resolve base ref: %s\n' "$base" >&2
    exit 3
  fi
  files="$(git -C "$repo" diff --no-renames --name-only "$merge_base")"
elif ! git -C "$repo" rev-parse --verify --quiet HEAD >/dev/null; then
  files=""
else
  files="$(git -C "$repo" diff --no-renames --name-only HEAD)"
fi

# Assigned separately so a `git ls-files` failure aborts the script (set -e)
# instead of being swallowed by the printf, which would silently return none.
untracked="$(git -C "$repo" ls-files --others --exclude-standard)"
printf '%s\n%s\n' "$files" "$untracked" | sed '/^$/d'
exit 0
