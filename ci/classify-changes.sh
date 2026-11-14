#!/usr/bin/env bash
#
# classify-changes.sh - classify a changed-file list into the `code` and `exec`
# outputs of ci.yml's `changes` gate.
#
# Usage: ci/classify-changes.sh <changed-files-list>
#
# Reads newline-separated paths from the file named by $1 and prints exactly two
# lines to stdout:
#
#   code=<true|false>
#   exec=<true|false>
#
# `code` is false iff every path matches the inert set - the complement of
# mutation.yml/codeql.yml's `paths-ignore` and check-changelog.sh's presence rule
# (keep the four copies in sync; enforced by ci/check-ci-guards.sh). `exec` is
# `code` with `docs/**` removed: a docs path is inert for the runner-image build
# but not for the Go jobs (a default-suite test reads
# docs/configuration/networking.md), so a docs-only list is `code=false`,
# `exec=true`.
#
# The list is read with grep directly, never via a producer into grep's quiet
# mode: grep exits at its first match, the producer then dies of SIGPIPE once the
# list outgrows the pipe buffer, and under `set -o pipefail` that 141 status reads
# as a non-match - failing open and skipping every required job. A list this
# large is ordinary, so the file indirection is load-bearing, not a style choice.
#
# An unreadable or missing file prints a diagnostic to stderr and exits non-zero:
# a caller that cannot read the list must not classify it as inert.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  printf 'classify-changes: usage: %s <changed-files-list>\n' "$0" >&2
  exit 2
fi

changed_files="$1"
if [[ ! -r "$changed_files" ]]; then
  printf 'classify-changes: cannot read changed-files list: %s\n' "$changed_files" >&2
  exit 1
fi

# The `code` set is the complement of mutation.yml/codeql.yml's paths-ignore and
# check-changelog.sh's presence rule; keep the four copies in sync
# (ci/check-ci-guards.sh).
code=false
if grep -vqE '^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/|\.agents-sandbox/)|\.md$|^\.gitignore$' "$changed_files"; then
  code=true
fi

# `exec` is `code` with docs/** removed: a docs/ path is inert for the image build
# but not for the Go jobs (a default-suite test reads
# docs/configuration/networking.md).
exec=false
if [ "$code" = true ] || grep -qE '^docs/' "$changed_files"; then
  exec=true
fi

printf 'code=%s\n' "$code"
printf 'exec=%s\n' "$exec"
