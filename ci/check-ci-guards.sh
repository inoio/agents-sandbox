#!/usr/bin/env bash
# Validate the CI gate's path classification and the workflow-condition invariants.
#
# Four sites classify the same paths as "not code": the `changes` gate in ci.yml,
# the `paths-ignore` lists in mutation.yml and codeql.yml, and the changelog
# presence rule in ci/check-changelog.sh. GitHub cannot compute those lists (a
# workflow's `paths-ignore` is evaluated before any job runs), so each site
# hardcodes its own copy, and nothing else keeps the copies in sync. This checker
# is that something: it derives the exempt set from each site and fails, naming
# the divergent file, when one drifts from the others.
#
# It also enforces the two workflow-condition invariants actionlint does not
# cover: every `github.event_name == '<event>'` / `!= '<event>'` condition names
# an event declared in `on:`, and a declared `on.pull_request.types` repeats the
# pull_request default types (`types:` replaces the default set rather than
# extending it).
#
# Usage: ci/check-ci-guards.sh [ROOT]
#   ROOT defaults to the repository root; the test harness passes a fixture root.
#
# Exit status is non-zero when any divergence is found.

set -euo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
CI_YML="$ROOT/.github/workflows/ci.yml"
MUTATION_YML="$ROOT/.github/workflows/mutation.yml"
CODEQL_YML="$ROOT/.github/workflows/codeql.yml"
CHANGELOG_CHECK="$ROOT/ci/check-changelog.sh"

violations=0
report() { printf 'ci-guards: %s\n' "$1"; violations=$((violations + 1)); }

# The canonical non-code path set, in the `paths-ignore` glob form. Every site
# below must classify exactly these paths; the changelog rule adds one more (a
# *_test.go change needs no fragment), asserted separately.
canonical="$(
  printf '%s\n' \
    'docs/**' \
    '.operator-shared/**' \
    'Formula/**' \
    '.github/ISSUE_TEMPLATE/**' \
    '**.md' \
    '.gitignore' | LC_ALL=C sort
)"

# dir_prefix_globs BODY: turn a `a/|b/|...` alternation body into `a/**`, `b/**`.
dir_prefix_globs() {
  local body="$1" part
  local -a parts
  IFS='|' read -r -a parts <<<"$body"
  for part in "${parts[@]}"; do
    printf '%s**\n' "${part//\\/}"
  done
}

# globs_from_paths_ignore FILE: every entry of the file's `paths-ignore` blocks.
globs_from_paths_ignore() {
  awk '
    /^[[:space:]]*paths-ignore:[[:space:]]*$/ { in_block = 1; next }
    in_block && /^[[:space:]]*-[[:space:]]*.+/ {
      line = $0
      sub(/^[[:space:]]*-[[:space:]]*/, "", line)
      gsub(/'"'"'/, "", line)
      print line
      next
    }
    { in_block = 0 }
  ' "$1" | LC_ALL=C sort -u
}

# globs_from_changes_regex: the exempt set encoded in ci.yml's `grep -vqE` regex.
globs_from_changes_regex() {
  local line regex body
  line="$(grep -F 'grep -vqE' "$CI_YML" | head -1)"
  regex="$(printf '%s\n' "$line" | sed -E "s/.*grep -vqE '([^']*)'.*/\1/")"
  body="$(printf '%s\n' "$regex" | sed -E 's/^\^\(([^)]*)\).*/\1/')"
  dir_prefix_globs "$body"
  printf '%s\n' "$regex" | grep -qF '\.md$' && printf '%s\n' '**.md'
  printf '%s\n' "$regex" | grep -qF '^\.gitignore$' && printf '%s\n' '.gitignore'
}

# globs_from_changelog_check: the exempt set in ci/check-changelog.sh's presence
# rule, minus its *_test.go addition (asserted separately below).
globs_from_changelog_check() {
  local body
  body="$(grep -F '^(docs/|' "$CHANGELOG_CHECK" | head -1 | sed -E 's/.*\^\(([^)]*)\).*/\1/')"
  dir_prefix_globs "$body"
  grep -qF '\.md$' "$CHANGELOG_CHECK" && printf '%s\n' '**.md'
  grep -qF '.gitignore' "$CHANGELOG_CHECK" && printf '%s\n' '.gitignore'
}

# --- The exempt-path set must agree across all four sites. ---------------------

declare -A site_globs=(
  [ci.yml]="$(globs_from_changes_regex | LC_ALL=C sort -u)"
  [mutation.yml]="$(globs_from_paths_ignore "$MUTATION_YML")"
  [codeql.yml]="$(globs_from_paths_ignore "$CODEQL_YML")"
  [check-changelog.sh]="$(globs_from_changelog_check | LC_ALL=C sort -u)"
)

for site in ci.yml mutation.yml codeql.yml check-changelog.sh; do
  if [[ "${site_globs[$site]}" != "$canonical" ]]; then
    report "$site: the non-code path set differs from the canonical set"
    diff <(printf '%s\n' "$canonical") <(printf '%s\n' "${site_globs[$site]}") | sed 's/^/    /' >&2 || true
  fi
done

# The changelog rule's extra exemption is easy to drop by accident; keep it.
grep -qF '_test\.go$' "$CHANGELOG_CHECK" \
  || report "check-changelog.sh: the *_test.go exemption is missing from the presence rule"

# --- Workflow-condition invariants (the ones actionlint does not cover). -------

declared_events="$(
  awk '
    /^on:[[:space:]]*$/ { in_on = 1; next }
    in_on && /^[^[:space:]]/ { in_on = 0 }
    in_on && /^  [a-z_]+:[[:space:]]*$/ { sub(/^  /, ""); sub(/:.*/, ""); print }
  ' "$CI_YML" | LC_ALL=C sort -u
)"

used_events="$(
  grep -oE "github\.event_name[[:space:]]*[!=]=[[:space:]]*'[a-z_]+'" "$CI_YML" \
    | sed -E "s/.*'([a-z_]+)'/\1/" | LC_ALL=C sort -u
)"

while IFS= read -r event; do
  [[ -n "$event" ]] || continue
  printf '%s\n' "$declared_events" | grep -qx "$event" \
    || report "ci.yml: github.event_name is compared to '$event', which is not declared in on:"
done <<<"$used_events"

types_line="$(
  awk '
    /^  pull_request:[[:space:]]*$/ { in_pr = 1; next }
    in_pr && /^  [^[:space:]]/ { in_pr = 0 }
    in_pr && /types:/ { print; exit }
  ' "$CI_YML"
)"
if [[ -n "$types_line" ]]; then
  for default_type in opened synchronize reopened; do
    printf '%s\n' "$types_line" | grep -qE "(^|[^a-z_])$default_type([^a-z_]|$)" \
      || report "ci.yml: on.pull_request.types omits the default type '$default_type' (declaring types: replaces the default set)"
  done
fi

if [[ "$violations" -gt 0 ]]; then
  printf 'ci-guards: %d violation(s) found\n' "$violations"
  exit 1
fi
exit 0
