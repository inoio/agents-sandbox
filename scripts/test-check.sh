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
if [[ "${STUB_ORDER:-0}" == 1 ]]; then
  printf 'start %s\n' "$target" >>"$STUB_DIR/order"
  sleep 0.3
fi
printf 'stdout %s\n' "$target"
printf 'stderr %s\n' "$target" >&2
status=0
if [[ -f "$STUB_DIR/fail-$target" ]]; then status=1; fi
if [[ "${STUB_ORDER:-0}" == 1 ]]; then
  printf 'end %s\n' "$target" >>"$STUB_DIR/order"
fi
exit "$status"
EOF
chmod +x "$stub"
export STUB_DIR="$tmp"

# runner_run <heavy> <target...> -> RUN_OUT, RUN_ERR, RUN_STATUS
# Callers set RUN_JOBS, RUN_SERIAL, RUN_VERBOSE, RUN_ORDER (all default off).
runner_run() {
  local heavy="$1"; shift
  if RUN_OUT="$(env \
      MAKE="$stub" CHECK_HEAVY_TARGETS="$heavy" \
      JOBS="${RUN_JOBS:-2}" SERIAL="${RUN_SERIAL:-0}" VERBOSE="${RUN_VERBOSE:-0}" \
      STUB_ORDER="${RUN_ORDER:-0}" \
      "$RUNNER" "$@" 2>"$tmp/err")"; then
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
# Number of `start` markers seen before the first `end` in the order file.
starts_before_first_end() { awk '/^end /{print s+0; exit} /^start /{s++}' "$STUB_DIR/order"; }

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

# The inner-loop wiring routes through `check-selected`: it filters the heavy
# set to TARGETS and passes MODE/BASE to the runner. CHECK_RUNNER stubs the
# runner so no real target runs.
record="$tmp/selected"
stub_runner="$tmp/selected-runner"
cat >"$stub_runner" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf 'heavy=[%s] mode=[%s] base=[%s] args=[%s]\n' "\${CHECK_HEAVY_TARGETS:-}" "\${MODE:-}" "\${BASE:-}" "\$*" >"$record"
exit 0
EOF
chmod +x "$stub_runner"
run_selected() {  # <targets> <mode> <base> <heavy>
  : >"$record"
  make -s -C "$SCRIPT_DIR/.." check-selected \
    TARGETS="$1" MODE="$2" BASE="$3" \
    CHECK_RUNNER="$stub_runner" CHECK_HEAVY_TARGETS="$4" >/dev/null 2>&1 || true
  cat "$record" 2>/dev/null || true
}
got="$(run_selected "docs-linkcheck shellcheck" branch origin/main "validate-fmt lint test compile-all")"
if [[ "$got" == "heavy=[] mode=[branch] base=[origin/main] args=[docs-linkcheck shellcheck changelog-presence]" ]]; then
  ok "check-selected filters the heavy set to TARGETS and passes MODE/BASE"
else
  bad "check-selected wiring (got '$got')"
fi
got="$(run_selected "validate-fmt docs-linkcheck" local "" "validate-fmt lint test compile-all")"
if [[ "$got" == "heavy=[validate-fmt] mode=[local] base=[] args=[validate-fmt docs-linkcheck changelog-presence]" ]]; then
  ok "check-selected keeps a selected heavy target"
else
  bad "check-selected keeps a selected heavy target (got '$got')"
fi

# Scheduling: light targets overlap, heavy targets do not, SERIAL forces one at
# a time, VERBOSE streams without capture or status lines.
rm -f "$tmp"/fail-*
RUN_ORDER=1; RUN_JOBS=2; RUN_SERIAL=0; RUN_VERBOSE=0
: >"$STUB_DIR/order"
runner_run "" alpha beta
expect_status "light targets run to completion" 0
got="$(starts_before_first_end)"
if [[ "$got" == 2 ]]; then ok "light targets overlap"; else bad "light targets overlap (starts before first end: $got)"; fi

: >"$STUB_DIR/order"
runner_run "alpha beta" alpha beta
expect_status "heavy targets run to completion" 0
got="$(starts_before_first_end)"
if [[ "$got" == 1 ]]; then ok "heavy targets run one at a time"; else bad "heavy targets run one at a time (starts before first end: $got)"; fi

RUN_SERIAL=1
: >"$STUB_DIR/order"
runner_run "" alpha beta
expect_status "SERIAL run completes" 0
got="$(starts_before_first_end)"
if [[ "$got" == 1 ]]; then ok "SERIAL runs one at a time"; else bad "SERIAL runs one at a time (starts before first end: $got)"; fi
RUN_SERIAL=0

RUN_VERBOSE=1
runner_run "" alpha
expect_status "VERBOSE run completes" 0
expect_contains "VERBOSE streams target output" "stdout alpha" "$RUN_OUT"
expect_absent "VERBOSE prints no status lines" "PASS " "$RUN_OUT"
RUN_VERBOSE=0; RUN_ORDER=0

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
