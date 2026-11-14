#!/usr/bin/env bash
# Tests for ci/check-ci-guards.sh.
#
# Builds throwaway fixture roots whose four exempt-path sites agree, then mutates
# one site at a time and asserts the checker catches each drift and the two
# workflow-condition invariants. Run directly or via `make ci-guards-check-test`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-ci-guards.sh"

pass=0
fail=0
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }

fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/ci-guards.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT

# new_fixture: a root whose four sites all encode the canonical non-code set.
new_fixture() {
  local dir
  dir="$(mktemp -d "$fixture_root/root.XXXXXX")"
  mkdir -p "$dir/.github/workflows" "$dir/ci"

  # Structurally close to the real ci.yml: jobs at two-space indent, job keys at
  # four, so the checker's job-scoped extraction works and the one-line perl
  # mutations below land where intended.
  cat >"$dir/.github/workflows/ci.yml" <<'EOF'
on:
  push:
    branches: [main]
  pull_request:
    types: [opened, synchronize, reopened, labeled]
  merge_group:
  workflow_dispatch:
  schedule:
    - cron: "43 2 * * *"

jobs:
  changes:
    name: changes
    if: github.event_name != 'merge_group'
    runs-on: ubuntu-latest
    timeout-minutes: 5
    outputs:
      code: ${{ steps.filter.outputs.code }}
      exec: ${{ steps.filter.outputs.exec }}
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - name: Classify the changeset
        id: filter
        run: |
          set -euo pipefail
          code=false
          exec=false
          if base=$(ci/event-base.sh) && files=$(git diff --name-only "$base" "$GITHUB_SHA"); then
            if printf '%s\n' "$files" | grep -vqE '^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/|\.agents-sandbox/)|\.md$|^\.gitignore$'; then
              code=true
            fi
            if [ "$code" = true ] || printf '%s\n' "$files" | grep -qE '^docs/'; then
              exec=true
            fi
          else
            code=true
            exec=true
          fi
          echo "code=$code" >> "$GITHUB_OUTPUT"
          echo "exec=$exec" >> "$GITHUB_OUTPUT"

  changelog:
    name: changelog
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/checkout@v7
      - name: Compute changed files
        run: |
          set -euo pipefail
          base=$(ci/event-base.sh || true)
          git diff --name-only "${base:-}" "$GITHUB_SHA" > /tmp/changed.txt 2>/dev/null || true

  lint:
    name: lint
    needs: [changes]
    if: ${{ !cancelled() }}
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - uses: golangci/golangci-lint-action@v9
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}
        with:
          version: v2.15
      - run: make docs-linkcheck

  test:
    name: test
    needs: [changes]
    if: ${{ !cancelled() }}
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - run: make coverage-junit
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}
      - uses: codecov/codecov-action@v7
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}
      - name: Upload test results to Codecov
        if: ${{ !cancelled() && (github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false') }}
        uses: codecov/codecov-action@v7

  race:
    name: race
    needs: [changes]
    if: ${{ !cancelled() }}
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - run: make test-race
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}

  deadcode:
    name: deadcode
    needs: [changes]
    if: ${{ !cancelled() }}
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - run: make deadcode
        if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}

  vulncheck:
    name: vulncheck
    needs: [changes]
    if: >-
      !cancelled() &&
      github.event_name != 'merge_group' &&
      (github.event_name == 'workflow_dispatch' ||
      startsWith(github.ref, 'refs/tags/') ||
      needs.changes.outputs.code != 'false')
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - run: make vulncheck

  integration:
    name: integration (docker build)
    needs: [changes]
    if: >-
      !cancelled() &&
      (github.event_name == 'workflow_dispatch' ||
      startsWith(github.ref, 'refs/tags/') ||
      (github.event_name == 'pull_request' && needs.changes.outputs.code != 'false'))
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v7
      - run: make test-integration

  integration-cache-writer:
    name: integration (docker build, cache writer)
    needs: [changes]
    if: >-
      !cancelled() &&
      github.event_name == 'push' &&
      github.ref == 'refs/heads/main' &&
      needs.changes.outputs.code != 'false'
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v7
      - run: make test-integration

  integration-fresh:
    name: integration (docker build, fresh)
    if: github.event_name == 'schedule'
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@v7
      - run: make test-integration
EOF

  cat >"$dir/.github/workflows/mutation.yml" <<'EOF'
on:
  pull_request:
    paths-ignore:
      - '**.md'
      - 'docs/**'
      - '.operator-shared/**'
      - 'Formula/**'
      - '.github/ISSUE_TEMPLATE/**'
      - '.agents-sandbox/**'
      - '.gitignore'
  workflow_dispatch:
EOF
  # codeql.yml carries the same list under both push and pull_request.
  cat >"$dir/.github/workflows/codeql.yml" <<'EOF'
on:
  push:
    paths-ignore:
      - '**.md'
      - 'docs/**'
      - '.operator-shared/**'
      - 'Formula/**'
      - '.github/ISSUE_TEMPLATE/**'
      - '.agents-sandbox/**'
      - '.gitignore'
  pull_request:
    paths-ignore:
      - '**.md'
      - 'docs/**'
      - '.operator-shared/**'
      - 'Formula/**'
      - '.github/ISSUE_TEMPLATE/**'
      - '.agents-sandbox/**'
      - '.gitignore'
EOF

  # The fixture mirrors the real file's decoys: an IGNORE list naming
  # `.gitignore` and `.md`, and a comment doing the same. A derivation that
  # greps the whole file instead of the presence condition would still see
  # `.gitignore`/`.md` after the condition drops them, so those decoys are what
  # make the anchored cases below meaningful.
  cat >"$dir/ci/check-changelog.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
# The *.md and .gitignore mentions in this comment, and in the IGNORE list, are
# decoys: only the presence condition below grants an exemption.
IGNORE='README.md README.rst README .gitignore .gitkeep .keep'
check_presence() {
  while IFS= read -r line; do
    if [[ ! "$line" =~ ^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/|\.agents-sandbox/) ]] \
      && [[ ! "$line" =~ \.md$ ]] \
      && [[ ! "$line" =~ _test\.go$ ]] \
      && [[ "$line" != ".gitignore" ]]; then
      code=1
    fi
  done
}
EOF
  printf '%s' "$dir"
}

