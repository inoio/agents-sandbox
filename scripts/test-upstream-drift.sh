#!/usr/bin/env bash
# Tests for scripts/upstream-drift.sh.
#
# Builds throwaway repositories (a `main` that advances, a `feature` branch that
# diverges from an earlier `main`) and asserts the drift report. Never touches
# the real repository. Run directly or `make upstream-drift-test`.
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
DRIFT="$SCRIPT_DIR/upstream-drift.sh"

pass=0
fail=0
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/drift-fixtures.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }
assert_contains()     { if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1 (missing: $3)"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then bad "$1 (unexpected: $3)"; else ok "$1"; fi; }

# new_repo -> prints the project path; `feature` diverges from an older main.
new_repo() {
  local proj; proj="$(mktemp -d "$fixture_root/repo.XXXXXX")"
  git init -q "$proj"
  git -C "$proj" config user.email test@example.com
  git -C "$proj" config user.name test
  git -C "$proj" symbolic-ref HEAD refs/heads/main
  printf 'a\n' >"$proj/fileA"; printf 'b\n' >"$proj/fileB"; printf 'c\n' >"$proj/fileC"
  git -C "$proj" add -A; git -C "$proj" commit -q -m base
  git -C "$proj" branch feature
  # main advances, touching fileA and fileB.
  printf 'a-main\n' >"$proj/fileA"; git -C "$proj" commit -q -am main-a
  printf 'b-main\n' >"$proj/fileB"; git -C "$proj" commit -q -am main-b
  # feature diverges from base, touching fileA (overlap) and fileC (only here).
  git -C "$proj" checkout -q feature
  printf 'a-feature\n' >"$proj/fileA"; git -C "$proj" commit -q -am feat-a
  printf 'c-feature\n' >"$proj/fileC"; git -C "$proj" commit -q -am feat-c
  printf '%s' "$proj"
}

echo "# upstream-drift.sh"
proj="$(new_repo)"
out="$("$DRIFT" --repo "$proj" --base main --no-fetch)"
assert_contains "counts upstream commits" "$out" "upstream commits since merge-base: 2"
assert_contains "counts branch commits" "$out" "branch commits since merge-base:   2"
assert_contains "finds the overlap count" "$out" "files changed on both sides:       1"
assert_contains "names the conflict site" "$out" "  fileA"
assert_not_contains "does not list a branch-only file as overlap" "$out" "  fileC"
assert_contains "lists upstream commits" "$out" "main-a"

# A branch that touched nothing upstream did → no overlap.
git -C "$proj" checkout -q -b disjoint main
printf 'z\n' >"$proj/fileZ"; git -C "$proj" add -A; git -C "$proj" commit -q -m feat-z
out="$("$DRIFT" --repo "$proj" --base main --no-fetch)"
assert_contains "reports a clean rebase" "$out" "No file changed on both sides"

# --quiet suppresses the per-commit sections.
out="$("$DRIFT" --repo "$proj" --base main --no-fetch)" 2>/dev/null || true
git -C "$proj" checkout -q feature
out="$("$DRIFT" --repo "$proj" --base main --no-fetch --quiet)"
assert_not_contains "--quiet omits the upstream commit list" "$out" "Upstream commits:"

# Usage errors.
if "$DRIFT" --repo "$fixture_root/does-not-exist" --no-fetch >/dev/null 2>&1; then
  bad "missing repo is an error"
else ok "missing repo is an error"; fi
if "$DRIFT" --help >/dev/null 2>&1; then ok "--help exits 0"; else bad "--help exits 0"; fi

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
