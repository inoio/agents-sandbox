#!/usr/bin/env bash
#
# check.sh - run the `make check` targets with a concise per-target summary.
#
# Each target runs as its own `make` invocation with stdout+stderr captured to a
# per-target log. One `PASS|FAIL <target> <n>s (cpu <m>s) [heavy|light]` line is
# printed as each target finishes, followed by a `check: <n> targets in <t>s`
# total; the captured log of every failing target is dumped last, so failures
# land at the end by construction. The CPU-heavy targets run one at a time; the
# rest run in a pool. A single writer (this shell) owns all output, so on an
# interactive terminal it keeps a live status line on the last row naming the
# running targets, grouped as `heavy`/`light`. See
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
#   CHECK_PROGRESS=1/0   force the live status line on/off (default: on for a TTY;
#                        a forced on still requires capture mode, so it is off under VERBOSE)
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

heavy_targets=()
if [[ -n "${CHECK_HEAVY_TARGETS:-}" ]]; then
  read -r -a heavy_targets <<<"${CHECK_HEAVY_TARGETS}"
fi

if [[ "${#targets[@]}" -eq 0 ]]; then
  printf 'check: no targets given\n' >&2
  exit 2
fi

is_target() {
  local t
  for t in "${targets[@]}"; do [[ "$t" == "$1" ]] && return 0; done
  return 1
}

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
serial=0
if [[ "${SERIAL:-0}" == 1 ]]; then pool_width=1; serial=1; fi

# The live status line needs capture mode (a single writer, this shell, see the
# schedule below) and a terminal to redraw; CHECK_PROGRESS forces it for testing,
# but a forced on still needs capture mode, so it stays off under VERBOSE where
# the pool owns stdout.
progress=0
case "${CHECK_PROGRESS:-}" in
  1) if [[ "$capture" == 1 ]]; then progress=1; fi ;;
  0) progress=0 ;;
  *) if [[ "$capture" == 1 && -t 1 ]]; then progress=1; fi ;;
esac

declare -a logs statfiles timefiles cpus state pid
for i in "${!targets[@]}"; do
  logs[i]="$tmp/log.$i"
  statfiles[i]="$tmp/status.$i"
  timefiles[i]="$tmp/time.$i"
  cpus[i]="$tmp/cpu.$i"
  state[i]=pending
  pid[i]=""
done

# TIMEFORMAT drives run_target's `time`, which measures a target's whole process
# subtree (its own seconds are folded in as each subshell exits).
TIMEFORMAT='%3U %3S'

# run_target runs one target to completion, capturing its output and recording
# its elapsed seconds, its subtree CPU seconds, and its exit status. In capture
# mode the target's stdout+stderr go to the log while `time` writes the CPU pair
# to a separate file; in VERBOSE mode the output streams and no CPU is recorded.
# The status file is written last, so its appearance marks completion for the
# scheduler; this function prints nothing.
run_target() {
  local i="$1"
  local target="${targets[$i]}" status started elapsed
  started="$SECONDS"
  if [[ "$capture" == 1 ]]; then
    if { time { ( cd "$root" && MAKEFLAGS='' "${MAKE:-make}" --no-print-directory "$target" ) >"${logs[$i]}" 2>&1; }; } 2>"${cpus[$i]}"; then
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
  printf '%s\n' "$elapsed" >"${timefiles[$i]}"
  printf '%s\n' "$status" >"${statfiles[$i]}"
}

# running_count counts the targets currently running, optionally restricted to a
# lane: "heavy" or "light" (the empty argument counts every lane).
running_count() {
  local want="${1:-}" i n=0
  for i in "${!targets[@]}"; do
    if [[ "${state[$i]}" != running ]]; then continue; fi
    if [[ -z "$want" ]]; then
      n=$((n + 1))
    elif [[ "$want" == heavy ]]; then
      if is_heavy "${targets[$i]}"; then n=$((n + 1)); fi
    else
      if ! is_heavy "${targets[$i]}"; then n=$((n + 1)); fi
    fi
  done
  printf '%s' "$n"
}

# first_pending echoes the lowest-index pending target in a lane ("heavy" or
# "light", or every lane when empty), or returns 1 when the lane is drained.
first_pending() {
  local want="${1:-}" i
  for i in "${!targets[@]}"; do
    if [[ "${state[$i]}" != pending ]]; then continue; fi
    if [[ "$want" == heavy ]]; then
      if ! is_heavy "${targets[$i]}"; then continue; fi
    elif [[ "$want" == light ]]; then
      if is_heavy "${targets[$i]}"; then continue; fi
    fi
    printf '%s' "$i"
    return 0
  done
  return 1
}

launch() {
  local i="$1"
  run_target "$i" &
  pid[i]=$!
  state[i]=running
}

