#!/usr/bin/env bash
# Tests for ci/event-base.sh.
#
# Builds a throwaway git repo fixture (A -> B on `main`, a `feature` branch forked
# at B with commit C) and asserts the resolver's stdout and exit status for each
# event. Run directly or via `make event-base-test`.
#
# The fixture is self-contained: a local `origin` remote points back at the
# fixture repo, so the pull_request case's fetch/merge-base resolve offline, and
# the harness ignores any host-level git identity (GIT_CONFIG_NOSYSTEM /
# GIT_CONFIG_GLOBAL, as scripts/test-changed-targets.sh does).
set -euo pipefail

export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_TERMINAL_PROMPT=0
unset GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT 2>/dev/null || true

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EVENT_BASE="$SCRIPT_DIR/event-base.sh"

pass=0
fail=0
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }

fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/event-base.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT

# A -> B on `main`; `feature` forked at B adds C. `origin` points at the fixture
# itself with refs/remotes/origin/main at B, so the pull_request fetch resolves.
# The fixture sets a repo-local user identity, so no global git config is needed.
fixture="$fixture_root/repo"
git init -q "$fixture"
git -C "$fixture" symbolic-ref HEAD refs/heads/main
git -C "$fixture" config user.email test@example.com
git -C "$fixture" config user.name test

commit() { # <message>
  git -C "$fixture" commit -q -m "$1"
  git -C "$fixture" rev-parse HEAD
}

printf 'a\n' >"$fixture/file"; git -C "$fixture" add -A
A="$(commit a)"
printf 'b\n' >"$fixture/file"; git -C "$fixture" add -A
B="$(commit b)"
git -C "$fixture" remote add origin "$fixture"
git -C "$fixture" update-ref refs/remotes/origin/main "$B"

git -C "$fixture" checkout -q -b feature
printf 'c\n' >"$fixture/file"; git -C "$fixture" add -A
C="$(commit c)"
git -C "$fixture" checkout -q main

# run_event <event> [VAR=value ...]: run the resolver in the fixture with a
# scrubbed event environment, capturing stdout/status. Usage is the brief's:
#   run_event push GITHUB_BEFORE="$A" GITHUB_SHA="$B"  expect_stdout "$A"
run_event() {
  local event="$1"; shift
  local out
  last_status=0
  out="$(cd "$fixture" && env -u GITHUB_BASE_REF -u GITHUB_BEFORE -u GITHUB_MERGE_GROUP_BASE_SHA \
    GITHUB_EVENT_NAME="$event" "$@" bash "$EVENT_BASE" 2>/dev/null)" || last_status=$?
  last_stdout="$out"
}

expect_stdout() { # <want>
  if [[ "$last_status" -eq 0 && "$last_stdout" == "$1" ]]; then
    ok "stdout $1 (status 0)"
  else
    bad "stdout $1 (status $last_status, got '$last_stdout')"
  fi
}

expect_exit() { # <want-status>
  if [[ "$last_status" -eq "$1" ]]; then
    ok "exit $1"
  else
    bad "exit $1 (got $last_status, stdout '$last_stdout')"
  fi
}

# push with a real before.
run_event push GITHUB_BEFORE="$A" GITHUB_SHA="$B"; expect_stdout "$A"

# push whose before is the zero SHA.
run_event push GITHUB_BEFORE="0000000000000000000000000000000000000000" GITHUB_SHA="$B"; expect_exit 1

# push whose before is absent.
run_event push GITHUB_SHA="$B"; expect_exit 1

# schedule / workflow_dispatch: undecidable.
run_event schedule GITHUB_SHA="$B"; expect_exit 1
run_event workflow_dispatch GITHUB_SHA="$B"; expect_exit 1

# merge_group: its base_sha, or undecidable when empty.
run_event merge_group GITHUB_MERGE_GROUP_BASE_SHA="$B"; expect_stdout "$B"
run_event merge_group GITHUB_MERGE_GROUP_BASE_SHA=""; expect_exit 1

# pull_request: the merge base of the base branch and the head.
run_event pull_request GITHUB_BASE_REF=main GITHUB_SHA="$C"; expect_stdout "$B"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
