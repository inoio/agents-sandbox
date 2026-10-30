#!/usr/bin/env bash
# Validate the changelog fragment convention.
#
# A fragment is changelog.d/<slug>.<type>.md, one file per change: slug is the
# issue number, else the PR number, and type is one of the changelog sections.
# See .operator-shared/specs/changelog-fragments.md.
#
# - Shape (always): every file in changelog.d/ is named <slug>.<type>.md with a
#   known type; the ignored names are skipped.
#
# Usage: ci/check-changelog.sh [ROOT]
#   ROOT defaults to the repository root; the test harness passes a fixture root.
#
# Exit status is non-zero when any violation is found.

set -euo pipefail

ROOT="${1:-}"
ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

CHANGELOG_DIR="$ROOT/changelog.d"
TYPES='added|changed|deprecated|docs|fixed'
IGNORE='README.md README.rst README .gitignore .gitkeep .keep'

violations=0

report() {
  printf 'changelog: %s\n' "$1"
  violations=$((violations + 1))
}

check_shape() {
  local file base
  [[ -d "$CHANGELOG_DIR" ]] || return 0
  while IFS= read -r file; do
    base="$(basename "$file")"
    [[ " $IGNORE " == *" $base "* ]] && continue
    if [[ ! "$base" =~ ^[0-9]+\.($TYPES)\.md$ ]]; then
      if [[ "$base" =~ ^[0-9]+\.([a-z]+)\.md$ ]]; then
        report "changelog.d/$base: unknown type '${BASH_REMATCH[1]}'"
      else
        report "changelog.d/$base: does not match <slug>.<type>.md"
      fi
    fi
  done < <(find "$CHANGELOG_DIR" -maxdepth 1 -type f | sort)
}

check_shape

if [[ "$violations" -gt 0 ]]; then
  printf 'changelog: %d violation(s) found\n' "$violations"
  exit 1
fi
exit 0
