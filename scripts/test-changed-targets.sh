#!/usr/bin/env bash
# Tests for scripts/changed-targets.sh. Throwaway repos only.
set -euo pipefail

export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_TERMINAL_PROMPT=0
unset GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT 2>/dev/null || true

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAP="$SCRIPT_DIR/changed-targets.sh"

pass=0; fail=0
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/changed-targets.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()     { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad()    { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }

# assert_set <name> <repo> <mode> <expected whitespace-separated targets> [base]
assert_set() {
  local name="$1" repo="$2" mode="$3" expected="$4" base="${5:-}" got
  if [[ -n "$base" ]]; then
    got="$("$MAP" --repo "$repo" --base "$base" "$mode" 2>/dev/null | tr '\n' ' ' | sed 's/ *$//')"
  else
    got="$("$MAP" --repo "$repo" "$mode" 2>/dev/null | tr '\n' ' ' | sed 's/ *$//')"
  fi
  if [[ "$got" == "$expected" ]]; then ok "$name"; else bad "$name (got: '$got' want: '$expected')"; fi
}

# The full `make check` set, read from the Makefile's CHECK_TARGETS so this
# expectation tracks it (the map reads the same line).
MAKEFILE="$(cd "$SCRIPT_DIR/.." && pwd)/Makefile"
ALL="$(grep -E '^CHECK_TARGETS[[:space:]]*=' "$MAKEFILE" | head -1 \
  | sed -E 's/^CHECK_TARGETS[[:space:]]*=[[:space:]]*//' \
  | tr ' ' '\n' | LC_ALL=C sort -u | tr '\n' ' ' | sed 's/ *$//')"
GO_DOCS="compile-all docs-linkcheck lint test validate-fmt"

new_repo() {
  local proj; proj="$(mktemp -d "$fixture_root/repo.XXXXXX")"
  git init -q "$proj"
  git -C "$proj" config user.email test@example.com
  git -C "$proj" config user.name test
  git -C "$proj" symbolic-ref HEAD refs/heads/main
  mkdir -p "$proj/internal/foo" "$proj/cmd/app" "$proj/docs" "$proj/changelog.d" "$proj/ci/deadcodecheck" "$proj/scripts" "$proj/.operator-shared/specs"
  printf 'package foo\n' >"$proj/internal/foo/foo.go"
  printf 'package main\n' >"$proj/cmd/app/main.go"
  printf '# t\n' >"$proj/README.md"
  printf '# d\n' >"$proj/docs/guide.md"
  : >"$proj/Makefile"
  printf '# b\n' >"$proj/.operator-shared/specs/x.md"
  git -C "$proj" add -A; git -C "$proj" commit -q -m base
  printf '%s' "$proj"
}

echo "# changed-targets.sh: local"
proj="$(new_repo)"
assert_set "no changes selects nothing" "$proj" local ""

# local mode reads the uncommitted working tree, so each case is left uncommitted.
run_local() { # <name> <path> <content> <expected>
  local name="$1" path="$2" content="$3" expected="$4"
  mkdir -p "$(dirname "$proj/$path")"
  printf '%s' "$content" >"$proj/$path"
  assert_set "$name" "$proj" local "$expected"
  git -C "$proj" checkout -q -- . 2>/dev/null || true
  git -C "$proj" clean -fdq
}

run_local "modified Go under internal" internal/foo/foo.go "package foo // x" "$GO_DOCS"
run_local "modified Go under cmd unions docs" cmd/app/main.go "package main // x" "$GO_DOCS"
run_local "modified docs page" docs/guide.md "# d2" "docs-linkcheck"
run_local "modified root markdown" README.md "# t2" "docs-linkcheck"
run_local "added changelog fragment" changelog.d/999.changed.md "note" "changelog-check"
run_local "modified docs checker" ci/check-docs.sh "# x" "docs-linkcheck docs-linkcheck-test"
run_local "modified changelog checker" ci/check-changelog.sh "# x" "changelog-check changelog-check-test"
run_local "modified deadcode checker" ci/check-deadcode.sh "# x" "deadcode-test"
run_local "modified upstream-drift script" scripts/upstream-drift.sh "# x" "upstream-drift-test"
run_local "modified rebase-status script" scripts/rebase-status.sh "# x" "rebase-status-test"
run_local "modified envelope test" scripts/test-envelope.sh "# x" "envelope-test"
run_local "modified cleanup script" scripts/cleanup-merged-branches.sh "# x" "cleanup-branches-test"
run_local "modified ci-guards checker" ci/check-ci-guards.sh "# x" "ci-guards-check ci-guards-check-test"
run_local "modified workflow" .github/workflows/ci.yml "# x" "actionlint ci-guards-check"
run_local "nested markdown selects nothing" .github/PULL_REQUEST_TEMPLATE.md "# x" ""
run_local "dev-VM dockerfile selects nothing" .agents-sandbox/Dockerfile "FROM x" ""
run_local "opencode config selects nothing" opencode.jsonc "{}" ""
run_local "unrecognized Makefile selects all" Makefile "all:" "$ALL"
run_local "operator brain selects nothing" .operator-shared/specs/x.md "# b2" ""

# A deleted tracked file is reported by `git diff --name-only HEAD`.
rm "$proj/internal/foo/foo.go"
assert_set "deleted tracked Go" "$proj" local "$GO_DOCS"
git -C "$proj" checkout -q -- internal/foo/foo.go

# An untracked new file is reported by `git ls-files --others`.
mkdir -p "$proj/internal/new"
printf 'package new\n' >"$proj/internal/new/new.go"
assert_set "untracked new Go" "$proj" local "$GO_DOCS"
rm -rf "$proj/internal/new"

# A failing `git ls-files` must surface as a script error, not be swallowed by
# the surrounding printf: otherwise the map returns an empty selection and the
# Makefile skips every check as if nothing had changed.
shim_dir="$(mktemp -d "$fixture_root/shim.XXXXXX")"
real_git="$(command -v git)"
cat >"$shim_dir/git" <<EOF
#!/usr/bin/env bash
for arg in "\$@"; do
  if [[ "\$arg" == "ls-files" ]]; then
    printf 'simulated ls-files failure\n' >&2
    exit 1
  fi
done
exec "$real_git" "\$@"
EOF
chmod +x "$shim_dir/git"
if PATH="$shim_dir:$PATH" "$MAP" --repo "$proj" local >/dev/null 2>&1; then
  bad "failing ls-files is an error"
else
  ok "failing ls-files is an error"
fi

# A rename is reported at both endpoints (`--no-renames`), so the source class
# still selects; plain `git diff` would collapse it to the destination only.
git -C "$proj" mv docs/guide.md .operator-shared/guide.md
assert_set "rename selects the source class" "$proj" local "docs-linkcheck"
git -C "$proj" reset -q --hard HEAD
git -C "$proj" clean -fdq

echo "# changed-targets.sh: branch"
bproj="$(new_repo)"
# feature diverges from main; main advances after the divergence.
git -C "$bproj" checkout -q -b feature
printf '# d3\n' >"$bproj/docs/guide.md"
git -C "$bproj" add -A; git -C "$bproj" commit -q -m "feature docs"
git -C "$bproj" checkout -q main
printf 'all:\n' >"$bproj/Makefile"
git -C "$bproj" add -A; git -C "$bproj" commit -q -m "main unreconciled"
git -C "$bproj" checkout -q feature
assert_set "branch uses the merge base" "$bproj" branch "docs-linkcheck" main
assert_set "branch with missing base selects all" "$bproj" branch "$ALL" no-such-ref

echo "# changed-targets.sh: usage"
if "$MAP" --repo "$proj" >/dev/null 2>&1; then bad "no mode is an error"; else ok "no mode is an error"; fi
if "$MAP" --repo "$proj" bogus >/dev/null 2>&1; then bad "unknown mode is an error"; else ok "unknown mode is an error"; fi
if "$MAP" --repo "$fixture_root/does-not-exist" local >/dev/null 2>&1; then bad "non-repository is an error"; else ok "non-repository is an error"; fi
if "$MAP" --help >/dev/null 2>&1; then ok "--help exits 0"; else bad "--help exits 0"; fi

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
