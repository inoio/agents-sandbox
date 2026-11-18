#!/usr/bin/env bash
#
# check.sh - run the `make check` targets with a concise per-target summary.
#
# Each target runs as its own `make` invocation with stdout+stderr captured to a
# per-target log. One `PASS <target> <n>s` / `FAIL <target> <n>s` line is printed
# as each target finishes; the captured log of every failing target is dumped
# last, so failures land at the end by construction. The CPU-heavy targets run
# one at a time; the rest run in a pool. See
# .operator-shared/specs/ci/quality-gates.md.
#
# Usage:
#   check.sh [--help] <target>...
#
# Environment:
#   CHECK_HEAVY_TARGETS  space-separated targets to run one at a time
#   JOBS                 light-pool width (default: max(2, nproc/4))
#   VERBOSE=1            stream each target's output instead of capturing it
#   SERIAL=1             run one target at a time
#   MAKE                 make command (default: make)
#
# Exit status: 0 if every target passed, 1 if any failed, 2 on a usage error or
# a heavy target that is not in the target list.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

targets=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) printf 'check: unknown option: %s\n' "$1" >&2; exit 2 ;;
    *) targets+=("$1"); shift ;;
  esac
done

if [[ "${#targets[@]}" -eq 0 ]]; then
  printf 'check: no targets given\n' >&2
  exit 2
fi

is_target() {
  local t
  for t in "${targets[@]}"; do [[ "$t" == "$1" ]] && return 0; done
  return 1
}

heavy_targets=()
if [[ -n "${CHECK_HEAVY_TARGETS:-}" ]]; then
  read -r -a heavy_targets <<<"${CHECK_HEAVY_TARGETS}"
fi
for h in "${heavy_targets[@]}"; do
  if ! is_target "$h"; then
    printf 'check: heavy target not in the target list: %s\n' "$h" >&2
    exit 2
  fi
done

tmp="$(mktemp -d "${TMPDIR:-/tmp}/check.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

run_order=()
declare -a run_status=()
declare -a run_log=()

index=0
for target in "${targets[@]}"; do
  log="$tmp/log.$index"
  started="$SECONDS"
  if ( cd "$root" && MAKEFLAGS='' "${MAKE:-make}" --no-print-directory "$target" ) >"$log" 2>&1; then
    status=0
  else
    status=$?
  fi
  elapsed=$((SECONDS - started))
  if [[ "$status" -eq 0 ]]; then
    printf 'PASS %s %ss\n' "$target" "$elapsed"
  else
    printf 'FAIL %s %ss\n' "$target" "$elapsed"
  fi
  run_order+=("$target")
  run_status+=("$status")
  run_log+=("$log")
  index=$((index + 1))
done

failed=0
for i in "${!run_order[@]}"; do
  if [[ "${run_status[$i]}" -ne 0 ]]; then
    failed=1
    printf '\n--- FAIL %s ---\n' "${run_order[$i]}"
    cat "${run_log[$i]}"
  fi
done

if [[ "$failed" -ne 0 ]]; then
  exit 1
fi
exit 0
