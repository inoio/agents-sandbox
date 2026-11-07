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
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"

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

netns_capable() {
  [[ "$(uname -s)" == "Linux" ]] && command -v unshare >/dev/null 2>&1 && command -v ip >/dev/null 2>&1
}

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

# Case: runs in a copy with a scrubbed environment. GOFLAGS is cleared so the
# exact `GOFLAGS=-count=1` assertion below does not depend on the caller's env.
fixture="$(new_fixture base)"
work="$fixture_root/work"
output="$(env -u GOFLAGS HOST_ONLY_VAR=leak "$ENVELOPE" --source "$fixture" --work-dir "$work" -- make test)" && rc=0 || rc=$?
[[ "$rc" -eq 0 ]] && ok "scrubbed run exits 0" || bad "scrubbed run exits 0 (rc=$rc)"
assert_contains     "runs in the work dir"     "$output" "PWD=$work/module"
assert_contains     "HOME points in work dir"  "$output" "HOME=$work/home"
assert_contains     "TMPDIR points in work dir" "$output" "TMPDIR=$work/tmp"
assert_not_contains "host-only var dropped"    "$output" "leak"

# Case: the source tree is untouched by the run.
leftovers="$(find "$fixture" -mindepth 1 -maxdepth 1 -not \( -name Makefile -o -name check.sh \) -print)"
[[ -z "$leftovers" ]] && ok "source tree untouched" || bad "source tree untouched (left: $leftovers)"

# Case: no test-result cache can stand in for a real run.
assert_contains "GOFLAGS pins -count=1" "$output" "GOFLAGS=-count=1"

# Case: the forced -count=1 is the last flag, so an inherited -count cannot win.
printed="$(GOFLAGS=-count=3 "$ENVELOPE" --source "$fixture" --print)" && rc=0 || rc=$?
goflags_line="$(grep '^env GOFLAGS=' <<<"$printed")"
case "$goflags_line" in
  *-count=1) ok "forced -count=1 is last" ;;
  *) bad "forced -count=1 is last ($goflags_line)" ;;
esac

# Case: --print reports the plan and runs nothing.
printed="$("$ENVELOPE" --source "$fixture" --print)" && rc=0 || rc=$?
assert_contains     "print shows the command" "$printed" "command: make test"
assert_not_contains "print does not run"      "$printed" "MARKER"

# Case: --print with no --source reports the default source (this repo's root).
printed_default="$("$ENVELOPE" --print)" && rc=0 || rc=$?
assert_contains "print shows the default source" "$printed_default" "source_dir=$REPO_ROOT"

# Case: --help exits 0 and prints the full usage, including the default command
# line (the usage extraction must not drop it).
help_out="$("$ENVELOPE" --help)" && rc=0 || rc=$?
[[ "$rc" -eq 0 ]] && ok "--help exits 0" || bad "--help exits 0 (rc=$rc)"
assert_contains "--help shows the usage"        "$help_out" "Usage:"
assert_contains "--help shows the default command" "$help_out" 'With no `-- COMMAND...`, the command is `make test`.'

# Case: --keep preserves the work dir after a run.
keep_work="$fixture_root/keep-work"
"$ENVELOPE" --source "$fixture" --work-dir "$keep_work" --keep -- make test >/dev/null 2>&1 && rc=0 || rc=$?
[[ "$rc" -eq 0 ]] && ok "--keep run exits 0" || bad "--keep run exits 0 (rc=$rc)"
[[ -d "$keep_work" ]] && ok "--keep keeps the work dir" || bad "--keep keeps the work dir"

# Case: --print removes its work dir (the cleanup trap is installed before the
# print branch, so a preview does not leak a temp directory).
print_work="$(sed -n 's/^work_dir=//p' <<<"$printed")"
if [[ -n "$print_work" && ! -e "$print_work" ]]; then
  ok "print cleans its work dir"
else
  bad "print cleans its work dir (work_dir=$print_work left: $( [[ -e "$print_work" ]] && echo yes || echo no ))"
fi

# Case: a source path with a space survives quoting.
spaced="$(new_fixture "with space")"
spaced_work="$fixture_root/space work"
output="$("$ENVELOPE" --source "$spaced" --work-dir "$spaced_work" -- make test)" && rc=0 || rc=$?
[[ "$rc" -eq 0 ]] && ok "spaced source exits 0" || bad "spaced source exits 0 (rc=$rc)"
assert_contains "spaced source runs in work dir" "$output" "PWD=$spaced_work/module"

# Case: refuses to clobber an existing non-empty work dir (it removes the work
# dir on exit, so a reused path would be lost).
existing="$fixture_root/existing"
mkdir -p "$existing"
: >"$existing/keepme"
"$ENVELOPE" --source "$fixture" --work-dir "$existing" -- make test >/dev/null 2>&1 && rc=0 || rc=$?
[[ "$rc" -ne 0 ]] && ok "existing work dir refused" || bad "existing work dir refused (rc=$rc)"
[[ -f "$existing/keepme" ]] && ok "existing work dir left intact" || bad "existing work dir left intact"

