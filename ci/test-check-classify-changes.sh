#!/usr/bin/env bash
# Tests for ci/classify-changes.sh.
#
# Builds throwaway changed-file fixtures in a mktemp -d and asserts the
# classifier's stdout and exit status. Run directly or via
# `make classify-changes-test`.
#
# The large code-only case is the regression guard: the old inline
# `printf ... | grep -q` classification failed open on a list larger than the
# ~64 KiB pipe buffer (the producer died of SIGPIPE and pipefail read it as a
# non-match), silently skipping every required job.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLASSIFY="$SCRIPT_DIR/classify-changes.sh"

pass=0
fail=0
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }

fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/classify-changes.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT

last_stdout=""
last_status=0
classify() { # <file>
  last_status=0
  last_stdout="$("$CLASSIFY" "$1" 2>/dev/null)" || last_status=$?
}

expect() { # <name> <want-code> <want-exec>
  local name="$1" want_code="$2" want_exec="$3" want
  want="$(printf 'code=%s\nexec=%s' "$want_code" "$want_exec")"
  if [[ "$last_status" -eq 0 && "$last_stdout" == "$want" ]]; then
    ok "$name"
  else
    bad "$name (status $last_status, got '$last_stdout', want '$want')"
  fi
}

expect_fail() { # <name>
  local name="$1"
  if [[ "$last_status" -ne 0 && "$last_stdout" != *'code='* ]]; then
    ok "$name"
  else
    bad "$name (status $last_status, stdout '$last_stdout')"
  fi
}

# A code-only list larger than the pipe buffer (~123 KiB, well past the 64 KiB
# Linux pipe capacity the old pipeline died on).
seq 1 4000 | sed 's#^#internal/sandbox/pkg/file_#' >"$fixture_root/code-large.txt"
if [[ "$(wc -c <"$fixture_root/code-large.txt")" -gt 65536 ]]; then
  ok "large-code fixture exceeds the pipe buffer"
else
  bad "large-code fixture does not exceed the pipe buffer (the regression case is not exercised)"
fi
classify "$fixture_root/code-large.txt"; expect "large code-only list" true true

# A docs-only list: inert for the image build, live for the Go jobs.
seq 1 4000 | sed 's#^#docs/page_#' >"$fixture_root/docs-large.txt"
classify "$fixture_root/docs-large.txt"; expect "large docs-only list" false true

# An inert-only list: root Markdown.
seq 1 4000 | sed 's#^#notes_#; s#$#.md#' >"$fixture_root/inert-large.txt"
classify "$fixture_root/inert-large.txt"; expect "large inert-only list" false false

# A mixed code+docs list.
printf 'src/main.go\ndocs/guide.md\n' >"$fixture_root/mixed.txt"
classify "$fixture_root/mixed.txt"; expect "mixed code+docs list" true true

# An all-.operator-shared/ list.
seq 1 4000 | sed 's#^#.operator-shared/note_#' >"$fixture_root/shared.txt"
classify "$fixture_root/shared.txt"; expect "all-.operator-shared list" false false

# An empty file: nothing changed, nothing to run.
: >"$fixture_root/empty.txt"
classify "$fixture_root/empty.txt"; expect "empty list" false false

# A missing file must fail loudly, not classify as inert.
classify "$fixture_root/does-not-exist.txt"; expect_fail "missing file"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
