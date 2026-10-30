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

# Several entries of the same type from one PR carry an ordinal suffix.
d="$(new_fixture 103.added-1.md 103.added-2.md 103.changed-1.md)"
expect "valid ordinal names" 0 "" "$d"
rm -rf "$d"

# An ordinal that is not a number is reported.
d="$(new_fixture 103.changed-x.md)"
expect "non-numeric ordinal" 1 "does not match" "$d"
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

# expect_presence <name> <want-status> <want-substring> <fixture-dir> <changed path...>
# Writes the changed paths (as `git diff --name-only` emits them) to a file and
# runs the checker's presence mode.
expect_presence() {
  local name="$1" want_status="$2" want_msg="$3" dir="$4"
  shift 4
  local changed out status
  changed="$(mktemp)"
  printf '%s\n' "$@" >"$changed"
  out="$(bash "$CHECK" "$dir" --changed "$changed" 2>&1)" && status=0 || status=$?
  rm -f "$changed"
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

# A code change with no fragment is rejected.
d="$(new_fixture)"
expect_presence "code, no fragment" 1 "no changelog.d" "$d" "internal/foo/foo.go"
rm -rf "$d"

# A code change with a fragment is accepted.
d="$(new_fixture 432.added.md)"
expect_presence "code, with fragment" 0 "" "$d" "internal/foo/foo.go" "changelog.d/432.added.md"
rm -rf "$d"

# Docs-only, markdown-only, and gate-file-only changes need no fragment.
d="$(new_fixture)"
expect_presence "docs only" 0 "" "$d" "docs/commands.md"
rm -rf "$d"

d="$(new_fixture)"
expect_presence "markdown only" 0 "" "$d" "README.md"
rm -rf "$d"

d="$(new_fixture)"
expect_presence "gitignore only" 0 "" "$d" ".gitignore"
rm -rf "$d"

# The release PR (CHANGELOG.md plus fragment deletion, all .md) needs no fragment.
d="$(new_fixture)"
expect_presence "release PR, all markdown" 0 "" "$d" "CHANGELOG.md" "changelog.d/432.added.md"
rm -rf "$d"

# A workflow/script change is code and needs a fragment.
d="$(new_fixture)"
expect_presence "workflow change" 1 "no changelog.d" "$d" ".github/workflows/ci.yml"
rm -rf "$d"

# An ignored file in changelog.d/ is not a fragment: editing only the README does
# not satisfy the presence rule.
d="$(new_fixture README.md)"
expect_presence "code, only changelog README" 1 "no changelog.d" "$d" "internal/foo/foo.go" "changelog.d/README.md"
rm -rf "$d"

# A fragment nested in a subdirectory is not a top-level fragment either.
d="$(new_fixture)"
mkdir -p "$d/changelog.d/sub"
printf -- '- x\n' >"$d/changelog.d/sub/432.added.md"
expect_presence "code, only nested fragment" 1 "no changelog.d" "$d" "internal/foo/foo.go" "changelog.d/sub/432.added.md"
rm -rf "$d"

# A test-only changeset needs no fragment: tests are not user-visible behavior.
d="$(new_fixture)"
expect_presence "test-only change" 0 "" "$d" "internal/foo/foo_test.go" "internal/foo/foo_internal_test.go"
rm -rf "$d"

# A test file alongside production code still needs a fragment.
d="$(new_fixture)"
expect_presence "code plus test" 1 "no changelog.d" "$d" "internal/foo/foo.go" "internal/foo/foo_test.go"
rm -rf "$d"

# A deleted fragment does not satisfy the rule: the path is in the changeset but
# not in the tree.
d="$(new_fixture)"
expect_presence "code, fragment only deleted" 1 "no changelog.d" "$d" "internal/foo/foo.go" "changelog.d/432.added.md"
rm -rf "$d"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
