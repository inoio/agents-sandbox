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
if [[ "${#heavy_targets[@]}" -gt 0 ]]; then
  for h in "${heavy_targets[@]}"; do
    if ! is_target "$h"; then
      printf 'check: heavy target not in the target list: %s\n' "$h" >&2
      exit 2
    fi
  done
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/check.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

is_heavy() {
  if [[ "${#heavy_targets[@]}" -eq 0 ]]; then return 1; fi
  local h
  for h in "${heavy_targets[@]}"; do [[ "$h" == "$1" ]] && return 0; done
  return 1
}

default_jobs() {
  local n q
  n="$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 2)"
  q=$((n / 4))
  if [[ "$q" -lt 2 ]]; then q=2; fi
  printf '%s' "$q"
}

capture=1
if [[ "${VERBOSE:-0}" == 1 ]]; then capture=0; fi

pool_width="${JOBS:-$(default_jobs)}"
if [[ "${SERIAL:-0}" == 1 ]]; then pool_width=1; fi

declare -a logs statfiles
for i in "${!targets[@]}"; do
  logs[i]="$tmp/log.$i"
  statfiles[i]="$tmp/status.$i"
done

run_target() {
  local i="$1" target="${targets[$1]}" status started elapsed
  started="$SECONDS"
  if [[ "$capture" == 1 ]]; then
    if ( cd "$root" && MAKEFLAGS='' "${MAKE:-make}" --no-print-directory "$target" ) >"${logs[$i]}" 2>&1; then
      status=0
    else
      status=$?
    fi
  else
    if ( cd "$root" && MAKEFLAGS='' "${MAKE:-make}" --no-print-directory "$target" ); then
      status=0
    else
      status=$?
    fi
  fi
  elapsed=$((SECONDS - started))
  printf '%s\n' "$status" >"${statfiles[$i]}"
  if [[ "$capture" == 1 ]]; then
    if [[ "$status" -eq 0 ]]; then
      printf 'PASS %s %ss\n' "$target" "$elapsed"
    else
      printf 'FAIL %s %ss\n' "$target" "$elapsed"
    fi
  fi
  return 0
}

# Run the light targets in a pool of pool_width, each in its own subshell.
run_light_pool() {
  local i pid
  local -a pids=()
  for i in "${light_idx[@]}"; do
    run_target "$i" &
    pids+=("$!")
    while [[ "${#pids[@]}" -ge "$pool_width" ]]; do
      wait "${pids[0]}" || true
      pids=("${pids[@]:1}")
    done
  done
  if [[ "${#pids[@]}" -gt 0 ]]; then
    for pid in "${pids[@]}"; do wait "$pid" || true; done
  fi
}

declare -a heavy_idx=() light_idx=()
for i in "${!targets[@]}"; do
  if is_heavy "${targets[$i]}"; then
    heavy_idx+=("$i")
  else
    light_idx+=("$i")
  fi
done

if [[ "${SERIAL:-0}" == 1 ]]; then
  for i in "${!targets[@]}"; do run_target "$i"; done
else
  pool_group=""
  if [[ "${#light_idx[@]}" -gt 0 ]]; then
    run_light_pool &
    pool_group=$!
  fi
  if [[ "${#heavy_idx[@]}" -gt 0 ]]; then
    for i in "${heavy_idx[@]}"; do run_target "$i"; done
  fi
  if [[ -n "$pool_group" ]]; then wait "$pool_group" || true; fi
fi

failed=0
for i in "${!targets[@]}"; do
  status="$(cat "${statfiles[$i]}")"
  if [[ "$status" -ne 0 ]]; then
    failed=1
    if [[ "$capture" == 1 ]]; then
      printf '\n--- FAIL %s ---\n' "${targets[$i]}"
      cat "${logs[$i]}"
    fi
  fi
done

if [[ "$failed" -ne 0 ]]; then
  exit 1
fi
exit 0
