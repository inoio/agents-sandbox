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
# Finally it pins the release invariants no other gate covers: the release/*
# push trigger, the `tag-guard` job and its place in `release.needs`, and the
# single main-push writer of the GHCR build cache.
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

violations_file="$(mktemp "${TMPDIR:-/tmp}/ci-guards-violations.XXXXXX")"
trap 'rm -f "$violations_file"' EXIT
# Diagnostics go to stderr and their count to a file: several callers run the
# derivation functions inside `$(...)`, where stdout is captured as the glob set
# and a shell-variable increment would be lost.
report() {
  printf 'ci-guards: %s\n' "$1" >&2
  printf 'v\n' >>"$violations_file"
}

# The canonical non-code path set, in the `paths-ignore` glob form. Every site
# below must classify exactly these paths; the changelog rule adds one more (a
# *_test.go change needs no fragment), asserted separately.
canonical="$(
  printf '%s\n' \
    'docs/**' \
    '.operator-shared/**' \
    'Formula/**' \
    '.github/ISSUE_TEMPLATE/**' \
    '.agents-sandbox/**' \
    '**.md' \
    '.gitignore' | LC_ALL=C sort
)"

# alternation_parts BODY: print each top-level `|`-separated part of BODY. A
# top-level alternative is one not nested inside a `(...)` group, so the
# `^(a/|b/|...)` body stays a single part.
alternation_parts() {
  local str="$1" part="" c depth=0 i
  for ((i = 0; i < ${#str}; i++)); do
    c="${str:i:1}"
    case "$c" in
      '(') depth=$((depth + 1)) ;;
      ')') depth=$((depth - 1)) ;;
      '|')
        if [[ "$depth" -eq 0 ]]; then
          printf '%s\n' "$part"
          part=""
          continue
        fi
        ;;
    esac
    part+="$c"
  done
  [[ -n "$part" ]] && printf '%s\n' "$part"
}

# dir_prefix_globs BODY: turn an alternation body `a/|b/|...` into `a/**`, `b/**`.
dir_prefix_globs() {
  local part
  while IFS= read -r part; do
    printf '%s**\n' "${part//\\/}"
  done <<<"$(alternation_parts "$1")"
}

# globs_from_paths_ignore FILE: print each `paths-ignore` block's globs, blocks
# separated by a `--block--` line. A file may hold several blocks (codeql.yml
# declares one per trigger); the caller compares each block on its own so a
# drift in either is caught rather than hidden by their union.
globs_from_paths_ignore() {
  awk '
    /^[[:space:]]*paths-ignore:[[:space:]]*$/ {
      if (seen) print "--block--"
      seen = 1
      in_block = 1
      next
    }
    in_block && /^[[:space:]]*-[[:space:]]*.+/ {
      line = $0
      sub(/^[[:space:]]*-[[:space:]]*/, "", line)
      gsub(/'"'"'/, "", line)
      print line
      next
    }
    { in_block = 0 }
  ' "$1"
}

# globs_from_changes_regex: the exempt set encoded in ci.yml's `grep -vqE` regex.
# Every top-level alternative in the regex is decoded to a glob (a `^(a/|b/|...)`
# group to its dir globs, `\.md$` to `**.md`, `^\.gitignore$` to `.gitignore`);
# an alternative this derivation cannot model fails loudly rather than being
# silently dropped, so a site that gains an exemption it does not model is caught.
globs_from_changes_regex() {
  local line regex alt body
  line="$(grep -F 'grep -vqE' "$CI_YML" | head -1)"
  regex="$(printf '%s\n' "$line" | sed -E "s/.*grep -vqE '([^']*)'.*/\1/")"
  while IFS= read -r alt; do
    if [[ "$alt" =~ ^\^\((.+)\)$ ]]; then
      body="${BASH_REMATCH[1]}"
      dir_prefix_globs "$body"
    elif [[ "$alt" == '\.md$' ]]; then
      printf '%s\n' '**.md'
    elif [[ "$alt" == '^\.gitignore$' ]]; then
      printf '%s\n' '.gitignore'
    else
      report "ci.yml: changes gate regex has an unrecognized alternative '$alt'"
    fi
  done <<<"$(alternation_parts "$regex")"
}

