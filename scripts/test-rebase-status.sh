#!/usr/bin/env bash
# Tests for scripts/rebase-status.sh.
#
# Builds a throwaway repository and provokes a real conflicting rebase, then
# asserts the status report names the step, the unmerged path, and the file left
# with conflict markers. Never touches the real repository. Run directly or
# `make rebase-status-test`.
set -euo pipefail

export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_TERMINAL_PROMPT=0
unset GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT
while IFS='=' read -r inherited _; do
  case "$inherited" in
    GIT_CONFIG_KEY_*|GIT_CONFIG_VALUE_*) unset "$inherited" ;;
  esac
done < <(env)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATUS="$SCRIPT_DIR/rebase-status.sh"

pass=0
fail=0
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/rebase-fixtures.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }
assert_contains()     { if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1 (missing: $3)"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then bad "$1 (unexpected: $3)"; else ok "$1"; fi; }

proj="$(mktemp -d "$fixture_root/repo.XXXXXX")"
git init -q "$proj"
git -C "$proj" config user.email test@example.com
git -C "$proj" config user.name test
git -C "$proj" symbolic-ref HEAD refs/heads/main
printf 'one\ntwo\n' >"$proj/conflict.txt"
git -C "$proj" add -A; git -C "$proj" commit -q -m base
git -C "$proj" branch feature
# main rewrites the first line; feature rewrites it differently.
printf 'main-line\ntwo\n' >"$proj/conflict.txt"; git -C "$proj" commit -q -am main
git -C "$proj" checkout -q feature
# feature has a clean commit (clean.txt) then a conflicting one (conflict.txt):
# the rebase applies the first, advancing HEAD to its rewritten commit, then
# stops on the second.
printf 'feature-only\n' >"$proj/clean.txt"
git -C "$proj" add -A; git -C "$proj" commit -q -m feature-clean
printf 'feature-line\ntwo\n' >"$proj/conflict.txt"; git -C "$proj" commit -q -am feature

echo "# rebase-status.sh (clean)"
out="$("$STATUS" --repo "$proj")"
assert_contains "reports no rebase when clean" "$out" "no rebase in progress"
assert_contains "reports zero unmerged" "$out" "unmerged paths: 0"
assert_contains "reports zero markers" "$out" "files with conflict markers: 0"

echo "# rebase-status.sh (conflicted rebase)"
git -C "$proj" rebase main >/dev/null 2>&1 || true
main_tip="$(git -C "$proj" rev-parse --short main)"
out="$("$STATUS" --repo "$proj")"
assert_contains "reports a rebase in progress" "$out" "rebase in progress"
assert_not_contains "does not report no rebase" "$out" "no rebase in progress"
assert_contains "reports the step counter" "$out" "step 2/2"
assert_contains "reports the rebase target, not HEAD" "$out" "onto $main_tip"
assert_contains "counts the unmerged path" "$out" "unmerged paths: 1"
assert_contains "names the unmerged file" "$out" "conflict.txt"
assert_contains "counts the marker file" "$out" "files with conflict markers: 1"

git -C "$proj" rebase --abort >/dev/null 2>&1 || true
out="$("$STATUS" --repo "$proj")"
assert_contains "clean again after abort" "$out" "no rebase in progress"

if "$STATUS" --repo "$fixture_root/does-not-exist" >/dev/null 2>&1; then
  bad "missing repo is an error"
else ok "missing repo is an error"; fi
if "$STATUS" --help >/dev/null 2>&1; then ok "--help exits 0"; else bad "--help exits 0"; fi

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
