#!/usr/bin/env bash
# Tests for scripts/cleanup-merged-branches.sh.
#
# Builds throwaway bare checkouts - a config worktree bridged into the bare
# root with .agents-sandbox symlinks, main advanced past the config branch, and
# a merged worktree plus merged local branch to remove - and asserts the plan.
# Never touches the real repository. Run directly or `make cleanup-branches-test`.
set -euo pipefail

# Isolate the fixtures from the developer's git config: the fixtures run the
# real git binary (init, commit, clone, push to local remotes), and an ambient
# commit signing/askpass or credential helper must not reach them — a signing
# commit would otherwise pop a GUI askpass such as ksshaskpass. The real
# cleanup-merged-branches.sh is a user tool and deliberately keeps the user's
# config; only this test isolates.
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_TERMINAL_PROMPT=0

# Clear git's per-invocation config channels too: they stay active even with the
# config files disabled, so a poisoned GIT_CONFIG_KEY_n (say, commit.gpgsign)
# would still sign a fixture commit.
unset GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT
while IFS='=' read -r inherited _; do
  case "$inherited" in
    GIT_CONFIG_KEY_*|GIT_CONFIG_VALUE_*) unset "$inherited" ;;
  esac
done < <(env)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLEANUP="$SCRIPT_DIR/cleanup-merged-branches.sh"

pass=0
fail=0
# Repositories and capture files live under one parent-owned root. new_fixture
# runs in a command substitution, so it cannot register paths in an array the
# parent trap could see; the root is created here instead.
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/cleanup-fixtures.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }

assert_contains()     { if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1 (missing: $3)"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then bad "$1 (unexpected: $3)"; else ok "$1"; fi; }

# The lines under a section header, up to the next blank line.
section() { awk -v h="$2" 'BEGIN{s=0} $0==h{s=1;next} s&&NF==0{exit} s{print}' <<<"$1"; }

new_fixture() { # [config_dir] [config_branch] [relative|absolute|none] [dirty 0|1] [containment 0|1] -> prints proj
  local config_dir="${1:-config}" config_branch="${2:-agents-sandbox-config}"
  local symlink="${3:-relative}" dirty="${4:-0}" containment="${5:-0}"
  local root seed proj old
  root="$(mktemp -d "$fixture_root/fixture.XXXXXX")"
  seed="$root/seed"; proj="$root/proj"

  mkdir -p "$seed/.agents-sandbox"
  git init -q "$seed"
  git -C "$seed" config user.email test@example.com
  git -C "$seed" config user.name test
  echo base >"$seed/.agents-sandbox/Dockerfile"
  git -C "$seed" add -A
  git -C "$seed" commit -q -m init
  git -C "$seed" branch -M main
  [[ "$containment" == 1 ]] && build_containment_history "$seed"

  git clone -q --bare "$seed" "$proj/.git"
  git -C "$proj" config user.email test@example.com
  git -C "$proj" config user.name test
  git -C "$proj" config worktree.useRelativePaths true
  git -C "$proj" worktree add -q "$proj/main" main
  git -C "$proj" branch "$config_branch" main
  git -C "$proj" worktree add -q "$proj/$config_dir" "$config_branch"

  mkdir -p "$proj/.agents-sandbox"
  case "$symlink" in
    relative) ln -s "../$config_dir/.agents-sandbox/Dockerfile" "$proj/.agents-sandbox/Dockerfile" ;;
    absolute) ln -s "$proj/$config_dir/.agents-sandbox/Dockerfile" "$proj/.agents-sandbox/Dockerfile" ;;
    none) : ;;
  esac

  old="$(git -C "$proj/main" rev-parse HEAD)"
  git -C "$proj/main" commit -q --allow-empty -m advance
  git -C "$proj" update-ref refs/remotes/origin/main "$(git -C "$proj/main" rev-parse HEAD)"
  git -C "$proj" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main
  git -C "$proj" branch stale-branch "$old"
  git -C "$proj" update-ref refs/remotes/origin/"$config_branch" "$old"
  git -C "$proj" update-ref refs/remotes/origin/stale-branch "$old"
  git -C "$proj" worktree add -q "$proj/oldwork" -b old-work "$old"
  if [[ "$containment" == 1 ]]; then
    git -C "$proj" worktree add -q --detach "$proj/detached-contained" "$old"
    git -C "$proj" worktree add -q --detach "$proj/detached-unique" "$(git -C "$proj" rev-parse unique)"
    git -C "$proj" worktree add -q --detach "$proj/detached-current" "$(git -C "$proj" rev-parse main)"
  fi
  [[ "$dirty" == 1 ]] && echo scratch >"$proj/$config_dir/untracked.txt"

  printf '%s\n' "$proj"
}

