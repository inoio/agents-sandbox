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

  cat >"$dir/.github/workflows/ci.yml" <<'EOF'
on:
  push:
    branches: [main]
  pull_request:
    types: [opened, synchronize, reopened, labeled]
  merge_group:
  workflow_dispatch:

jobs:
  changes:
    runs-on: ubuntu-latest
    steps:
      - run: |
          if printf '%s\n' "$files" | grep -vqE '^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/|\.agents-sandbox/)|\.md$|^\.gitignore$'; then
            echo "code=true"
          fi
  integration:
    if: github.event_name != 'merge_group'
    runs-on: ubuntu-latest
    steps:
      - run: echo 'integration'
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

echo "# check-ci-guards.sh: the real repository passes"
expect "this repository" "$(cd "$SCRIPT_DIR/.." && pwd)" 0 ""

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
