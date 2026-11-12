#!/usr/bin/env bash
# Tests for surface-sweep.sh. Runs against a throwaway root and a stub `asb`,
# so it needs no VM, no runtime, and touches no real state.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
sweep="$here/surface-sweep.sh"
tmp="$(mktemp -d)"
# The root must be short enough for the socket budget, so keep it directly under
# /tmp with a short name (mktemp -d's own path is long).
root="/tmp/asb-sweep-test-$$"
trap 'rm -rf "$tmp" "$root"' EXIT

failures=0
ok() { echo "ok - $1"; }
fail() {
  echo "FAIL - $1"
  failures=$((failures + 1))
}
assert_eq() {
  local name="$1" want="$2" got="$3"
  if [[ "$want" == "$got" ]]; then ok "$name"; else fail "$name (want '$want', got '$got')"; fi
}
assert_dir() {
  local name="$1" path="$2"
  if [[ -d "$path" ]]; then ok "$name"; else fail "$name (no directory $path)"; fi
}
assert_contains() {
  local name="$1" needle="$2" haystack="$3"
  if grep -qF -- "$needle" <<<"$haystack"; then ok "$name"; else fail "$name (missing '$needle')"; fi
}
assert_nonzero() {
  local name="$1" status="$2"
  if [[ "$status" -ne 0 ]]; then ok "$name"; else fail "$name (expected non-zero status)"; fi
}

# A stub `asb` that records its args and emits nothing.
stub="$tmp/asb"
cat >"$stub" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${ASB_STUB_LOG:?}"
EOF
chmod +x "$stub"
export ASB_BIN="$stub"

# --- init rejects a root whose derived socket path is too long ---------------
long_root="$tmp/$(printf 'x%.0s' {1..120})"
set +e
err="$(ASB_SURFACE_ROOT="$long_root" bash "$sweep" init 2>&1)"
status=$?
set -e
assert_nonzero "init refuses a too-long root" "$status"
assert_contains "error names the socket budget" "socket path" "$err"
if [[ -d "$long_root" ]]; then fail "too-long root not created"; else ok "too-long root not created"; fi

# --- init creates data/ and runs/ and is 0700-protected ---------------------
out="$(ASB_SURFACE_ROOT="$root" bash "$sweep" init)"
assert_eq "init prints the root" "$root" "$out"
assert_dir "init creates data/" "$root/data"
assert_dir "init creates runs/" "$root/runs"
assert_eq "init protects the root 0700" "700" "$(stat -c '%a' "$root")"

# --- env creates the per-run tree and exports the isolated vars -------------
env_out="$(ASB_SURFACE_ROOT="$root" bash "$sweep" env foo)"
for d in home config state cache tmp project; do
  assert_dir "env creates runs/foo/$d" "$root/runs/foo/$d"
done
for var in HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_CACHE_HOME TMPDIR; do
  assert_contains "env exports $var" "export $var=" "$env_out"
done
assert_contains "XDG_DATA_HOME is the shared data dir" "export XDG_DATA_HOME=\"$root/data\"" "$env_out"

# --- env is idempotent and requires a name ----------------------------------
before="$(find "$root/runs/foo" | sort)"
env_again="$(ASB_SURFACE_ROOT="$root" bash "$sweep" env foo)"
after="$(find "$root/runs/foo" | sort)"
assert_eq "env is idempotent" "$before" "$after"
assert_eq "env prints the same vars on re-run" "$env_out" "$env_again"

set +e
ASB_SURFACE_ROOT="$root" bash "$sweep" env >/dev/null 2>&1
missing_status=$?
set -e
assert_nonzero "env without a name fails" "$missing_status"

# --- sweep removes sandboxes and volumes, with --yes ------------------------
export ASB_STUB_LOG="$tmp/asb.log"
cat >"$stub" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${ASB_STUB_LOG:?}"
case "$*" in
  "sandbox list -q") printf 'sandbox-a\nsandbox-b\n' ;;
  "volume list") printf 'NAME  KIND  SIZE\nvol-a  dir  -\nvol-b  dir  -\n' ;;
esac
EOF
chmod +x "$stub"
: >"$ASB_STUB_LOG"
ASB_SURFACE_ROOT="$root" bash "$sweep" sweep
log="$(cat "$ASB_STUB_LOG")"
assert_contains "sweep removes sandbox-a" "sandbox remove --force --yes sandbox-a" "$log"
assert_contains "sweep removes sandbox-b" "sandbox remove --force --yes sandbox-b" "$log"
assert_contains "sweep removes vol-a" "volume remove --force --yes vol-a" "$log"
assert_contains "sweep removes vol-b" "volume remove --force --yes vol-b" "$log"

# --- sweep with nothing present prints nothing and exits 0 ------------------
cat >"$stub" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${ASB_STUB_LOG:?}"
case "$*" in
  "sandbox list -q") : ;;
  "volume list") printf 'NAME  KIND  SIZE\n' ;;
esac
EOF
chmod +x "$stub"
: >"$ASB_STUB_LOG"
sweep_out="$(ASB_SURFACE_ROOT="$root" bash "$sweep" sweep)"
assert_eq "empty sweep prints nothing" "" "$sweep_out"

if [[ "$failures" -gt 0 ]]; then
  echo "$failures test(s) failed"
  exit 1
fi
echo "all tests passed"