# History for the content-containment cases: a branch whose only own commit is
# patch-equivalent to a main commit plus a clean auto-merge of a sibling branch
# (removable); the same with the merge amended by a manual edit (kept); and a
# branch with unique commits (kept).
build_containment_history() { # seed
  local seed="$1" init m1 s
  init="$(git -C "$seed" rev-parse HEAD)"
  echo filler >"$seed/filler.txt"; git -C "$seed" add filler.txt; git -C "$seed" commit -qm filler
  echo m1 >"$seed/a.txt"; git -C "$seed" add a.txt; git -C "$seed" commit -qm M1
  m1="$(git -C "$seed" rev-parse HEAD)"
  git -C "$seed" checkout -q -b S "$init"; echo s >"$seed/s.txt"; git -C "$seed" add s.txt; git -C "$seed" commit -qm S
  s="$(git -C "$seed" rev-parse HEAD)"
  git -C "$seed" checkout -q -b containment "$init"; git -C "$seed" cherry-pick "$m1" >/dev/null; git -C "$seed" merge -q --no-edit "$s"
  git -C "$seed" checkout -q -b containment-manual "$init"; git -C "$seed" cherry-pick "$m1" >/dev/null; git -C "$seed" merge -q --no-edit "$s"; echo manual >"$seed/man.txt"; git -C "$seed" add man.txt; git -C "$seed" commit -q --amend --no-edit
  git -C "$seed" checkout -q -b unique "$init"; echo u1 >"$seed/u.txt"; git -C "$seed" add u.txt; git -C "$seed" commit -qm u1; echo u2 >>"$seed/u.txt"; git -C "$seed" commit -qam u2
  git -C "$seed" checkout -q main; git -C "$seed" merge -q --no-edit "$s"
}

run_cleanup() { # proj [extra args...]
  local proj="$1"; shift
  ( cd "$proj/main" && bash "$CLEANUP" --dry-run --no-fetch --no-gh --fresh 0 --color never "$@" )
}

check_protection() { # label proj config_dir config_branch
  local label="$1" proj="$2" dir="$3" branch="$4" out rem_wt rem_lb keep_wt
  out="$(run_cleanup "$proj")"
  rem_wt="$(section "$out" 'Worktrees to remove:')"
  rem_lb="$(section "$out" 'Local branches to delete:')"
  keep_wt="$(section "$out" 'Remaining worktrees:')"
  assert_contains     "$label: config worktree kept"        "$keep_wt" "$proj/$dir"
  assert_contains     "$label: protection note"             "$keep_wt" "[config mirror; protected]"
  assert_not_contains "$label: config worktree not removed" "$rem_wt"  "$proj/$dir"
  assert_contains     "$label: merged worktree still removed" "$rem_wt" "$proj/oldwork"
  assert_not_contains "$label: config branch not deleted"   "$rem_lb"  "$branch (was"
  assert_contains     "$label: stale branch still deleted"  "$rem_lb"  "stale-branch (was"
}

# Case 1 (B+A): named config, mirror branch, relative symlink.
proj="$(new_fixture config agents-sandbox-config relative 0)"
check_protection "B+A named+symlinked" "$proj" config agents-sandbox-config

# Case 2 (B only, relative target - the recipe's actual shape): other name, other branch.
proj="$(new_fixture mirror cfg-mirror relative 0)"
check_protection "B other-name relative" "$proj" mirror cfg-mirror

# Case 2b (B only, absolute target): other name, other branch, absolute symlink.
proj="$(new_fixture mirror2 cfg-mirror absolute 0)"
check_protection "B other-name absolute" "$proj" mirror2 cfg-mirror

# Case 3 (A only): named config, no symlink, branch is neither mirror nor merged.
proj="$(new_fixture config cfg-mirror none 0)"
check_protection "A named no-symlink" "$proj" config cfg-mirror

# Case 4 (dirty): protection must precede the dirty/--force check.
proj="$(new_fixture config agents-sandbox-config relative 1)"
out="$(run_cleanup "$proj" --force)"
keep_wt="$(section "$out" 'Remaining worktrees:')"
assert_contains "dirty: config worktree kept" "$keep_wt" "$proj/config"
assert_contains "dirty config kept under --force" "$keep_wt" "[config mirror; protected]"