# expect <name> <root> <want-status> <want-substring>
expect() {
  local name="$1" root="$2" want_status="$3" want_msg="$4" out status
  out="$("$CHECK" "$root" 2>&1)" && status=0 || status=$?
  if [[ "$status" != "$want_status" ]]; then
    bad "$name (exit $status, want $want_status)"
    printf '%s\n' "$out" >&2
    return
  fi
  if [[ -n "$want_msg" && "$out" != *"$want_msg"* ]]; then
    bad "$name (output lacks '$want_msg')"
    printf '%s\n' "$out" >&2
    return
  fi
  ok "$name"
}

echo "# check-ci-guards.sh: agreeing sites pass"
good="$(new_fixture)"
expect "canonical fixture passes" "$good" 0 ""

echo "# check-ci-guards.sh: exempt-path drift is caught"
mut="$(new_fixture)"
perl -ni -e 'print unless m{Formula/\*\*}' "$mut/.github/workflows/mutation.yml"
expect "mutation.yml dropping an entry" "$mut" 1 "mutation.yml"

vag="$(new_fixture)"
perl -ni -e 'print unless m{\.agents-sandbox/\*\*}' "$vag/.github/workflows/mutation.yml"
expect "mutation.yml dropping .agents-sandbox" "$vag" 1 "mutation.yml"

cql="$(new_fixture)"
perl -pi -e "s{      - '\.gitignore'}{      - '.gitignore'\n      - 'src/**'}" "$cql/.github/workflows/codeql.yml"
expect "codeql.yml adding an entry" "$cql" 1 "codeql.yml"

# Ditto, but only in one of codeql.yml's two paths-ignore blocks: the guard must
# compare each block, not their union (which would mask the drift).
oneblock="$(new_fixture)"
perl -ni -e 'if (m/\.gitignore/ && !$done++) { next } print' "$oneblock/.github/workflows/codeql.yml"
expect "codeql.yml drifting one paths-ignore block" "$oneblock" 1 "codeql.yml"

# Dropping a whole paths-ignore block must fail on the expected block count, not
# silently re-assert the surviving block as both "block 1" and "block 2".
absent="$(new_fixture)"
perl -0pi -e "s/  pull_request:\n    paths-ignore:\n(?:      - [^\n]*\n)+//" \
  "$absent/.github/workflows/codeql.yml"
expect "codeql.yml losing a whole paths-ignore block" "$absent" 1 "expected 2 paths-ignore block"

ciy="$(new_fixture)"
perl -pi -e 's{Formula/}{Formulae/}' "$ciy/.github/workflows/ci.yml"
expect "ci.yml regex renaming an entry" "$ciy" 1 "ci.yml"

chg="$(new_fixture)"
perl -ni -e 'print unless m{_test}' "$chg/ci/check-changelog.sh"
expect "check-changelog.sh dropping the *_test.go exemption" "$chg" 1 "check-changelog.sh"