# globs_from_changelog_check: the exempt set in ci/check-changelog.sh's presence
# rule's `if` condition (the `&&`-chained `[[ ... ]]` sub-tests), minus its
# *_test.go addition (asserted separately below). The condition is extracted as
# a block, not from a whole-file grep, so an unrelated `.gitignore`/`.md`
# mention elsewhere (the IGNORE list, a comment) cannot spoof an exemption that
# the rule no longer makes. A sub-test must be negated (`! ... =~`) to read as an
# exemption, so reversing one is unrecognized and fails loudly too.
globs_from_changelog_check() {
  local cond line inner expr
  cond="$(awk '
    /if \[\[ ! "\$line" =~/ { in_if = 1; print; next }
    in_if {
      print
      if (/; then/) exit
      if (!/\\$/) exit
    }
  ' "$CHANGELOG_CHECK")"
  if [[ -z "$cond" ]]; then
    report "check-changelog.sh: presence condition not found"
    return
  fi
  while IFS= read -r line; do
    # Normalize each sub-test line down to its `[[ ... ]]` inner text.
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line#if }"
    line="${line#&& }"
    line="${line%; then}"
    line="${line%\\}"
    line="${line%"${line##*[![:space:]]}"}"
    expr="${line#*\[\[ }"
    expr="${expr%\]\]}"
    expr="${expr#"${expr%%[![:space:]]*}"}"
    expr="${expr%"${expr##*[![:space:]]}"}"
    [[ -n "$expr" ]] || continue
    # Each `=~` sub-test is matched with its leading `!`, so an exemption that
    # was reversed (the `!` dropped, turning "exempt" into "counts as code")
    # fails loudly instead of still reading as an exemption. The `.gitignore`
    # clause carries its negation in the `!=` operator itself.
    if [[ "$expr" =~ \!=\ *\"\.gitignore\" ]]; then
      printf '%s\n' '.gitignore'
    elif [[ "$expr" =~ ^\!.*=\~\ *\^\((.+)\)$ ]]; then
      dir_prefix_globs "${BASH_REMATCH[1]}"
    elif [[ "$expr" =~ ^\!.*=\~\ *\\\.md\$ ]]; then
      printf '%s\n' '**.md'
    elif [[ "$expr" =~ ^\!.*=\~\ *_test\\\.go\$ ]]; then
      :
    else
      report "check-changelog.sh: presence condition has an unrecognized or un-negated exempt test '$expr'"
    fi
  done <<<"$cond"
}

# --- The exempt-path set must agree across all four sites. ---------------------

# assert_canonical SITE GLOBS: report when a site's set differs from canonical.
assert_canonical() {
  local site="$1" globs="$2"
  if [[ "$globs" == "$canonical" ]]; then
    return
  fi
  report "$site: the non-code path set differs from the canonical set"
  diff <(printf '%s\n' "$canonical") <(printf '%s\n' "$globs") | sed 's/^/    /' >&2 || true
}

# assert_paths_ignore_blocks SITE FILE EXPECTED: assert each `paths-ignore` block
# separately, naming the block when one diverges, and require exactly EXPECTED
# blocks. GitHub cannot compute the lists, so every block is a site that must
# carry the canonical set; a block that disappears entirely is a site lost, not a
# drift the surviving blocks can stand in for.
assert_paths_ignore_blocks() {
  local site="$1" file="$2" expected="$3" block="" line n=0 globs raw found
  raw="$(globs_from_paths_ignore "$file")"
  if [[ -n "$raw" ]]; then
    found=$((1 + $(printf '%s\n' "$raw" | grep -c '^--block--$' || true)))
  else
    found=0
  fi
  while IFS= read -r line; do
    if [[ "$line" == '--block--' ]]; then
      n=$((n + 1))
      globs="$(printf '%s' "$block" | LC_ALL=C sort -u)"
      assert_canonical "$site block $n" "$globs"
      block=""
    else
      block+="$line"$'\n'
    fi
  done <<<"$raw"
  n=$((n + 1))
  globs="$(printf '%s' "$block" | LC_ALL=C sort -u)"
  assert_canonical "$site block $n" "$globs"
  [[ "$found" -eq "$expected" ]] \
    || report "$site: expected $expected paths-ignore block(s), found $found"
}

assert_canonical ci.yml "$(globs_from_changes_regex | LC_ALL=C sort -u)"
assert_paths_ignore_blocks mutation.yml "$MUTATION_YML" 1
assert_paths_ignore_blocks codeql.yml "$CODEQL_YML" 2
assert_canonical check-changelog.sh "$(globs_from_changelog_check | LC_ALL=C sort -u)"

# The changelog rule's extra exemption is easy to drop by accident; keep it.
grep -qF '_test\.go$' "$CHANGELOG_CHECK" \
  || report "check-changelog.sh: the *_test.go exemption is missing from the presence rule"

# --- Workflow-condition invariants (the ones actionlint does not cover). -------

declared_events="$(
  awk '
    { sub(/[[:space:]]+#.*$/, "") }
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

# The `types:` block as written, in either the flow form
# (`types: [opened, ...]`) or the block form (a `types:` line followed by
# indented `- item` lines), joined for matching below.
types_block="$(
  awk '
    { sub(/[[:space:]]+#.*$/, "") }
    /^  pull_request:[[:space:]]*$/ { in_pr = 1; next }
    in_pr && /^  [^[:space:]]/ {
      if (in_types) exit
      in_pr = 0
      next
    }
    in_pr && /types:/ {
      match($0, /types:/)
      types_indent = RSTART - 1
      print
      in_types = 1
      next
    }
    in_types {
      match($0, /[^[:space:]]/)
      if (RSTART - 1 <= types_indent) exit
      if ($0 ~ /^[[:space:]]*-/) print
    }
  ' "$CI_YML"
)"
if [[ -n "$types_block" ]]; then
  for default_type in opened synchronize reopened; do
    printf '%s\n' "$types_block" | grep -qE "(^|[^a-z_])$default_type([^a-z_]|$)" \
      || report "ci.yml: on.pull_request.types omits the default type '$default_type' (declaring types: replaces the default set)"
  done
  # `labeled` is not a pull_request default, but it is what opts a PR into the
  # podman integration job, so declaring types: must keep it too.
  printf '%s\n' "$types_block" | grep -qE '(^|[^a-z_])labeled([^a-z_]|$)' \
    || report "ci.yml: on.pull_request.types omits 'labeled', which opts a PR into the podman integration job"
else
  # No `types:` block is not "nothing to check": the pull_request defaults
  # return, `labeled` is gone, and the podman opt-in can never fire. Deleting
  # the block would otherwise slip past every check above.
  if printf '%s\n' "$declared_events" | grep -qx pull_request; then
    report "ci.yml: on.pull_request declares no types: list (the default types return and 'labeled' is absent, so the podman opt-in can never fire)"
  fi
fi

# --- Fast-path and fail-closed workflow invariants. ---------------------------
#
# The `changes` gate emits two outputs, `code` and `exec`, both derived from the
# shared `ci/event-base.sh`: the Go jobs gate on `merge_group || exec` (a
# docs-only change is inert for them too), the integration jobs on `code`. These
# invariants tie that shape down. A job that re-inlines the merge-base, swallows
# a failed diff, drops the `docs/` exec rule, forgets a `needs:`, loses its
# timeout, or reverts a consumer to `== 'true'` silently undoes the optimization
# or the fail-closed behavior. Every message names the job and the missing token.

# job_block JOB: print JOB's block (its `  <job>:` line's body up to the next
# top-level job key). Scoping to the block keeps a job-local assertion honest:
# the `changelog` job legitimately keeps a `git diff ... || true`, and a
# whole-file grep would false-positive on it.
job_block() {
  awk -v want="$1" '
    /^jobs:[[:space:]]*$/ { in_jobs = 1; next }
    in_jobs && /^[^[:space:]]/ { in_jobs = 0 }
    in_jobs && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ {
      name = $0
      sub(/^  /, "", name)
      sub(/:.*$/, "", name)
      if (name == want) { in_job = 1; next }
      if (in_job) exit
      next
    }
    in_job { print }
  ' "$CI_YML"
}

# job_names: every job key under `jobs:`, in order (the `on:` events share the
# two-space indent, so the section boundary has to be tracked).
job_names() {
  awk '
    /^jobs:[[:space:]]*$/ { in_jobs = 1; next }
    in_jobs && /^[^[:space:]]/ { in_jobs = 0 }
    in_jobs && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ {
      name = $0
      sub(/^  /, "", name)
      sub(/:.*$/, "", name)
      print name
    }
  ' "$CI_YML"
}

# has_token BLOCK TOKEN MESSAGE: report MESSAGE when BLOCK lacks TOKEN.
has_token() {
  printf '%s\n' "$1" | grep -qF -- "$2" || report "$3"
}

changes_block="$(job_block changes)"
has_token "$changes_block" 'ci/event-base.sh' \
  "ci.yml: 'changes' does not call ci/event-base.sh (the merge-base must not be re-inlined)"
has_token "$(job_block changelog)" 'ci/event-base.sh' \
  "ci.yml: 'changelog' does not call ci/event-base.sh (the merge-base must not be re-inlined)"

# A `git diff ... || true` in `changes` hides a failed diff, so the fail-closed
# branch never runs and the gate can run nothing.
if printf '%s\n' "$changes_block" | grep -qE 'git diff.*\|\|[[:space:]]*true'; then
  report "ci.yml: 'changes' keeps a 'git diff ... || true' that hides a failed diff"
fi

# The exec rule and its derivation from code: a docs/ change runs the Go jobs
# (a default-suite test reads docs/), an execution-inert change skips them.
has_token "$changes_block" "grep -qE '^docs/'" \
  "ci.yml: 'changes' is missing the exec rule grep -qE '^docs/'"
has_token "$changes_block" '[ "$code" = true ]' \
  "ci.yml: 'changes' does not derive exec from code (missing [ \"\$code\" = true ])"

# A release tag forces the full set: a force-updated tag can carry a resolvable
# `before`, so a diff that looks execution-inert could otherwise skip a required
# check's work on a release.
has_token "$changes_block" 'refs/tags/' \
  "ci.yml: 'changes' is missing the forced-full-set rule refs/tags/ (a release tag must force code=true and exec=true)"

# The four Go jobs depend on the changes gate.
for job in lint test race deadcode; do
  needs_line="$(printf '%s\n' "$(job_block "$job")" | grep -E '^    needs:' || true)"
  if [[ "$needs_line" != *changes* ]]; then
    report "ci.yml: '$job' needs: does not include 'changes'"
  fi
done

# Every exec-gated body step must keep `merge_group` running (the queue) and
# skip only on an execution-inert changeset. Every one, not just one of them: a
# job with several gated steps (test has three) would otherwise hide a single
# step that lost the merge_group fast path behind a sibling that kept it.
for job in lint test race deadcode; do
  gate_lines="$(
    printf '%s\n' "$(job_block "$job")" \
      | grep -E '^        if:' | grep -F "needs.changes.outputs.exec != 'false'" || true
  )"
  if [[ -z "$gate_lines" ]]; then
    report "ci.yml: '$job' body-step if: is missing needs.changes.outputs.exec != 'false'"
    continue
  fi
  gate_count="$(printf '%s\n' "$gate_lines" | grep -c .)"
  group_count="$(printf '%s\n' "$gate_lines" | grep -cF merge_group || true)"
  [[ "$group_count" -eq "$gate_count" ]] \
    || report "ci.yml: '$job' body-step if: is missing merge_group on $((gate_count - group_count)) of $gate_count gated step(s) (the gate must keep the merge queue running)"
done

# The code-affecting integration jobs gate on code, not the rawer exec.
for job in integration integration-cache-writer vulncheck; do
  has_token "$(job_block "$job")" "needs.changes.outputs.code != 'false'" \
    "ci.yml: '$job' if: is missing needs.changes.outputs.code != 'false'"
done

# Every job bounds itself with a timeout; a hung job otherwise burns a runner.
while IFS= read -r job; do
  [[ -n "$job" ]] || continue
  printf '%s\n' "$(job_block "$job")" | grep -qE '^    timeout-minutes:[[:space:]]*[0-9]+' \
    || report "ci.yml: '$job' is missing a job-level timeout-minutes"
done <<<"$(job_names)"

# --- Release-invariant guards. -------------------------------------------------
#
# Four facts keep the release pipeline honest, and no other gate pins them: a
# push to a release branch triggers CI (so a patch reaches a tag only through a
# checked ref), a `tag-guard` job gates a release tag on main or a release branch,
# the release waits on that guard, and the GHCR build cache keeps exactly one
# writer (a main push). A silent edit — dropping the branch trigger, deleting the
# guard, unbundling it from `release.needs`, or widening the writer's ref — reopens
# the hole the design closed. Each message names the job and the missing token.

# push_branches: the `on.push.branches` list, in flow (`branches: [a, b]`) or
# block (`branches:` then indented `- a` lines) form.
push_branches() {
  awk '
    { sub(/[[:space:]]+#.*$/, "") }
    /^  push:[[:space:]]*$/ { in_push = 1; next }
    in_push && /^  [^[:space:]]/ { in_push = 0 }
    in_push && /^    branches:/ { in_branches = 1; print; next }
    in_branches && /^      - / { print; next }
    in_branches { in_branches = 0 }
  ' "$CI_YML"
}

if ! printf '%s\n' "$(push_branches)" | grep -qF 'release/*'; then
  report "ci.yml: on.push.branches does not include 'release/*' (a release-branch push must run the required checks)"
fi

# The guard job's own gate is what stops a tag on nothing/another branch.
if printf '%s\n' "$(job_names)" | grep -qx 'tag-guard'; then
  has_token "$(job_block tag-guard)" "startsWith(github.ref, 'refs/tags/')" \
    "ci.yml: 'tag-guard' if: is missing startsWith(github.ref, 'refs/tags/') (the guard must run on a release tag)"
else
  report "ci.yml: the 'tag-guard' job is missing (a release tag must be gated on main or a release branch)"
fi

# A release that does not name tag-guard in needs: can start before the guard
# has proven the tag's ancestry.
release_needs="$(printf '%s\n' "$(job_block release)" | grep -E '^    needs:' || true)"
if [[ "$release_needs" != *tag-guard* ]]; then
  report "ci.yml: 'release' needs: does not include 'tag-guard' (the release must wait on the tag guard)"
fi

# Exactly one cache writer: a ref wider than main lets a release push or tag
# clobber the shared GHCR tag a main push owns.
has_token "$(job_block integration-cache-writer)" "github.ref == 'refs/heads/main'" \
  "ci.yml: 'integration-cache-writer' if: is missing github.ref == 'refs/heads/main' (the build cache must keep exactly one writer, a main push)"

# The reader's release-push clause must carry the same `code` gate as the main
# push's build, so a docs-only release push skips a build. The whole-block token
# check above passes on the pull_request clause, so this must scope to the
# release clause itself.
release_clause="$(printf '%s\n' "$(job_block integration)" | grep -F 'refs/heads/release/' || true)"
if [[ -z "$release_clause" ]]; then
  report "ci.yml: 'integration' if: is missing refs/heads/release/ (a release-branch push must run the build)"
elif [[ "$release_clause" != *"needs.changes.outputs.code != 'false'"* ]]; then
  report "ci.yml: 'integration' release-push clause is missing needs.changes.outputs.code != 'false'"
fi

violations="$(grep -c '^v$' "$violations_file" || true)"
if [[ "$violations" -gt 0 ]]; then
  printf 'ci-guards: %d violation(s) found\n' "$violations"
  exit 1
fi
exit 0
