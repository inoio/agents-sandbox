#!/usr/bin/env bash
# Tests for scripts/brain-check.sh.
#
# Builds throwaway Operator brain fixtures and asserts each structural rule the
# checker enforces. Never touches the real brain. Run directly or
# `make brain-check-test`.
#
# The fixtures write Markdown, so single-quoted text legitimately contains
# backticks that are not command substitution.
# shellcheck disable=SC2016
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/brain-check.sh"

pass=0
fail=0
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/brain-check-fixtures.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }

assert_contains()     { if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1 (missing: $3 in: $2)"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then bad "$1 (unexpected: $3)"; else ok "$1"; fi; }

# new_brain NAME -> prints a valid brain root
new_brain() {
  local root="$fixture_root/$1/.operator-shared"
  mkdir -p "$root/index" "$root/specs/demo" "$root/guides" "$root/plans"
  cat >"$root/operator.md" <<'EOF'
# Operator
EOF
  cat >"$root/catalog.md" <<'EOF'
# Catalog

## Tree

- `operator.md`
  - Description: Instructions.
  - Read If: Auto-injected.
- `catalog.md`
  - Description: This catalog.
  - Read If: Auto-injected.

### `specs/` - Contracts

- `specs/demo/`
  - Description: Demo contracts.
  - Read If: Working on the demo.
- `guides/demo.md`
  - Description: Demo guide.
  - Read If: Following the demo.
- `plans/demo-plan.md`
  - Description: Demo plan.
  - Read If: Executing the demo plan.
EOF
  cat >"$root/index/index.md" <<'EOF'
---
description: Demo index.
read_if: Navigating the demo.
---

# Shared Project Index

- `README.md` — Ditto.
EOF
  cat >"$root/specs/demo/contract.md" <<'EOF'
---
description: Demo contract.
read_if: Working on the demo.
---

# Demo

- See the guide at [`../../guides/demo.md`](../../guides/demo.md).
EOF
  printf '# Demo guide\n' >"$root/guides/demo.md"
  printf '# Demo plan\n' >"$root/plans/demo-plan.md"
  printf '# Repo\n' >"$fixture_root/$1/README.md"
  printf '%s\n' "$fixture_root/$1"
}

run_check() { # root -> prints "rc=N" then output
  local out rc
  out="$("$CHECK" "$1" 2>&1)" && rc=0 || rc=$?
  printf 'rc=%s\n%s\n' "$rc" "$out"
}

# Case: a well-formed brain passes.
root="$(new_brain good)"
out="$(run_check "$root")"
assert_contains "valid brain exits 0" "$out" "rc=0"

# Case: a spec without frontmatter keys is reported.
root="$(new_brain no-frontmatter)"
cat >"$root/.operator-shared/specs/demo/contract.md" <<'EOF'
---
description: Demo contract.
---

# Demo
EOF
out="$(run_check "$root")"
assert_contains "missing read_if fails" "$out" "rc=1"
assert_contains "missing read_if is named" "$out" "specs/demo/contract.md: missing read_if"

# Case: an index file without frontmatter is reported.
root="$(new_brain no-index-frontmatter)"
printf '# Index\n' >"$root/.operator-shared/index/index.md"
out="$(run_check "$root")"
assert_contains "index without frontmatter fails" "$out" "index/index.md: missing description"

# Case: a broken relative markdown link is reported.
root="$(new_brain broken-link)"
cat >"$root/.operator-shared/specs/demo/contract.md" <<'EOF'
---
description: Demo contract.
read_if: Working on the demo.
---

# Demo

- See [`../../guides/gone.md`](../../guides/gone.md).
EOF
out="$(run_check "$root")"
assert_contains "broken link fails" "$out" "rc=1"
assert_contains "broken link is named" "$out" "broken link"

# Case: an external link and a fragment-only link are not links to check.
root="$(new_brain external-link)"
cat >"$root/.operator-shared/guides/demo.md" <<'EOF'
# Demo guide

- [docs](https://example.com/x)
- [section](#demo)
EOF
out="$(run_check "$root")"
assert_contains "external and fragment links pass" "$out" "rc=0"

# Case: a freeform document with no catalog entry is reported.
root="$(new_brain uncatalogued)"
printf '# Orphan\n' >"$root/.operator-shared/guides/orphan.md"
out="$(run_check "$root")"
assert_contains "uncatalogued guide fails" "$out" "rc=1"
assert_contains "uncatalogued guide is named" "$out" "guides/orphan.md"

# Case: a catalogued path that does not exist is reported.
root="$(new_brain catalog-missing)"
printf '# Stale\n' >"$root/.operator-shared/catalog.md"
cat >>"$root/.operator-shared/catalog.md" <<'EOF'
- `guides/removed.md`
  - Description: Removed guide.
  - Read If: Nothing.
EOF
out="$(run_check "$root")"
assert_contains "catalog of a missing path fails" "$out" "rc=1"
assert_contains "missing catalog path is named" "$out" "guides/removed.md: no such file"

# Case: a spec that cites a plan is reported.
root="$(new_brain cites-plan)"
cat >>"$root/.operator-shared/specs/demo/contract.md" <<'EOF'

- See `.operator-shared/plans/demo-plan.md` for the detail.
EOF
out="$(run_check "$root")"
assert_contains "spec citing a plan fails" "$out" "rc=1"
assert_contains "plan citation is named" "$out" "specs/demo/contract.md"

# Case: a catalog Read If that restates the description prefix is reported.
root="$(new_brain read-if-prefix)"
printf '# Stale\n' >"$root/.operator-shared/catalog.md"
cat >>"$root/.operator-shared/catalog.md" <<'EOF'
- `specs/demo/`
  - Description: Demo contracts.
  - Read If: Implementing or reviewing the demo contract.
EOF
out="$(run_check "$root")"
assert_contains "Read If prefix fails" "$out" "rc=1"
assert_contains "Read If prefix is named" "$out" "Implementing"

# Case: a shared document pointing into the private partition is reported.
root="$(new_brain private-ref)"
printf '\nSee also `.operator/notes.md`.\n' >>"$root/.operator-shared/guides/demo.md"
out="$(run_check "$root")"
assert_contains "private partition reference fails" "$out" "rc=1"
assert_contains "private partition reference is named" "$out" "private partition"

# Case: an illustrative path placeholder in the catalog is not a path claim.
root="$(new_brain illustrative-path)"
printf '\nEach `specs/<subsystem>/` has one entry.\n' >>"$root/.operator-shared/catalog.md"
out="$(run_check "$root")"
assert_contains "illustrative catalog path passes" "$out" "rc=0"

# Case: operator.md may name the private partition it defines.
root="$(new_brain operator-private)"
printf '\n- **Private** (`.operator/`, local only).\n' >>"$root/.operator-shared/operator.md"
out="$(run_check "$root")"
assert_contains "operator.md private reference passes" "$out" "rc=0"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
