#!/usr/bin/env bash
# Tests for scripts/open-backport-pr.sh.
#
# A fake `gh` first on PATH records its argv and serves canned JSON, and each
# case runs against a real throwaway git repo with a bare `origin` (a throwaway
# directory) so `git push` lands somewhere without a network. Never touches this
# repository. Run directly or `make open-backport-pr-test`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OPEN="$SCRIPT_DIR/open-backport-pr.sh"

pass=0
fail=0
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/open-backport-pr-fixtures.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }
assert_contains()     { if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1 (missing: $3)"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then bad "$1 (unexpected: $3)"; else ok "$1"; fi; }
assert_eq()           { if [[ "$2" == "$3" ]]; then ok "$1"; else bad "$1 (got '$2' want '$3')"; fi; }

# The fake `gh`: gh_argv.txt records one call per line and `pr view` serves
# $GH_MERGE_OID while `pr create` echoes $GH_PR_URL (as the real CLI does).
fake_bin="$fixture_root/bin"
mkdir -p "$fake_bin"
cat >"$fake_bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${GH_ARGV:?}"
case " $* " in
  *" pr view "*)
    printf '%s\n' "${GH_MERGE_OID-}"
    exit 0
    ;;
  *" pr create "*)
    printf '%s\n' "${GH_PR_URL:?}"
    exit 0
    ;;
esac
echo "fake gh: unexpected call: $*" >&2
exit 1
GH
chmod +x "$fake_bin/gh"
export PATH="$fake_bin:$PATH"
export GH_ARGV="$fixture_root/argv.txt"
export GH_PR_URL="https://github.com/o/r/pull/99"

# unset_backport_env clears every knob a case may set, so the defaults are the
# baseline each case departs from.
unset_backport_env() {
  unset BACKPORT_REPO BACKPORT_RELEASE_BRANCH BACKPORT_BRANCH_PREFIX 2>/dev/null || true
}

# seed_fixture NAME builds a repo with a bare origin, a base on main, a
# release/0.1 branch, and one fix commit on main; MERGE is that fix commit and
# REPO/ORIGIN are the working and bare directories.
seed_fixture() {
  REPO="$fixture_root/repo-$1"
  ORIGIN="$fixture_root/origin-$1.git"
  rm -rf "$REPO" "$ORIGIN"
  mkdir -p "$REPO"
  git init -q --bare "$ORIGIN"
  git -C "$REPO" init -q -b main
  git -C "$REPO" config user.email t@t
  git -C "$REPO" config user.name t
  printf 'a\n' >"$REPO/f"; git -C "$REPO" add f; git -C "$REPO" commit -qm base
  git -C "$REPO" branch release/0.1
  printf 'b\n' >"$REPO/f"; git -C "$REPO" commit -qam fix
  MERGE="$(git -C "$REPO" rev-parse HEAD)"
  git -C "$REPO" remote add origin "$ORIGIN"
  git -C "$REPO" push -q origin main release/0.1
}

# seed_two_commit_fixture NAME builds a repo whose #7 has two commits: a topic
# branch carrying FIRST_SHA then SECOND_SHA, merged into main with a merge
# commit MERGE, so `git log <merge>^1..<merge>` yields both. release/0.1 sits on
# the base commit, below both.
seed_two_commit_fixture() {
  REPO="$fixture_root/repo-$1"
  ORIGIN="$fixture_root/origin-$1.git"
  rm -rf "$REPO" "$ORIGIN"
  mkdir -p "$REPO"
  git init -q --bare "$ORIGIN"
  git -C "$REPO" init -q -b main
  git -C "$REPO" config user.email t@t
  git -C "$REPO" config user.name t
  printf 'a\n' >"$REPO/f"; git -C "$REPO" add f; git -C "$REPO" commit -qm base
  git -C "$REPO" branch release/0.1
  git -C "$REPO" checkout -q -b topic
  printf 'b\n' >"$REPO/f"; git -C "$REPO" commit -qam first
  printf 'c\n' >"$REPO/g"; git -C "$REPO" add g; git -C "$REPO" commit -qm second
  FIRST_SHA="$(git -C "$REPO" rev-parse HEAD^)"
  SECOND_SHA="$(git -C "$REPO" rev-parse HEAD)"
  git -C "$REPO" checkout -q main
  git -C "$REPO" merge -q --no-ff -m "Merge pull request #7" topic
  MERGE="$(git -C "$REPO" rev-parse HEAD)"
  git -C "$REPO" remote add origin "$ORIGIN"
  git -C "$REPO" push -q origin main release/0.1
}

