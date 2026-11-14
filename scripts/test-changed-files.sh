#!/usr/bin/env bash
# Tests for scripts/changed-files.sh. Throwaway repos only.
set -euo pipefail

export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_TERMINAL_PROMPT=0
unset GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT 2>/dev/null || true

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FILES="$SCRIPT_DIR/changed-files.sh"

pass=0; fail=0
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/changed-files.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }

# assert_list <name> <repo> <mode> <expected newline-separated paths> [base]
# Expected and got are compared sorted, so the order git prints them in does not
# matter; the count matters, so a duplicated path is caught.
assert_list() {
  local name="$1" repo="$2" mode="$3" expected="$4" base="${5:-}" got
  if [[ -n "$base" ]]; then
    got="$("$FILES" --repo "$repo" --base "$base" "$mode" 2>/dev/null | LC_ALL=C sort)" || true
  else
    got="$("$FILES" --repo "$repo" "$mode" 2>/dev/null | LC_ALL=C sort)" || true
  fi
  expected="$(printf '%s\n' "$expected" | LC_ALL=C sort)"
  if [[ "$got" == "$expected" ]]; then ok "$name"; else bad "$name (got: $(printf '%s' "$got" | tr '\n' ' '))"; fi
}

new_repo() {
  local proj; proj="$(mktemp -d "$fixture_root/repo.XXXXXX")"
  git init -q "$proj"
  git -C "$proj" config user.email test@example.com
  git -C "$proj" config user.name test
  git -C "$proj" symbolic-ref HEAD refs/heads/main
  mkdir -p "$proj/internal/foo" "$proj/docs" "$proj/changelog.d"
  printf 'package foo\n' >"$proj/internal/foo/foo.go"
  printf '# d\n' >"$proj/docs/guide.md"
  printf '# t\n' >"$proj/README.md"
  git -C "$proj" add -A; git -C "$proj" commit -q -m base
  printf '%s' "$proj"
}

echo "# changed-files.sh: local"
proj="$(new_repo)"
assert_list "clean tree lists nothing" "$proj" local ""

printf 'package foo // x\n' >"$proj/internal/foo/foo.go"
assert_list "modified tracked file" "$proj" local "internal/foo/foo.go"

printf 'package new\n' >"$proj/internal/new.go"
assert_list "modified plus untracked" "$proj" local "internal/foo/foo.go
internal/new.go"

# A deleted tracked file is reported like any other change.
rm "$proj/internal/new.go"
git -C "$proj" checkout -q -- internal/foo/foo.go
rm "$proj/docs/guide.md"
assert_list "deleted tracked file" "$proj" local "docs/guide.md"
git -C "$proj" checkout -q -- docs/guide.md

# A rename is reported at both endpoints (`--no-renames`), not collapsed.
git -C "$proj" mv docs/guide.md internal/guide.md
assert_list "rename lists both endpoints" "$proj" local "docs/guide.md
internal/guide.md"
git -C "$proj" reset -q --hard HEAD

echo "# changed-files.sh: branch"
bproj="$(new_repo)"
# feature diverges from main; main advances after the divergence, so a plain
# `git diff main` would also report main's files and the merge base is required.
git -C "$bproj" checkout -q -b feature
printf 'package foo // f\n' >"$bproj/internal/foo/foo.go"
printf 'package extra\n' >"$bproj/internal/extra.go"
git -C "$bproj" add -A; git -C "$bproj" commit -q -m "feature"
git -C "$bproj" checkout -q main
printf '# t2\n' >"$bproj/README.md"
git -C "$bproj" add -A; git -C "$bproj" commit -q -m "main unreconciled"
git -C "$bproj" checkout -q feature
assert_list "branch uses the merge base" "$bproj" branch "internal/foo/foo.go
internal/extra.go" main

printf 'package untracked\n' >"$bproj/internal/untracked.go"
assert_list "branch includes untracked" "$bproj" branch "internal/foo/foo.go
internal/extra.go
internal/untracked.go" main

# An unresolvable base is a distinct status (3) so the caller can skip rather
# than treat it as an empty list.
if "$FILES" --repo "$bproj" --base no-such-ref branch >/dev/null 2>&1; then
  bad "missing base exits non-zero"
else
  status=0; "$FILES" --repo "$bproj" --base no-such-ref branch >/dev/null 2>&1 || status=$?
  [[ "$status" -eq 3 ]] && ok "missing base exits 3" || bad "missing base exits 3 (got $status)"
fi

echo "# changed-files.sh: usage"
if "$FILES" --repo "$proj" >/dev/null 2>&1; then bad "no mode is an error"; else ok "no mode is an error"; fi
if "$FILES" --repo "$proj" bogus >/dev/null 2>&1; then bad "unknown mode is an error"; else ok "unknown mode is an error"; fi
if "$FILES" --repo "$fixture_root/does-not-exist" local >/dev/null 2>&1; then bad "non-repository is an error"; else ok "non-repository is an error"; fi
if "$FILES" --help >/dev/null 2>&1; then ok "--help exits 0"; else bad "--help exits 0"; fi

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