# An alternative the changes regex derivation does not model must fail loudly,
# not be silently dropped (a Makefile-only diff would otherwise read as
# code=false and skip the integration job the spec keeps in).
alt="$(new_fixture)"
perl -pi -e 's{\^\Q\.gitignore\E\$}{^Makefile\$|^\Q\.gitignore\E\$}' "$alt/.github/workflows/ci.yml"
expect "ci.yml regex gaining an unmodeled alternative" "$alt" 1 "unrecognized alternative"

# A derivation diagnostic must not land inside the captured glob list, and its
# violation count must survive the command substitution. Two unmodeled
# alternatives while the modeled set stays canonical must report exactly two
# violations: before the fix the messages were captured as glob data (sinking
# the count to the single, incidental set-mismatch).
deriv="$(new_fixture)"
perl -pi -e 's{\^\Q\.gitignore\E\$}{$&|^Foo\$|^Bar\$}' "$deriv/.github/workflows/ci.yml"
expect "derivation diagnostics are counted and kept out of the glob set" "$deriv" 1 "2 violation(s) found"

# Dropping the .gitignore clause from the presence condition must be caught by
# the anchored derivation, not masked by an unrelated whole-file mention (the
# fixture carries an IGNORE list and a comment that both name .gitignore). The
# clause is removed without disturbing the `; then`, so only the anchoring is
# under test, not a mangled extraction.
gcl="$(new_fixture)"
perl -pi -e 's/ && \[\[ "\$line" != "\.gitignore" \]\]//' "$gcl/ci/check-changelog.sh"
expect "check-changelog.sh dropping the .gitignore clause" "$gcl" 1 "check-changelog.sh"

# Dropping a `!` reverses the sub-test: the rule then counts the exempt paths as
# code instead of exempting them. The derivation must require the negation, not
# merely the `=~` text, or a reversed exemption reads as an exemption. Only the
# `!` is removed, so the sub-test is otherwise identical and an unanchored match
# still recognizes it.
revmd="$(new_fixture)"
perl -pi -e 's/\[\[ ! ("\$line" =~ \\\.md\$) \]\]/[[ $1 ]]/' "$revmd/ci/check-changelog.sh"
expect "check-changelog.sh reversing the .md exemption" "$revmd" 1 "un-negated exempt test"

revtest="$(new_fixture)"
perl -pi -e 's/\[\[ ! ("\$line" =~ _test\\\.go\$) \]\]/[[ $1 ]]/' "$revtest/ci/check-changelog.sh"
expect "check-changelog.sh reversing the *_test.go exemption" "$revtest" 1 "un-negated exempt test"

echo "# check-ci-guards.sh: workflow-condition invariants"
undeclared="$(new_fixture)"
perl -pi -e "s/github\.event_name != 'merge_group'/github.event_name != 'scheduled'/" "$undeclared/.github/workflows/ci.yml"
expect "undeclared github.event_name literal" "$undeclared" 1 "not declared in on"

# A trailing comment on an `on:` key is valid YAML and must not hide the event.
commented="$(new_fixture)"
perl -pi -e 's/^  merge_group:$/  merge_group:  # queue ref/' "$commented/.github/workflows/ci.yml"
expect "declared event with a trailing comment" "$commented" 0 ""

types="$(new_fixture)"
perl -pi -e "s/types: \[opened, synchronize, reopened, labeled\]/types: [opened, labeled]/" "$types/.github/workflows/ci.yml"
expect "pull_request.types dropping a default" "$types" 1 "default type 'synchronize'"

# `labeled` is not a pull_request default but is what opts a PR into the podman
# integration job; dropping it must fail.
lab="$(new_fixture)"
perl -pi -e "s/types: \[opened, synchronize, reopened, labeled\]/types: [opened, synchronize, reopened]/" "$lab/.github/workflows/ci.yml"
expect "pull_request.types dropping labeled" "$lab" 1 "labeled"

# A block-sequence spelling lists the same defaults on following lines; it is a
# valid workflow and must pass, not read every default as missing.
block="$(new_fixture)"
perl -0pi -e 's/    types: \[opened, synchronize, reopened, labeled\]/    types:\n      - opened\n      - synchronize\n      - reopened\n      - labeled/' "$block/.github/workflows/ci.yml"
expect "pull_request.types block-sequence spelling" "$block" 0 ""

blkdrop="$(new_fixture)"
perl -0pi -e 's/    types: \[opened, synchronize, reopened, labeled\]/    types:\n      - opened\n      - labeled/' "$blkdrop/.github/workflows/ci.yml"
expect "pull_request.types block-sequence dropping a default" "$blkdrop" 1 "default type 'synchronize'"

