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

  cat >"$dir/ci/check-changelog.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
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
sed -i '/Formula\/\*\*/d' "$mut/.github/workflows/mutation.yml"
expect "mutation.yml dropping an entry" "$mut" 1 "mutation.yml"

vag="$(new_fixture)"
sed -i '/\.agents-sandbox\/\*\*/d' "$vag/.github/workflows/mutation.yml"
expect "mutation.yml dropping .agents-sandbox" "$vag" 1 "mutation.yml"

cql="$(new_fixture)"
sed -i "s|      - '.gitignore'|      - '.gitignore'\n      - 'src/**'|" "$cql/.github/workflows/codeql.yml"
expect "codeql.yml adding an entry" "$cql" 1 "codeql.yml"

ciy="$(new_fixture)"
sed -i 's|Formula/|Formulae/|' "$ciy/.github/workflows/ci.yml"
expect "ci.yml regex renaming an entry" "$ciy" 1 "ci.yml"

chg="$(new_fixture)"
sed -i '/_test/d' "$chg/ci/check-changelog.sh"
expect "check-changelog.sh dropping the *_test.go exemption" "$chg" 1 "check-changelog.sh"

echo "# check-ci-guards.sh: workflow-condition invariants"
undeclared="$(new_fixture)"
sed -i "s/github.event_name != 'merge_group'/github.event_name != 'scheduled'/" "$undeclared/.github/workflows/ci.yml"
expect "undeclared github.event_name literal" "$undeclared" 1 "not declared in on"

types="$(new_fixture)"
sed -i "s/types: \[opened, synchronize, reopened, labeled\]/types: [opened, labeled]/" "$types/.github/workflows/ci.yml"
expect "pull_request.types dropping a default" "$types" 1 "default type 'synchronize'"

echo "# check-ci-guards.sh: the real repository passes"
expect "this repository" "$(cd "$SCRIPT_DIR/.." && pwd)" 0 ""

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
