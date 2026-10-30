#!/usr/bin/env bash
# Tests for ci/check-deadcode.sh.
#
# Builds throwaway fixture trees — a source file plus one deadcode JSON payload
# per build-tag configuration — and asserts the gate's exit status and messages.
# The real tool is never run: DEADCODE_FIXTURE_DIR and DEADCODE_FILES stand in
# for it, so these cases are fast and offline.
#
# Run directly (ci/test-check-deadcode.sh).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-deadcode.sh"

pass=0
fail=0

WORK=""
SOURCE=""
REPORTS=""
FILES=""
DEAD_LINE=""

cleanup() {
  if [[ -n "$WORK" && -d "$WORK" ]]; then
    rm -rf "$WORK"
  fi
  return 0
}
trap cleanup EXIT

# new_fixture <marker-line>
# Creates the source tree every case starts from: one function, either bare or
# documented by the given //deadcode:ignore line. Sets WORK, SOURCE, REPORTS,
# FILES and DEAD_LINE.
new_fixture() {
  cleanup
  local marker="${1:-}"
  WORK="$(mktemp -d)"
  mkdir -p "$WORK/src" "$WORK/reports"
  SOURCE="$WORK/src/a.go"
  REPORTS="$WORK/reports"
  FILES="$WORK/files.txt"
  if [[ -n "$marker" ]]; then
    printf 'package main\n\n%s\nfunc Dead() {}\n' "$marker" >"$SOURCE"
    DEAD_LINE=4
  else
    printf 'package main\n\nfunc Dead() {}\n' >"$SOURCE"
    DEAD_LINE=3
  fi
  printf '%s\n' "$SOURCE" >"$FILES"
}

# write_report <configuration> <line> <function>
write_report() {
  local configuration="$1" line="$2" function="$3"
  printf '[{"Name":"p","Path":"example.com/p","Funcs":[{"Name":"%s","Position":{"File":"%s","Line":%s,"Col":6}}]}]\n' \
    "$function" "$SOURCE" "$line" >"$REPORTS/$configuration.json"
}

# write_empty_report <configuration>
write_empty_report() {
  printf '[]\n' >"$REPORTS/$1.json"
}

# write_all_configurations <line> <function>
write_all_configurations() {
  write_report no-tags "$1" "$2"
  write_report integration "$1" "$2"
  write_report msb "$1" "$2"
  write_report 'integration+msb' "$1" "$2"
}

# expect <name> <expected-exit> <expected-substring> [env assignment...]
expect() {
  local name="$1" want_status="$2" want_msg="$3"
  shift 3
  local out status
  out="$(env DEADCODE_VERSION=fixture DEADCODE_FIXTURE_DIR="$REPORTS" DEADCODE_FILES="$FILES" "$@" \
    bash "$CHECK" 2>&1)" && status=0 || status=$?
  if [[ "$status" != "$want_status" ]]; then
    printf 'FAIL %s: exit %s, want %s\n%s\n' "$name" "$status" "$want_status" "$out"
    fail=$((fail + 1))
    return
  fi
  if [[ -n "$want_msg" && "$out" != *"$want_msg"* ]]; then
    printf 'FAIL %s: output missing %q\n%s\n' "$name" "$want_msg" "$out"
    fail=$((fail + 1))
    return
  fi
  printf 'ok %s\n' "$name"
  pass=$((pass + 1))
}

# A finding covered by a marker in every configuration passes.
new_fixture '//deadcode:ignore reason="test seam"'
write_all_configurations "$DEAD_LINE" Dead
expect "marked finding in every configuration" 0 ""

# A finding only the tool's tagged build reports is still a finding, and the
# message names both the function and the configuration that saw it.
new_fixture
write_empty_report no-tags
write_empty_report msb
write_empty_report 'integration+msb'
write_report integration "$DEAD_LINE" Dead
expect "unmarked finding reported by one configuration" 1 "unreachable func: Dead"
expect "the report names the configuration" 1 "integration"

# A marker that excuses nothing fails, naming its own position.
new_fixture '//deadcode:ignore reason="test seam"'
write_all_configurations 99 Elsewhere
expect "stale marker" 1 "stale"
expect "the stale marker names its line" 1 "$SOURCE:3"

# A payload that is not a report is a hard error, never a pass.
new_fixture
write_all_configurations "$DEAD_LINE" Dead
printf 'not a report\n' >"$REPORTS/no-tags.json"
expect "malformed report payload" 2 ""

# A source list naming a file that does not exist is a hard error too.
new_fixture
write_all_configurations "$DEAD_LINE" Dead
printf '%s\n' "$WORK/src/absent.go" >"$FILES"
expect "missing source file" 2 ""

# A single-configuration run asks for that configuration only.
new_fixture '//deadcode:ignore reason="test seam"'
write_report no-tags "$DEAD_LINE" Dead
expect "single configuration run" 0 "" TAGS=

# The matrix still demands every configuration's payload.
new_fixture '//deadcode:ignore reason="test seam"'
write_report no-tags "$DEAD_LINE" Dead
expect "missing configuration payload" 2 "missing fixture"

# A package-scoped run is an inspection aid: it cannot judge markers elsewhere
# in the module, so it skips the stale pass and says so. With no findings at all,
# the stale marker is the only complaint left to suppress.
new_fixture '//deadcode:ignore reason="test seam"'
write_empty_report no-tags
write_empty_report integration
write_empty_report msb
write_empty_report 'integration+msb'
expect "package-scoped run skips staleness" 0 "skipping the stale-marker pass" PKG=./cmd/...

printf '%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