# launch_available starts whatever may run now: with SERIAL, at most one target
# total; otherwise the light lane fills to JOBS and the heavy lane runs one at a
# time. Heavy and light run concurrently, as before.
launch_available() {
  local i
  if [[ "$serial" == 1 ]]; then
    if [[ "$(running_count)" == 0 ]]; then
      if i="$(first_pending)"; then launch "$i"; fi
    fi
    return
  fi
  while [[ "$(running_count light)" -lt "$pool_width" ]]; do
    if i="$(first_pending light)"; then launch "$i"; else break; fi
  done
  if [[ "$(running_count heavy)" == 0 ]]; then
    if i="$(first_pending heavy)"; then launch "$i"; fi
  fi
}

clear_progress() {
  if [[ "$progress" == 1 ]]; then printf '\r\033[K'; fi
}

# progress_width is the terminal column count, with a fixed fallback when the
# size is unknown (a forced-on line outside a terminal, or a missing tput).
progress_width() {
  local w="${COLUMNS:-}"
  if [[ ! "$w" =~ ^[0-9]+$ ]] || [[ "$w" -le 0 ]]; then
    w="$(tput cols 2>/dev/null || true)"
  fi
  if [[ ! "$w" =~ ^[0-9]+$ ]] || [[ "$w" -le 0 ]]; then w=80; fi
  printf '%s' "$w"
}

# draw_progress redraws the single status line in place, naming the running
# targets grouped by lane (a heavy target runs alone; the light pool runs in
# parallel), truncated to the terminal width so a long list cannot wrap and
# corrupt the row.
draw_progress() {
  if [[ "$progress" != 1 ]]; then return; fi
  local i heavy_list="" light_list="" line elapsed width
  for i in "${!targets[@]}"; do
    if [[ "${state[$i]}" != running ]]; then continue; fi
    if is_heavy "${targets[$i]}"; then
      heavy_list="${heavy_list:+$heavy_list }${targets[$i]}"
    else
      light_list="${light_list:+$light_list }${targets[$i]}"
    fi
  done
  elapsed=$((SECONDS - start_seconds))
  line="check: ${done_count}/${#targets[@]} done, ${elapsed}s"
  if [[ -n "$heavy_list" ]]; then line="$line, heavy $heavy_list"; fi
  if [[ -n "$light_list" ]]; then line="$line, light $light_list"; fi
  width="$(progress_width)"
  if [[ "${#line}" -gt "$width" ]]; then line="${line:0:$width}"; fi
  printf '\r\033[K%s' "$line"
}

# cpu_seconds sums a target's user+system CPU (from its `time` file) to whole
# seconds, falling back to 0 when the file is empty (a truncated run).
cpu_seconds() {
  local value
  value="$(awk '{printf "%.0f", $1 + $2}' "$1" 2>/dev/null || true)"
  if [[ -z "$value" ]]; then value=0; fi
  printf '%s' "$value"
}

# reap_finished prints each just-completed target's summary line (capture mode
# only) and advances the scheduler's accounting.
reap_finished() {
  local i status elapsed cpu lane
  for i in "${!targets[@]}"; do
    if [[ "${state[$i]}" != running ]]; then continue; fi
    if [[ ! -f "${statfiles[$i]}" ]]; then continue; fi
    wait "${pid[$i]}" 2>/dev/null || true
    state[i]="done"
    done_count=$((done_count + 1))
    if [[ "$capture" == 1 ]]; then
      status="$(cat "${statfiles[$i]}")"
      elapsed="$(cat "${timefiles[$i]}")"
      cpu="$(cpu_seconds "${cpus[$i]}")"
      lane=light
      if is_heavy "${targets[$i]}"; then lane=heavy; fi
      clear_progress
      if [[ "$status" -eq 0 ]]; then
        printf 'PASS %s %ss (cpu %ss) [%s]\n' "${targets[$i]}" "$elapsed" "$cpu" "$lane"
      else
        printf 'FAIL %s %ss (cpu %ss) [%s]\n' "${targets[$i]}" "$elapsed" "$cpu" "$lane"
      fi
    fi
  done
}

start_seconds=$SECONDS
done_count=0

launch_available
while [[ "$done_count" -lt "${#targets[@]}" ]]; do
  reap_finished
  launch_available
  draw_progress
  if [[ "$done_count" -lt "${#targets[@]}" ]]; then sleep 0.2; fi
done
clear_progress

total=$((SECONDS - start_seconds))
if [[ "${#targets[@]}" -eq 1 ]]; then
  printf 'check: 1 target in %ss\n' "$total"
else
  printf 'check: %s targets in %ss\n' "${#targets[@]}" "$total"
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