# seed_empty_commit_fixture NAME builds a repo whose #7 has three commits on one
# topic branch: a real change, an empty commit, then another real change; MERGE
# is the merge commit, so `git log <merge>^1..<merge>` yields all three.
seed_empty_commit_fixture() {
  REPO="$fixture_root/repo-$1"
  ORIGIN="$fixture_root/origin-$1.git"
  rm -rf "$REPO" "$ORIGIN"
  mkdir -p "$REPO"
  git init -q --bare "$ORIGIN"
  git -C "$REPO" init -q -b main
  git -C "$REPO" config user.email t@t
  git -C "$REPO" config user.name t
  printf 'a\n' >"$REPO/f"; git -C "$REPO" add f; git -C "$REPO" commit -qm base
  git -C "$REPO" branch release/0.1
  git -C "$REPO" checkout -q -b topic
  printf 'b\n' >"$REPO/f"; git -C "$REPO" commit -qam first
  git -C "$REPO" commit -q --allow-empty -m "empty"
  printf 'c\n' >"$REPO/g"; git -C "$REPO" add g; git -C "$REPO" commit -qm second
  FIRST_SHA="$(git -C "$REPO" rev-parse HEAD~2)"
  SECOND_SHA="$(git -C "$REPO" rev-parse HEAD)"
  git -C "$REPO" checkout -q main
  git -C "$REPO" merge -q --no-ff -m "Merge pull request #7" topic
  MERGE="$(git -C "$REPO" rev-parse HEAD)"
  git -C "$REPO" remote add origin "$ORIGIN"
  git -C "$REPO" push -q origin main release/0.1
}

# run_open PR [MERGE_OID] runs the script; MERGE_OID overrides what the fake gh
# serves for `pr view` (an empty value models an unknown or unmerged PR).
run_open() {
  rm -f "$GH_ARGV"
  export BACKPORT_REPO="$REPO"
  export GH_MERGE_OID="${2-$MERGE}"
  set +e
  out="$("$OPEN" "$1" 2>"$fixture_root/stderr")"
  status=$?
  set -e
}
argv() { [[ -f "$GH_ARGV" ]] && cat "$GH_ARGV" || true; }
stderr() { cat "$fixture_root/stderr" 2>/dev/null || true; }

unset_backport_env

echo "# open-backport-pr.sh (clean port)"
seed_fixture clean
run_open 7
assert_eq "a clean port exits 0" "$status" "0"
assert_contains "prints the PR url" "$out" "$GH_PR_URL"
assert_contains "targets the release branch" "$(argv)" "--base release/0.1"
assert_contains "heads the backport branch" "$(argv)" "--head backport/7-0.1"
assert_contains "titles the backport" "$(argv)" "Backport #7 to 0.1"
assert_contains "references the source PR" "$(argv)" "Backport of #7."
assert_contains "lists the source commit subject" "$(argv)" "fix"
if git -C "$REPO" show-ref --verify --quiet refs/heads/backport/7-0.1; then
  ok "creates the backport branch"
else
  bad "creates the backport branch"
fi
assert_eq "carries the fix onto the branch" "$(git -C "$REPO" show backport/7-0.1:f)" "b"
refs="$(git -C "$REPO" ls-remote origin)"
assert_contains "pushes the backport branch" "$refs" "refs/heads/backport/7-0.1"
assert_contains "leaves the release branch on origin" "$refs" "refs/heads/release/0.1"

echo "# open-backport-pr.sh (conflict aborts without pushing)"
seed_fixture conflict
git -C "$REPO" checkout -q release/0.1
printf 'c\n' >"$REPO/f"; git -C "$REPO" add f; git -C "$REPO" commit -qm "release-side change"
git -C "$REPO" push -q origin release/0.1
git -C "$REPO" checkout -q main
before="$(git -C "$REPO" ls-remote origin)"
run_open 7
assert_eq "a conflict exits 1" "$status" "1"
assert_contains "explains the conflict" "$(stderr)" "conflict"
assert_eq "leaves origin unchanged" "$(git -C "$REPO" ls-remote origin)" "$before"
assert_not_contains "opens no PR" "$(argv)" "pr create"

echo "# open-backport-pr.sh (partially ported)"
seed_two_commit_fixture partial
git -C "$REPO" checkout -q release/0.1
git -C "$REPO" cherry-pick -x "$FIRST_SHA" >/dev/null
git -C "$REPO" checkout -q main
git -C "$REPO" push -q origin release/0.1
run_open 7
assert_eq "a partially ported PR still exits 0" "$status" "0"
assert_contains "pushes the backport branch" "$(git -C "$REPO" ls-remote origin)" "refs/heads/backport/7-0.1"
assert_eq "ports only the missing commit" \
  "$(git -C "$REPO" rev-list --count origin/release/0.1..origin/backport/7-0.1)" "1"
