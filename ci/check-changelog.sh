#!/usr/bin/env bash
# Validate the changelog fragment convention.
#
# A fragment is changelog.d/<slug>.<type>.md, one file per change: slug is the
# issue number, else the PR number, and type is one of the changelog sections.
# A PR with several entries of the same type numbers them: <slug>.<type>-<n>.md.
# See .operator-shared/specs/changelog-fragments.md.
#
# - Shape (always): every file in changelog.d/ is named <slug>.<type>.md (or
#   <slug>.<type>-<n>.md) with a known type; the ignored names are skipped.
# - Presence (--changed FILE): when FILE lists one changed path per line (as
#   `git diff --name-only` emits), a changeset that touches production code must
#   include a changelog.d/*.md fragment. The code rule mirrors .github/workflows/ci.yml's
#   `changes` job, except that a test-only changeset (`*_test.go`) needs none;
#   keep the exempt prefix set in sync with the `changes` job.
#
# Usage: ci/check-changelog.sh [ROOT] [--changed FILE]
#   ROOT defaults to the repository root; the test harness passes a fixture root.
#
# Exit status is non-zero when any violation is found.

set -euo pipefail

ROOT=""
CHANGED_FILE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --changed)
      CHANGED_FILE="$2"
      shift 2
      ;;
    *)
      ROOT="$1"
      shift
      ;;
  esac
done
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
    if [[ ! "$base" =~ ^[0-9]+\.($TYPES)(-[0-9]+)?\.md$ ]]; then
      if [[ "$base" =~ ^[0-9]+\.([a-z]+)(-[0-9]+)?\.md$ ]]; then
        report "changelog.d/$base: unknown type '${BASH_REMATCH[1]}'"
      else
        report "changelog.d/$base: does not match <slug>.<type>[-<n>].md"
      fi
    fi
  done < <(find "$CHANGELOG_DIR" -maxdepth 1 -type f | sort)
}

check_shape

# A changeset that touches production code must carry a fragment. The exempt
# prefix set mirrors the `.changes` gate in .github/workflows/ci.yml (docs, the
# operator brain, the Homebrew formula, issue templates, any *.md, and
# .gitignore); on top of that a test-only changeset needs no fragment, so
# `*_test.go` is exempt too.
check_presence() {
  local line base code=0 fragment=0
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    if [[ ! "$line" =~ ^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/) ]] \
      && [[ ! "$line" =~ \.md$ ]] \
      && [[ ! "$line" =~ _test\.go$ ]] \
      && [[ "$line" != ".gitignore" ]]; then
      code=1
    fi
    # Only a top-level, non-ignored fragment counts; a nested path or the
    # directory README does not satisfy the rule.
    if [[ "$line" =~ ^changelog\.d/([^/]+)$ ]]; then
      base="${BASH_REMATCH[1]}"
      [[ " $IGNORE " == *" $base "* ]] && continue
      [[ "$base" =~ \.md$ ]] && fragment=1
    fi
  done <"$CHANGED_FILE"
  if [[ "$code" -eq 1 && "$fragment" -eq 0 ]]; then
    report "code changed but no changelog.d/<slug>.<type>.md fragment was added"
  fi
}
[[ -n "$CHANGED_FILE" ]] && check_presence

if [[ "$violations" -gt 0 ]]; then
  printf 'changelog: %d violation(s) found\n' "$violations"
  exit 1
fi
exit 0
