#!/usr/bin/env bash
# Tests for scripts/backport-audit.sh.
#
# A fake `gh` first on PATH records its argv and serves canned JSON, and each
# case runs against a real throwaway git repo whose release/<major>.<minor>
# branch carries (or omits) a cherry-pick of a known `main` commit. Never
# touches this repository. Run directly or `make backport-audit-test`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AUDIT="$SCRIPT_DIR/backport-audit.sh"

pass=0
fail=0
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/backport-audit-fixtures.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }
assert_contains()     { if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1 (missing: $3)"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then bad "$1 (unexpected: $3)"; else ok "$1"; fi; }

# The fake `gh`: gh_argv.txt records one call per line and the canned merged
# (`$GH_MERGED_JSON`) / open (`$GH_OPEN_JSON`) documents are served, honouring
# gh's --jq/-q the way the real CLI does.
fake_bin="$fixture_root/bin"
mkdir -p "$fake_bin"
cat >"$fake_bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${GH_ARGV:?}"
case " $* " in
  *" pr list "*)
    doc_file="$GH_MERGED_JSON"
    case " $* " in
      *" --state open "*) doc_file="$GH_OPEN_JSON" ;;
    esac
    doc="$(cat "$doc_file")"
    query=""
    for ((i = 1; i <= $#; i++)); do
      if [[ "${!i}" == "-q" || "${!i}" == "--jq" ]]; then
        j=$((i + 1))
        query="${!j}"
      fi
    done
    if [[ -n "$query" ]]; then
      printf '%s' "$doc" | jq -r "$query"
    else
      printf '%s' "$doc"
    fi
    exit 0
    ;;
esac
echo "fake gh: unexpected call: $*" >&2
exit 1
GH
chmod +x "$fake_bin/gh"
export PATH="$fake_bin:$PATH"
export GH_ARGV="$fixture_root/argv.txt"
export GH_MERGED_JSON="$fixture_root/merged.json"
export GH_OPEN_JSON="$fixture_root/open.json"
printf '[]\n' >"$GH_OPEN_JSON"

# unset_audit_env clears every knob a case may set, so the defaults are the
# baseline each case departs from.
unset_audit_env() {
  unset BACKPORT_AUDIT_REPO BACKPORT_AUDIT_DEFAULT_BRANCH BACKPORT_AUDIT_LABEL \
    BACKPORT_AUDIT_GRACE_SECONDS BACKPORT_AUDIT_NOW 2>/dev/null || true
}

# seed_fixture NAME builds a repo with one real fix on main, a release/0.1
# branch, and MERGE set to the fix commit — the shape `git log <merge>^1..<merge>`
# derives a PR's commits from.
seed_fixture() {
  REPO="$fixture_root/repo-$1"
  rm -rf "$REPO"
  mkdir -p "$REPO"
  git -C "$REPO" init -q -b main
  git -C "$REPO" config user.email t@t
  git -C "$REPO" config user.name t
  printf 'a\n' >"$REPO/f"; git -C "$REPO" add f; git -C "$REPO" commit -qm base
  git -C "$REPO" branch release/0.1
  printf 'b\n' >"$REPO/g"; git -C "$REPO" add g; git -C "$REPO" commit -qm fix
  MERGE="$(git -C "$REPO" rev-parse HEAD)"
}

# write_merged MERGED_AT writes one labeled #7 PR whose merge commit is MERGE.
write_merged() {
  printf '[{"number":7,"title":"Fix the bug","url":"https://github.com/o/r/pull/7","mergedAt":"%s","mergeCommit":{"oid":"%s"}}]\n' \
    "$1" "$MERGE" >"$GH_MERGED_JSON"
}

run_audit() {
  rm -f "$GH_ARGV"
  export BACKPORT_AUDIT_REPO="$REPO"
  set +e
  out="$("$AUDIT" 2>"$fixture_root/stderr")"
  status=$?
  set -e
}
argv() { [[ -f "$GH_ARGV" ]] && cat "$GH_ARGV" || true; }

# iso_from_epoch EPOCH prints the UTC ISO-8601 timestamp gh reports as mergedAt,
# via whichever `date` spelling the host offers (GNU or BSD).
iso_from_epoch() {
  local iso
  if iso="$(date -u -d "@$1" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"; then printf '%s' "$iso"; return 0; fi
  if iso="$(date -u -r "$1" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"; then printf '%s' "$iso"; return 0; fi
  return 1
}

unset_audit_env

echo "# backport-audit.sh (no release branch)"
seed_fixture no-release
git -C "$REPO" branch -D release/0.1 >/dev/null
write_merged 2020-01-01T00:00:00Z
run_audit
if [[ "$status" -eq 0 ]]; then ok "no release branch exits clean"; else bad "no release branch exits clean (got $status)"; fi
assert_contains "names the missing release branch" "$out" "no release branch; nothing to audit"
if [[ -z "$(argv)" ]]; then ok "does not ask gh without a release branch"; else bad "does not ask gh without a release branch"; fi

echo "# backport-audit.sh (unported PR)"
seed_fixture unported
write_merged 2020-01-01T00:00:00Z
run_audit
if [[ "$status" -eq 1 ]]; then ok "an unported PR exits 1"; else bad "an unported PR exits 1 (got $status)"; fi
assert_contains "reports the PR number" "$out" "#7"
assert_contains "counts the missing patch-ids" "$out" "missing:1"
assert_contains "labels the filter" "$(argv)" "--label needs-backport"
assert_contains "asks for the merge commit" "$(argv)" "--json number,title,url,mergedAt,mergeCommit"

echo "# backport-audit.sh (commits read from the merged range)"
assert_not_contains "never asks the API for the original commits" "$(argv)" "--json commits"

echo "# backport-audit.sh (ported PR)"
seed_fixture ported
git -C "$REPO" checkout -q release/0.1
git -C "$REPO" cherry-pick -x "$MERGE" >/dev/null
write_merged 2020-01-01T00:00:00Z
run_audit
if [[ "$status" -eq 0 ]]; then ok "a ported PR exits clean"; else bad "a ported PR exits clean (got $status)"; fi
assert_not_contains "reports no debt" "$out" "#7"

echo "# backport-audit.sh (merged within grace)"
seed_fixture grace
now=1700000000
write_merged "$(iso_from_epoch $((now - 60)))"
export BACKPORT_AUDIT_NOW="$now"
export BACKPORT_AUDIT_GRACE_SECONDS=7200
run_audit
if [[ "$status" -eq 0 ]]; then ok "a PR merged within the grace window is skipped"; else bad "a PR merged within the grace window is skipped (got $status)"; fi
unset_audit_env

echo "# backport-audit.sh (open backport PR)"
seed_fixture open-pr
printf '[{"number":8,"body":"Backport of #7"}]\n' >"$GH_OPEN_JSON"
write_merged 2020-01-01T00:00:00Z
run_audit
if [[ "$status" -eq 0 ]]; then ok "an open backport PR suppresses the debt"; else bad "an open backport PR suppresses the debt (got $status)"; fi
printf '[]\n' >"$GH_OPEN_JSON"

echo "# backport-audit.sh (unlabeled merged PR)"
seed_fixture unlabeled
printf '[]\n' >"$GH_MERGED_JSON"
run_audit
if [[ "$status" -eq 0 ]]; then ok "no labeled PR exits clean"; else bad "no labeled PR exits clean (got $status)"; fi

echo "# backport-audit.sh (duplicate patch-id)"
seed_fixture duplicate
git -C "$REPO" checkout -q release/0.1
git -C "$REPO" cherry-pick -x "$MERGE" >/dev/null
git -C "$REPO" revert --no-edit HEAD >/dev/null
git -C "$REPO" cherry-pick -x "$MERGE" >/dev/null
write_merged 2020-01-01T00:00:00Z
run_audit
if [[ "$status" -eq 0 ]]; then ok "a duplicated patch-id on the branch is present"; else bad "a duplicated patch-id on the branch is present (got $status)"; fi

echo "# backport-audit.sh (adapted port)"
seed_fixture adapted
git -C "$REPO" checkout -q release/0.1
printf 'c\n' >"$REPO/g"; git -C "$REPO" add g; git -C "$REPO" commit -qm "adapted g"
write_merged 2020-01-01T00:00:00Z
run_audit
if [[ "$status" -eq 1 ]]; then ok "an adapted port is reported as debt"; else bad "an adapted port is reported as debt (got $status)"; fi
assert_contains "names the adapted PR" "$out" "#7"

echo "# backport-audit.sh (usage)"
if "$AUDIT" --help >/dev/null 2>&1; then ok "--help exits 0"; else bad "--help exits 0"; fi
if "$AUDIT" bogus >/dev/null 2>&1; then bad "an unknown argument is a usage error"; else ok "an unknown argument is a usage error"; fi

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
