#!/usr/bin/env bash
#
# changed-targets.sh - map the changed files to the `make check` targets to run.
#
# Prints, one per line and sorted, the subset of `make check` targets a change
# can affect. Whole targets are selected or skipped; nothing is scoped inside a
# target. An unrecognized path selects the full set (fail safe), as does an
# unresolvable base ref.
#
# This backs `make check-local-changes` and `make check-branch-changes`, the
# fast inner loop while iterating. It is not a gate: run `make check` before
# finalizing.
#
# Usage:
#   changed-targets.sh [options] <local|branch>
#
# Arguments:
#   local    Diff the uncommitted working tree against HEAD.
#   branch   Diff the working tree against the merge base of --base and HEAD
#            (committed and uncommitted changes together).
#
# Options:
#   --repo DIR   Repository worktree to inspect (default: the CWD).
#   --base REF   Branch-mode base ref to merge-base against (default: origin/main).
#                Ignored in local mode.
#   -h, --help   Show this help.
#
# Exit status: 0 on success (including an empty selection), 2 on a usage or
# repository error.

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
    *) printf 'changed-targets: unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [[ -z "$mode" ]]; then
  printf 'changed-targets: expected a mode: local or branch\n' >&2
  exit 2
fi

if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  printf 'changed-targets: not a git repository: %s\n' "$repo" >&2
  exit 2
fi

# The full `make check` target set, in no particular order (output is sorted).
# Also the fail-safe selection for an unknown path or an undecidable base.
all_targets=(
  validate-fmt lint test compile-all
  docs-linkcheck docs-linkcheck-test
  changelog-check changelog-check-test
  deadcode-test
  cleanup-branches-test envelope-test upstream-drift-test rebase-status-test
)

declare -A selected=()
add_targets() { local t; for t in "$@"; do selected["$t"]=1; done; }
select_all() { add_targets "${all_targets[@]}"; }

# An unresolvable base means we cannot know what changed, so fall back to the
# full set rather than passing silently.
branch_merge_base=""
if [[ "$mode" == "branch" ]]; then
  branch_merge_base="$(git -C "$repo" merge-base "$base" HEAD 2>/dev/null || true)"
  [[ -n "$branch_merge_base" ]] || select_all
elif ! git -C "$repo" rev-parse --verify --quiet HEAD >/dev/null; then
  select_all
fi

if ((${#selected[@]} == 0)); then
  if [[ "$mode" == "branch" ]]; then
    files="$(git -C "$repo" diff --name-only "$branch_merge_base")"
  else
    files="$(git -C "$repo" diff --name-only HEAD)"
  fi
  files="$(printf '%s\n%s\n' "$files" "$(git -C "$repo" ls-files --others --exclude-standard)")"

  while IFS= read -r path; do
    [[ -n "$path" ]] || continue
    known=0
    case "$path" in
      *.go|go.mod|go.sum|.golangci.yml) add_targets validate-fmt lint test compile-all; known=1 ;;
    esac
    case "$path" in
      cmd/*.go|internal/*.go) add_targets docs-linkcheck; known=1 ;;
    esac
    case "$path" in
      docs/*) add_targets docs-linkcheck; known=1 ;;
    esac
    case "$path" in
      */*) ;; # nested: the root-`*.md` rule below does not apply
      *.md) add_targets docs-linkcheck; known=1 ;;
    esac
    case "$path" in
      changelog.d/*) add_targets changelog-check; known=1 ;;
    esac
    case "$path" in
      ci/check-docs.sh|ci/test-check-docs.sh) add_targets docs-linkcheck docs-linkcheck-test; known=1 ;;
      ci/check-changelog.sh|ci/test-check-changelog.sh) add_targets changelog-check changelog-check-test; known=1 ;;
      ci/check-deadcode.sh|ci/test-check-deadcode.sh|ci/deadcodecheck/*) add_targets deadcode-test; known=1 ;;
      scripts/cleanup-merged-branches.sh|scripts/test-cleanup-merged-branches.sh) add_targets cleanup-branches-test; known=1 ;;
      scripts/test-envelope.sh|scripts/test-envelope_test.sh) add_targets envelope-test; known=1 ;;
      scripts/upstream-drift.sh|scripts/test-upstream-drift.sh) add_targets upstream-drift-test; known=1 ;;
      scripts/rebase-status.sh|scripts/test-rebase-status.sh) add_targets rebase-status-test; known=1 ;;
    esac
    case "$path" in
      .operator-shared/*) known=1 ;;
    esac
    if [[ "$known" -eq 0 ]]; then
      select_all
      break
    fi
  done <<<"$files"
fi

if ((${#selected[@]})); then
  printf '%s\n' "${!selected[@]}" | sort
fi
exit 0