# Removing `types:` entirely restores the pull_request defaults, so `labeled` is
# gone and the podman opt-in can never fire; the guard must report its absence,
# not skip the check.
notypes="$(new_fixture)"
perl -ni -e 'print unless m/^    types: \[opened/' "$notypes/.github/workflows/ci.yml"
expect "pull_request with no types declared" "$notypes" 1 "declares no types"

echo "# check-ci-guards.sh: fast-path and fail-closed invariants"

# The exec rule's docs/ anchor removed: a bare `docs/` would exempt any path
# containing it, letting an execution-relevant change skip the Go suite.
noanchor="$(new_fixture)"
perl -pi -e "s{\Qgrep -qE '^docs/'\E}{grep -qE 'docs/'}" "$noanchor/.github/workflows/ci.yml"
expect "changes exec rule losing its ^docs/ anchor" "$noanchor" 1 "missing the exec rule"

# A `git diff ... || true` in `changes` swallows a failed diff, so the fail-closed
# branch never runs and the gate can run nothing. The same `|| true` is
# legitimate in `changelog`, so the guard must scope its check to `changes`.
swallow="$(new_fixture)"
perl -pi -e 's{\(git diff --name-only "\$base" "\$GITHUB_SHA"\)}{$& || true}' "$swallow/.github/workflows/ci.yml"
expect "changes hiding a failed diff behind || true" "$swallow" 1 "hides a failed diff"

# A code consumer back to the old `== 'true'`: a skipped `changes` job emits an
# empty output, so `== 'true'` skips the build the fail-closed gate should run.
consumer="$(new_fixture)"
perl -pi -e "s{(github\.event_name == 'pull_request' && needs\.changes\.outputs\.code) != 'false'}{\$1 == 'true'}" "$consumer/.github/workflows/ci.yml"
expect "integration gating on code == 'true'" "$consumer" 1 "outputs.code != 'false'"

# A job dropping its `needs: [changes]` no longer waits on the gate.
noneeds="$(new_fixture)"
perl -pi -e 'if (m/^    needs: \[changes\]/ && !$done++) { $_ = "" }' "$noneeds/.github/workflows/ci.yml"
expect "a gated job dropping needs: changes" "$noneeds" 1 "does not include 'changes'"

# `merge_group` dropped from a body-step gate: the queue would then skip the Go
# suite on its throwaway ref instead of running it.
nogroup="$(new_fixture)"
perl -pi -e 's/github\.event_name == .merge_group. \|\| //' "$nogroup/.github/workflows/ci.yml"
expect "body-step gate dropping merge_group" "$nogroup" 1 "missing merge_group"

# Dropping `merge_group` from just one gate in a multi-gate job (the fixture
# `test` job has three) must fail too: every gated step needs the merge-group
# fast path, not merely one of them. The whole-job fixture above cannot catch a
# single-gate regression, because a sibling gate still carries the token.
nogroupone="$(new_fixture)"
perl -pi -e 'if (m/make coverage-junit/) { $want = 1 } if ($want && m/github\.event_name == .merge_group. \|\| /) { s/github\.event_name == .merge_group. \|\| //; $want = 0 }' "$nogroupone/.github/workflows/ci.yml"
expect "one gate in a multi-gate job dropping merge_group" "$nogroupone" 1 "missing merge_group"

# A job without a timeout can burn a runner indefinitely.
notimeout="$(new_fixture)"
perl -pi -e 'if (m/^    timeout-minutes:/ && !$done++) { $_ = "" }' "$notimeout/.github/workflows/ci.yml"
expect "job missing timeout-minutes" "$notimeout" 1 "missing a job-level timeout-minutes"

# Re-inlining the merge-base replaces the tested ci/event-base.sh with untested
# shell that can drift from the fail-closed contract.
inlined="$(new_fixture)"
perl -pi -e 'if (m{ci/event-base\.sh} && !$seen++) { s{ci/event-base\.sh}{git merge-base origin/main "\$GITHUB_SHA"} }' "$inlined/.github/workflows/ci.yml"
expect "changes re-inlining the merge-base" "$inlined" 1 "'changes' does not call ci/event-base.sh"

# The `changelog` job must source the shared helper too ($seen skips the
# `changes` occurrence so only `changelog` is mutated).
nobase="$(new_fixture)"
perl -pi -e 'if (m{ci/event-base\.sh} && $seen++) { s{ci/event-base\.sh}{git merge-base origin/main "\$GITHUB_SHA"} }' "$nobase/.github/workflows/ci.yml"
expect "changelog not calling ci/event-base.sh" "$nobase" 1 "'changelog' does not call ci/event-base.sh"

echo "# check-ci-guards.sh: the real repository passes"
expect "this repository" "$(cd "$SCRIPT_DIR/.." && pwd)" 0 ""

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