# Case: refuses a work dir inside the source tree (the copy would nest into it).
inside="$fixture_root/base/inside"
"$ENVELOPE" --source "$fixture" --work-dir "$inside" -- make test >/dev/null 2>&1 && rc=0 || rc=$?
[[ "$rc" -ne 0 ]] && ok "work dir inside source refused" || bad "work dir inside source refused (rc=$rc)"

# Case: refuses a work dir that resolves inside the source through a symlinked
# parent, which the textual check above cannot see.
mkdir -p "$fixture/inside-link-target"
ln -s "$fixture/inside-link-target" "$fixture_root/sneaky-link"
"$ENVELOPE" --source "$fixture" --work-dir "$fixture_root/sneaky-link" -- make test >/dev/null 2>&1 && rc=0 || rc=$?
[[ "$rc" -ne 0 ]] && ok "symlinked work dir inside source refused" || bad "symlinked work dir inside source refused (rc=$rc)"

# Case: the default work dir is refused when TMPDIR points inside the source
# (nothing is copied, and the stray temp dir is cleaned up).
inside_tmp="$fixture_root/base/host-tmp"
mkdir -p "$inside_tmp"
TMPDIR="$inside_tmp" "$ENVELOPE" --source "$fixture" -- make test >/dev/null 2>&1 && rc=0 || rc=$?
[[ "$rc" -ne 0 ]] && ok "TMPDIR work dir inside source refused" || bad "TMPDIR work dir inside source refused (rc=$rc)"
leftover_tmp="$(find "$inside_tmp" -mindepth 1 -print -quit)"
[[ -z "$leftover_tmp" ]] && ok "rejected temp dir cleaned up" || bad "rejected temp dir cleaned up (left: $leftover_tmp)"

# Case: --netns is rejected with a clear message when the host is not Linux.
# A `uname` shim forces the branch so it runs on a capable Linux host too,
# rather than self-skipping exactly where the strict run is meant to work.
shim="$fixture_root/shim"
mkdir -p "$shim"
printf '#!/bin/sh\necho Darwin\n' >"$shim/uname"
chmod +x "$shim/uname"
err="$(PATH="$shim:$PATH" "$ENVELOPE" --source "$fixture" --netns --print 2>&1)" && rc=0 || rc=$?
[[ "$rc" -ne 0 ]] && ok "netns rejected off Linux" || bad "netns rejected off Linux (rc=$rc)"
assert_contains "netns rejection names the host os" "$err" "Darwin"

# Case: --netns wraps the command in a loopback-only namespace.
if netns_capable; then
  printed="$("$ENVELOPE" --source "$fixture" --netns --print)" && rc=0 || rc=$?
  assert_contains "print shows netns on"      "$printed" "netns: yes"
  assert_contains "print shows unshare --net" "$printed" "unshare --net"
  assert_contains "print guards loopback setup" "$printed" 'ip link set lo up && exec'
else
  ok "netns wraps the command (skipped: no netns on this host)"
fi

# Case: --netns actually denies egress (name resolution fails inside). The host
# must first resolve the name itself, so a host with no working network does not
# pass trivially; the fixture echoes a start marker so a setup failure is
# distinguished from a denied lookup, and bounds the lookup so a blackholed DNS
# cannot stall the run.
if unshare --net --map-root-user true >/dev/null 2>&1 && command -v ip >/dev/null 2>&1 && command -v getent >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
  if timeout 10 getent hosts example.com >/dev/null 2>&1; then
    resolving="$(new_fixture resolving)"
    cat >"$resolving/check.sh" <<'EOF'
echo "REACHED_NET_CHECK"
timeout 10 getent hosts example.com
EOF
    egress_out="$("$ENVELOPE" --source "$resolving" --work-dir "$fixture_root/netns-work" --netns -- make test 2>&1)" && rc=0 || rc=$?
    assert_contains "netns child reached its check" "$egress_out" "REACHED_NET_CHECK"
    [[ "$rc" -ne 0 ]] && ok "netns blocks egress" || bad "netns blocks egress (unexpected success)"
  else
    ok "netns blocks egress (skipped: host cannot resolve example.com)"
  fi
else
  ok "netns blocks egress (skipped: no netns-capable unshare/ip/getent/timeout)"
fi

# Case: a successful child run inside the namespace proves loopback is up (the
# driver's guarded `ip link set lo up` ran before the child).
if unshare --net --map-root-user true >/dev/null 2>&1 && command -v ip >/dev/null 2>&1; then
  loopback="$(new_fixture loopback)"
  cat >"$loopback/check.sh" <<'EOF'
ip -o link show lo | grep -q 'UP' && echo "LOOPBACK_UP"
EOF
  loopback_out="$("$ENVELOPE" --source "$loopback" --work-dir "$fixture_root/loopback-work" --netns -- make test 2>&1)" && rc=0 || rc=$?
  [[ "$rc" -eq 0 ]] && ok "netns child run succeeds" || bad "netns child run succeeds (rc=$rc: $loopback_out)"
  assert_contains "loopback is up inside the namespace" "$loopback_out" "LOOPBACK_UP"
else
  ok "loopback is up inside the namespace (skipped: no netns-capable unshare/ip)"
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