pushed_log="$(git -C "$REPO" log --format=%B origin/release/0.1..origin/backport/7-0.1)"
assert_contains "ports the second commit" "$pushed_log" "second"
assert_contains "carries the -x trailer for the ported commit" \
  "$pushed_log" "(cherry picked from commit $SECOND_SHA)"
assert_not_contains "does not replay the already-present commit" \
  "$pushed_log" "(cherry picked from commit $FIRST_SHA)"
assert_contains "opens the PR" "$(argv)" "pr create"

echo "# open-backport-pr.sh (empty commit)"
seed_empty_commit_fixture empty
run_open 7
assert_eq "a PR containing an empty commit exits 0" "$status" "0"
assert_contains "skips the empty pick" "$out" "nothing to port"
assert_contains "opens the PR" "$(argv)" "pr create"
assert_eq "ports the two real commits" \
  "$(git -C "$REPO" rev-list --count origin/release/0.1..origin/backport/7-0.1)" "2"
pushed_log="$(git -C "$REPO" log --format=%B origin/release/0.1..origin/backport/7-0.1)"
assert_contains "ports the first real commit" "$pushed_log" "(cherry picked from commit $FIRST_SHA)"
assert_contains "ports the second real commit" "$pushed_log" "(cherry picked from commit $SECOND_SHA)"
assert_not_contains "never replays the empty commit" "$pushed_log" "empty"

echo "# open-backport-pr.sh (change already present in another form)"
seed_fixture applied-elsewhere
git -C "$REPO" checkout -q release/0.1
printf 'b\n' >"$REPO/f"; printf 'h\n' >"$REPO/h"
git -C "$REPO" add f h; git -C "$REPO" commit -qm "release-side equivalent"
git -C "$REPO" push -q origin release/0.1
git -C "$REPO" checkout -q main
before="$(git -C "$REPO" ls-remote origin)"
run_open 7
assert_eq "a pick that becomes empty exits 3 (nothing to do)" "$status" "3"
assert_contains "says there is nothing to do" "$out" "already fully ported onto release/0.1"
assert_eq "pushes nothing for an empty pick" "$(git -C "$REPO" ls-remote origin)" "$before"
assert_not_contains "opens no empty PR" "$(argv)" "pr create"
if git -C "$REPO" show-ref --verify --quiet refs/heads/backport/7-0.1; then
  bad "deletes the otherwise-empty backport branch"
else
  ok "deletes the otherwise-empty backport branch"
fi

echo "# open-backport-pr.sh (fully ported)"
seed_two_commit_fixture fully-ported
git -C "$REPO" checkout -q release/0.1
git -C "$REPO" cherry-pick -x "$FIRST_SHA" >/dev/null
git -C "$REPO" cherry-pick -x "$SECOND_SHA" >/dev/null
git -C "$REPO" checkout -q main
git -C "$REPO" push -q origin release/0.1
before="$(git -C "$REPO" ls-remote origin)"
run_open 7
assert_eq "a fully ported PR exits 3 (nothing to do)" "$status" "3"
assert_contains "says there is nothing to do" "$out" "already fully ported onto release/0.1"
assert_eq "pushes nothing" "$(git -C "$REPO" ls-remote origin)" "$before"
assert_not_contains "opens no PR for a fully ported PR" "$(argv)" "pr create"
if git -C "$REPO" show-ref --verify --quiet refs/heads/backport/7-0.1; then
  bad "creates no backport branch for a fully ported PR"
else
  ok "creates no backport branch for a fully ported PR"
fi

echo "# open-backport-pr.sh (no release branch)"
seed_fixture no-release
git -C "$REPO" branch -D release/0.1 >/dev/null
git -C "$REPO" push -q origin --delete release/0.1
git -C "$REPO" fetch -q --prune origin
run_open 7
assert_eq "no release branch exits 1" "$status" "1"
assert_not_contains "opens no PR without a release branch" "$(argv)" "pr create"

echo "# open-backport-pr.sh (unknown PR)"
seed_fixture unknown
run_open 999 ""
assert_eq "an unknown PR exits 1" "$status" "1"
assert_not_contains "opens no PR for an unknown PR" "$(argv)" "pr create"

echo "# open-backport-pr.sh (usage)"
if "$OPEN" --help >/dev/null 2>&1; then ok "--help exits 0"; else bad "--help exits 0"; fi
set +e
"$OPEN" >/dev/null 2>&1; assert_eq "no argument is a usage error" "$?" "2"
"$OPEN" abc >/dev/null 2>&1; assert_eq "a non-numeric argument is a usage error" "$?" "2"
set -e

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
