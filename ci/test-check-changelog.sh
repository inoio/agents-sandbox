#!/usr/bin/env bash
# Tests for ci/check-changelog.sh.
#
# Builds throwaway fixture trees and asserts the checker's exit status and
# messages. Run directly or via `make changelog-check-test`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-changelog.sh"

pass=0
fail=0

# new_fixture [name.type.md ...]: a temp root whose changelog.d/ holds the named
# files, each with one line of placeholder content.
new_fixture() {
  local dir f
  dir="$(mktemp -d)"
  mkdir -p "$dir/changelog.d"
  for f in "$@"; do printf -- '- x\n' >"$dir/changelog.d/$f"; done
  echo "$dir"
}

# expect <name> <want-status> <want-substring> <fixture-dir>
expect() {
  local name="$1" want_status="$2" want_msg="$3" dir="$4"
  local out status
  out="$(bash "$CHECK" "$dir" 2>&1)" && status=0 || status=$?
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
  printf 'ok   %s\n' "$name"
  pass=$((pass + 1))
}

# Valid names pass.
d="$(new_fixture 432.added.md 431.fixed.md)"
expect "valid names" 0 "" "$d"
rm -rf "$d"

# An unknown type is reported, not silently accepted.
d="$(new_fixture 432.feature.md)"
expect "unknown type" 1 "unknown type" "$d"
rm -rf "$d"

# A missing type is reported.
d="$(new_fixture 432.md)"
expect "missing type" 1 "does not match" "$d"
rm -rf "$d"

# A non-.md fragment is reported.
d="$(new_fixture 432.added.txt)"
expect "wrong suffix" 1 "does not match" "$d"
rm -rf "$d"

# A directory with no fragments is fine.
d="$(new_fixture)"
expect "empty directory" 0 "" "$d"
rm -rf "$d"

# A missing changelog.d/ is not an error.
d="$(mktemp -d)"
expect "absent directory" 0 "" "$d"
rm -rf "$d"

# The ignored names are skipped.
d="$(new_fixture README.md .gitkeep)"
expect "ignored names" 0 "" "$d"
rm -rf "$d"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
