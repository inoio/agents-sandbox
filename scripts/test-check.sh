#!/usr/bin/env bash
# Tests for scripts/check.sh. Fixture-only: a stub `make` and no real targets,
# so this never re-enters `make check`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNNER="$SCRIPT_DIR/check.sh"

pass=0
fail=0
ok()  { pass=$((pass + 1)); if [[ "${VERBOSE:-0}" == 1 ]]; then printf 'ok   - %s\n' "$1"; fi; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }

tmp="$(mktemp -d "${TMPDIR:-/tmp}/check-runner.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

# A stub make: echoes a marker to stdout and stderr, then exits per a control
# file ($STUB_DIR/fail-<target> present => exit 1). Anything before the target
# name is a flag; the last argument is the target.
stub="$tmp/make"
cat >"$stub" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
target=""
for arg in "$@"; do target="$arg"; done
printf 'stdout %s\n' "$target"
printf 'stderr %s\n' "$target" >&2
if [[ -f "$STUB_DIR/fail-$target" ]]; then exit 1; fi
exit 0
EOF
chmod +x "$stub"
export STUB_DIR="$tmp"

# runner_run <heavy> <target...> -> RUN_OUT, RUN_ERR, RUN_STATUS
runner_run() {
  local heavy="$1"; shift
  if RUN_OUT="$(MAKE="$stub" CHECK_HEAVY_TARGETS="$heavy" JOBS=2 "$RUNNER" "$@" 2>"$tmp/err")"; then
    RUN_STATUS=0
  else
    RUN_STATUS=$?
  fi
  RUN_ERR="$(cat "$tmp/err")"
}

expect_status() {
  if [[ "$RUN_STATUS" == "$2" ]]; then
    ok "$1"
  else
    bad "$1 (status $RUN_STATUS, want $2)"
  fi
}
expect_contains() {
  if [[ "$3" == *"$2"* ]]; then
    ok "$1"
  else
    bad "$1 (missing '$2')"
  fi
}
expect_absent() {
  if [[ "$3" != *"$2"* ]]; then
    ok "$1"
  else
    bad "$1 (unexpected '$2')"
  fi
}
line_of()        { printf '%s\n' "$2" | grep -nF -- "$1" | head -1 | cut -d: -f1 || true; }

rm -f "$tmp"/fail-*

# All pass: two targets, one heavy. Success output is only the summary lines.
runner_run "alpha" alpha beta
expect_status "all pass exits 0" 0
expect_contains "all pass prints PASS alpha" "PASS alpha" "$RUN_OUT"
expect_contains "all pass prints PASS beta" "PASS beta" "$RUN_OUT"
expect_absent "all pass hides captured stdout" "stdout alpha" "$RUN_OUT"
expect_absent "all pass has no failure banner" "--- FAIL" "$RUN_OUT"

# A failing light target: FAIL line, banner, captured log, and the log lands
# after every PASS line.
touch "$tmp/fail-beta"
runner_run "alpha" alpha beta
expect_status "failing light target exits 1" 1
expect_contains "failing light target prints FAIL beta" "FAIL beta" "$RUN_OUT"
expect_contains "failing light target prints its banner" "--- FAIL beta ---" "$RUN_OUT"
expect_contains "failing light target dumps its stderr" "stderr beta" "$RUN_OUT"
beta_banner="$(line_of "--- FAIL beta ---" "$RUN_OUT")"
alpha_pass="$(line_of "PASS alpha" "$RUN_OUT")"
if [[ -n "$beta_banner" && -n "$alpha_pass" && "$alpha_pass" -lt "$beta_banner" ]]; then
  ok "failure log follows the PASS lines"
else
  bad "failure log follows the PASS lines (pass=$alpha_pass banner=$beta_banner)"
fi
rm -f "$tmp/fail-beta"

# A failing heavy target behaves the same.
touch "$tmp/fail-alpha"
runner_run "alpha" alpha beta
expect_status "failing heavy target exits 1" 1
expect_contains "failing heavy target prints FAIL alpha" "FAIL alpha" "$RUN_OUT"
expect_contains "failing heavy target prints its banner" "--- FAIL alpha ---" "$RUN_OUT"
rm -f "$tmp/fail-alpha"

# A heavy target absent from the target list is a configuration error.
runner_run "gamma" alpha beta
expect_status "unknown heavy target exits 2" 2
expect_contains "unknown heavy target names it" "gamma" "$RUN_ERR"

# --help prints usage and succeeds.
if "$RUNNER" --help >"$tmp/help" 2>&1; then help_status=0; else help_status=$?; fi
if [[ "$help_status" == 0 ]]; then ok "--help exits 0"; else bad "--help exits 0 (status $help_status)"; fi
expect_contains "--help prints usage" "Usage" "$(cat "$tmp/help")"

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
