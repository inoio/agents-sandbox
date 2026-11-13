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
trap 'rm -rf "$tmp" "$root" "$root.lock" "${rel_abs:-}"' EXIT

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
# GNU stat -c with a BSD stat -f fallback, like scripts/cleanup-merged-branches.sh.
mode="$(stat -c '%a' "$root" 2>/dev/null || stat -f '%Lp' "$root" 2>/dev/null)"
assert_eq "init protects the root 0700" "700" "$mode"

# --- C: the root resolves absolute, and the budget check runs for every cmd --
# Use a short CWD: the relative root still has to fit the socket budget.
rel_dir="$(cd /tmp && pwd -P)"
rel_abs="$rel_dir/asb-rel-$$"
rel_out="$(cd /tmp && ASB_SURFACE_ROOT="asb-rel-$$" bash "$sweep" init)"
assert_eq "init resolves a relative root absolute" "$rel_abs" "$rel_out"

set +e
env_long_err="$(ASB_SURFACE_ROOT="$long_root" bash "$sweep" env foo 2>&1)"
env_long_status=$?
sweep_long_err="$(ASB_SURFACE_ROOT="$long_root" bash "$sweep" sweep 2>&1)"
sweep_long_status=$?
set -e
assert_nonzero "env refuses a too-long root" "$env_long_status"
assert_nonzero "sweep refuses a too-long root" "$sweep_long_status"
assert_contains "env names the socket budget" "socket path" "$env_long_err"
assert_contains "sweep names the socket budget" "socket path" "$sweep_long_err"

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

# --- A: env drops the ambient config-override namespaces ---------------------
leak_env=(AGENTS_SANDBOX_NETWORK_PROFILE=none OPENCODE_SANDBOX_CPUS=99 MSB_HOME=/tmp/real)
leak_out="$(env "${leak_env[@]}" ASB_SURFACE_ROOT="$root" bash "$sweep" env leaky)"
assert_contains "env drops the AGENTS_SANDBOX override" "AGENTS_SANDBOX_NETWORK_PROFILE" "$leak_out"
assert_contains "env drops the OPENCODE_SANDBOX alias" "OPENCODE_SANDBOX_CPUS" "$leak_out"
assert_contains "env drops the MSB override" "MSB_HOME" "$leak_out"
eval_out="$(
  env "${leak_env[@]}" bash -c '
    eval "$(ASB_SURFACE_ROOT="$1" bash "$2" env leaky)"
    printf "%s|%s\n" "${AGENTS_SANDBOX_NETWORK_PROFILE-unset}" "${MSB_HOME-unset}"
  ' _ "$root" "$sweep"
)"
assert_eq "eval applied drops the ambient override" "unset|unset" "$eval_out"

# --- sweep removes sandboxes and volumes, with --yes ------------------------
# The stub also logs the env each asb call runs under, so a future edit that
# drops the envfile sourcing (letting sweep operate on the real data home) fails
# the isolation assertions below instead of passing. The run inherits
# AGENTS_SANDBOX_QUIET, and the stub emulates the real volume list -q /
# quiet-mode bare volume list (identifiers only, no header), so a revert to the
# old `awk 'NR > 1'` header skip would drop vol-a and fail below.
export ASB_STUB_LOG="$tmp/asb.log"
cat >"$stub" <<'EOF'
#!/usr/bin/env bash
printf 'home=%s xdg_data=%s\n' "$HOME" "$XDG_DATA_HOME" >>"${ASB_STUB_LOG:?}"
printf '%s\n' "$*" >>"${ASB_STUB_LOG:?}"
case "$*" in
  "sandbox list -q") printf 'sandbox-a\nsandbox-b\n' ;;
  "volume list -q") printf 'vol-a\nvol-b\n' ;;
  "volume list") printf 'vol-a\nvol-b\n' ;;
esac
EOF
chmod +x "$stub"
: >"$ASB_STUB_LOG"
ASB_SURFACE_ROOT="$root" AGENTS_SANDBOX_QUIET=true bash "$sweep" sweep
log="$(cat "$ASB_STUB_LOG")"
assert_contains "sweep removes sandbox-a" "sandbox remove --force --yes sandbox-a" "$log"
assert_contains "sweep removes sandbox-b" "sandbox remove --force --yes sandbox-b" "$log"
assert_contains "sweep removes vol-a" "volume remove --force --yes vol-a" "$log"
assert_contains "sweep removes vol-b" "volume remove --force --yes vol-b" "$log"
assert_contains "sweep lists volumes in quiet mode" "volume list -q" "$log"
assert_contains "sweep keeps XDG_DATA_HOME under the root" "$root/data" "$log"
assert_contains "sweep keeps HOME under the root" "$root/runs/coordinator/home" "$log"

# --- sweep with nothing present prints nothing and exits 0 ------------------
cat >"$stub" <<'EOF'
#!/usr/bin/env bash
printf 'home=%s xdg_data=%s\n' "$HOME" "$XDG_DATA_HOME" >>"${ASB_STUB_LOG:?}"
printf '%s\n' "$*" >>"${ASB_STUB_LOG:?}"
case "$*" in
  "sandbox list -q") : ;;
  "volume list -q") : ;;
esac
EOF
chmod +x "$stub"
: >"$ASB_STUB_LOG"
sweep_out="$(ASB_SURFACE_ROOT="$root" bash "$sweep" sweep)"
assert_eq "empty sweep prints nothing" "" "$sweep_out"

# --- sweep fails loudly when a list command fails ---------------------------
cat >"$stub" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  "sandbox list -q") exit 1 ;;
esac
EOF
chmod +x "$stub"
: >"$ASB_STUB_LOG"
set +e
ASB_SURFACE_ROOT="$root" bash "$sweep" sweep >/dev/null 2>"$tmp/sweep.err"
sandbox_list_status=$?
set -e
assert_nonzero "sweep fails when sandbox list fails" "$sandbox_list_status"
assert_contains "sweep names the failing sandbox list" "sandbox list failed" "$(cat "$tmp/sweep.err")"

cat >"$stub" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  "sandbox list -q") : ;;
  "volume list -q") exit 1 ;;
esac
EOF
chmod +x "$stub"
: >"$ASB_STUB_LOG"
set +e
ASB_SURFACE_ROOT="$root" bash "$sweep" sweep >/dev/null 2>"$tmp/sweep.err"
volume_list_status=$?
set -e
assert_nonzero "sweep fails when volume list fails" "$volume_list_status"
assert_contains "sweep names the failing volume list" "volume list failed" "$(cat "$tmp/sweep.err")"

# --- B: the lock is a portable, exclusive mkdir ------------------------------
lock_dir="$root.lock"
ASB_SURFACE_ROOT="$root" bash "$sweep" lock >/dev/null
assert_dir "lock creates <root>.lock" "$lock_dir"
set +e
ASB_SURFACE_ROOT="$root" bash "$sweep" lock >/dev/null 2>&1
second_lock_status=$?
set -e
assert_nonzero "a second lock is refused" "$second_lock_status"
ASB_SURFACE_ROOT="$root" bash "$sweep" unlock
if [[ -d "$lock_dir" ]]; then fail "unlock removes <root>.lock"; else ok "unlock removes <root>.lock"; fi
set +e
ASB_SURFACE_ROOT="$root" bash "$sweep" unlock >/dev/null 2>&1
unlock_again_status=$?
set -e
assert_nonzero "unlock without a held lock fails" "$unlock_again_status"

if [[ "$failures" -gt 0 ]]; then
  echo "$failures test(s) failed"
  exit 1
fi
echo "all tests passed"
