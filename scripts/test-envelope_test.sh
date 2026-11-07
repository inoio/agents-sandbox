#!/usr/bin/env bash
# Tests for scripts/test-envelope.sh.
#
# Builds throwaway fixture modules and asserts the envelope's properties: it
# runs the command in a copy with a scrubbed environment, leaves the source
# tree untouched, drops host-only variables, defeats the test-result cache,
# and honors --print. Never touches the real repository. Run directly or
# `make envelope-test`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENVELOPE="$SCRIPT_DIR/test-envelope.sh"

pass=0
fail=0
# Fixtures live under one parent-owned root so a single EXIT trap can remove
# them; new_fixture runs in a command substitution and cannot register paths
# in an array the parent trap would see.
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/envelope-fixtures.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }

assert_contains()     { if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1 (missing: $3)"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then bad "$1 (unexpected: $3)"; else ok "$1"; fi; }

new_fixture() { # name -> prints the fixture dir
  local name="$1"
  local dir="$fixture_root/$name"
  mkdir -p "$dir"
  cat >"$dir/Makefile" <<'EOF'
test:
	@sh check.sh
EOF
  cat >"$dir/check.sh" <<'EOF'
test -n "${HOME:-}" && echo "HOME=$HOME"
echo "PWD=$PWD"
echo "TMPDIR=${TMPDIR:-}"
echo "GOFLAGS=${GOFLAGS:-}"
echo "HOST_ONLY_VAR=${HOST_ONLY_VAR:-}"
: > marker
echo "MARKER"
EOF
  printf '%s\n' "$dir"
}

# Case: runs in a copy with a scrubbed environment.
fixture="$(new_fixture base)"
work="$fixture_root/work"
output="$(HOST_ONLY_VAR=leak "$ENVELOPE" --source "$fixture" --work-dir "$work" -- make test)" && rc=0 || rc=$?
[[ "$rc" -eq 0 ]] && ok "scrubbed run exits 0" || bad "scrubbed run exits 0 (rc=$rc)"
assert_contains     "runs in the work dir"     "$output" "PWD=$work"
assert_contains     "HOME points in work dir"  "$output" "HOME=$work/home"
assert_contains     "TMPDIR points in work dir" "$output" "TMPDIR=$work/tmp"
assert_not_contains "host-only var dropped"    "$output" "leak"

# Case: the source tree is untouched by the run.
leftovers="$(find "$fixture" -mindepth 1 -maxdepth 1 -not \( -name Makefile -o -name check.sh \) -print)"
[[ -z "$leftovers" ]] && ok "source tree untouched" || bad "source tree untouched (left: $leftovers)"

# Case: no test-result cache can stand in for a real run.
assert_contains "GOFLAGS pins -count=1" "$output" "GOFLAGS=-count=1"

# Case: --print reports the plan and runs nothing.
printed="$("$ENVELOPE" --source "$fixture" --print)" && rc=0 || rc=$?
assert_contains     "print shows the command" "$printed" "command: make test"
assert_not_contains "print does not run"      "$printed" "MARKER"

# Case: a source path with a space survives quoting.
spaced="$(new_fixture "with space")"
spaced_work="$fixture_root/space work"
output="$("$ENVELOPE" --source "$spaced" --work-dir "$spaced_work" -- make test)" && rc=0 || rc=$?
[[ "$rc" -eq 0 ]] && ok "spaced source exits 0" || bad "spaced source exits 0 (rc=$rc)"
assert_contains "spaced source runs in work dir" "$output" "PWD=$spaced_work"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