# Branch-loop protection: the config branch and its origin ref stay put.
proj="$(new_fixture config agents-sandbox-config relative 0)"
out="$(run_cleanup "$proj")"
rem_lb="$(section "$out" 'Local branches to delete:')"
keep_lb="$(section "$out" 'Remaining local branches:')"
rem_rb="$(section "$out" 'Origin branches to delete:')"
keep_rb="$(section "$out" 'Remaining origin branches:')"
assert_not_contains "local config branch not deleted" "$rem_lb" "agents-sandbox-config (was"
assert_contains     "local config branch kept"        "$keep_lb" "[config mirror; protected]"
assert_not_contains "origin config branch not deleted" "$rem_rb" "origin/agents-sandbox-config"
assert_contains     "origin config branch kept"        "$keep_rb" "origin/agents-sandbox-config"
assert_contains     "origin config branch note"        "$keep_rb" "[config mirror; protected]"

# --quiet still names what it acts on.
proj="$(new_fixture config agents-sandbox-config relative 0)"
out="$(run_cleanup "$proj" --quiet)"
assert_contains "quiet names worktree" "$out" "$proj/oldwork"
assert_contains "quiet names branch"   "$out" "stale-branch"
assert_not_contains "quiet omits remaining worktrees" "$out" "Remaining worktrees:"
assert_not_contains "quiet omits remaining local branches" "$out" "Remaining local branches:"
assert_not_contains "quiet omits remaining origin branches" "$out" "Remaining origin branches:"

# The plan is printed before a non-TTY run aborts.
proj="$(new_fixture config agents-sandbox-config relative 0)"
stdout_file="$fixture_root/stdout"; stderr_file="$fixture_root/stderr"
( cd "$proj/main" && bash "$CLEANUP" --no-fetch --no-gh --fresh 0 --color never \
    >"$stdout_file" 2>"$stderr_file" </dev/null ) && rc=0 || rc=$?
assert_contains     "plan names worktree before abort" "$(cat "$stdout_file")" "$proj/oldwork"
assert_contains     "non-TTY aborts"                   "$(cat "$stderr_file")" "refusing to delete"
[[ "$rc" -ne 0 ]] && ok "non-TTY exit non-zero" || bad "non-TTY exit non-zero"

# Zero planned actions: no prompt, no abort, exit 0.
proj="$(new_fixture config agents-sandbox-config relative 0)"
git -C "$proj" worktree remove "$proj/oldwork"
git -C "$proj" branch -D old-work stale-branch >/dev/null
git -C "$proj" update-ref -d refs/remotes/origin/stale-branch
( cd "$proj/main" && bash "$CLEANUP" --no-fetch --no-gh --fresh 0 --color never \
    >/dev/null 2>"$stderr_file" </dev/null ) && rc=0 || rc=$?
[[ "$rc" -eq 0 ]] && ok "zero actions exit 0" || bad "zero actions exit 0 (rc=$rc)"
assert_not_contains "zero actions no abort" "$(cat "$stderr_file")" "refusing to delete"

# Content containment: a branch whose only own commit is patch-equivalent to a
# main commit plus a clean auto-merge is removed; a manual-resolution merge and
# a branch with unique commits are kept.
proj="$(new_fixture config agents-sandbox-config relative 0 1)"
out="$(run_cleanup "$proj")"
rem_lb="$(section "$out" 'Local branches to delete:')"
keep_lb="$(section "$out" 'Remaining local branches:')"
assert_contains     "containment: auto-merge branch removed" "$rem_lb"  "containment (was"
assert_not_contains "containment: manual-merge branch kept"  "$rem_lb"  "containment-manual (was"
assert_contains     "containment: manual-merge branch kept"  "$keep_lb" "containment-manual"
assert_not_contains "containment: unique branch kept"        "$rem_lb"  "unique (was"
assert_contains     "containment: unique branch kept"        "$keep_lb" "unique"

# Detached worktrees are tested by their HEAD commit: a contained one is removed,
# a unique one is kept, and one at the default-branch tip is kept.
proj="$(new_fixture config agents-sandbox-config relative 0 1)"
out="$(run_cleanup "$proj")"
rem_wt="$(section "$out" 'Worktrees to remove:')"
keep_wt="$(section "$out" 'Remaining worktrees:')"
assert_contains     "detached: contained worktree removed" "$rem_wt"  "$proj/detached-contained"
assert_not_contains "detached: unique worktree kept"       "$rem_wt"  "$proj/detached-unique"
assert_contains     "detached: unique worktree kept"       "$keep_wt" "$proj/detached-unique"
assert_contains     "detached: current worktree kept"      "$keep_wt" "$proj/detached-current"
assert_contains     "detached: current kept as default"    "$keep_wt" "[default branch or newer; kept]"

# A dirty detached worktree is kept without --force.
proj="$(new_fixture config agents-sandbox-config relative 1 1)"
echo scratch >>"$proj/detached-contained/scratch.txt"
out="$(run_cleanup "$proj")"
keep_wt="$(section "$out" 'Remaining worktrees:')"
assert_contains "detached: dirty kept without --force" "$keep_wt" "$proj/detached-contained"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
