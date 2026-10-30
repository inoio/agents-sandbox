# Changelog Fragments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace direct `CHANGELOG.md` `[Unreleased]` edits with per-PR fragment files in `changelog.d/`, so no two PR branches ever touch the same file and the merge queue stops ejecting PRs over a changelog conflict.

**Architecture:** A fragment is `changelog.d/<issue-or-pr>.<type>.md`, one file per change, so parallel PRs add distinct paths and cannot collide. A shell checker (`ci/check-changelog.sh`, tested by `ci/test-check-changelog.sh`) validates fragment names and, on a PR diff, requires a fragment when code changed. It becomes a new always-run required CI check. The release process assembles fragments into `CHANGELOG.md`; `CHANGELOG.md` is otherwise never touched.

**Tech Stack:** Bash 5 (`set -euo pipefail`), GitHub Actions, GNU Make.

**Spec:** `.operator-shared/specs/changelog-fragments.md`

## Global Constraints

- Fragment type vocabulary is closed and lowercase: `added`, `changed`, `deprecated`, `docs`, `fixed` — exactly the `CHANGELOG.md` sections in use.
- Fragment filename is `<slug>.<type>.md`; `<slug>` is the **issue number**, else the **PR number** (`[0-9]+`). No descriptive slugs.
- No new runtime dependencies: no Python, no towncrier. The checker is Bash, like `ci/check-docs.sh`.
- The "code" classification must match the `changes` job in `.github/workflows/ci.yml`: a path is non-code when it matches `^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/)|\.md$|^\.gitignore$`.
- The `changelog` CI job must always run — never a job-level `if:` — because GitHub reports a skipped required check as Success.
- Ignored names in `changelog.d/` (never validated): `README.md`, `README.rst`, `README`, `.gitignore`, `.gitkeep`, `.keep`.

## Review Focus

- A fragment whose type is unknown (`123.feature.md`) or missing (`123.md`) must fail the shape check, not be passed over.
- A code-only changeset with no fragment must fail; a docs-only or `*.md`-only changeset must pass.
- The release PR (changes only `CHANGELOG.md` and deletes `changelog.d/*.md`, all `*.md`) must pass the presence rule.
- Ignored files (`changelog.d/README.md`, `.gitkeep`) must not trip the shape check; an absent `changelog.d/` must not crash it.
- Every event (`pull_request`, `merge_group`, `push`) must report the `changelog` check; the presence rule must not spuriously fail a merge-group or push run.

---

### Task 1: Fragment shape checker and its test harness

**Files:**
- Create: `ci/check-changelog.sh`
- Create: `ci/test-check-changelog.sh`
- Modify: `Makefile` (add targets; wire into `check`, `verify`, `all`)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `ci/check-changelog.sh [ROOT]` — validates the shape of `ROOT/changelog.d/` (ROOT defaults to the repository root; a fixture root in tests). Exit 0 when clean, non-zero with `changelog: <msg>` lines otherwise. A `--changed FILE` mode is added in Task 2; this task defines the ROOT positional and the `report`/exit convention.
  - `ci/test-check-changelog.sh` — fixture-driven test runner (like `ci/test-check-docs.sh`), exit 0 when all cases pass.

- [ ] **Step 1: Write the failing test harness**

Create `ci/test-check-changelog.sh`. Copy the `pass`/`fail`/`expect` scaffolding and final summary from `ci/test-check-docs.sh` (its lines 11–12 and the tail), but the fixture is a bare directory and `expect` runs the checker with no `--changed`:

```bash
#!/usr/bin/env bash
# Tests for ci/check-changelog.sh. Builds throwaway fixture trees and asserts
# the checker's exit status and messages. Run directly or via `make changelog-check-test`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-changelog.sh"

pass=0
fail=0

# new_fixture [name.type.md ...]: a temp root whose changelog.d/ holds the named files.
new_fixture() {
  local dir
  dir="$(mktemp -d)"
  mkdir -p "$dir/changelog.d"
  local f
  for f in "$@"; do printf -- '- x\n' >"$dir/changelog.d/$f"; done
  echo "$dir"
}

# expect <name> <want-status> <want-substring> <fixture-dir>
expect() {
  local name="$1" want_status="$2" want_msg="$3" dir="$4" out status
  out="$(bash "$CHECK" "$dir" 2>&1)" && status=0 || status=$?
  if [[ "$status" != "$want_status" ]]; then
    printf 'FAIL %s: exit %s, want %s\n%s\n' "$name" "$status" "$want_status" "$out"
    fail=$((fail + 1)); return
  fi
  if [[ -n "$want_msg" && "$out" != *"$want_msg"* ]]; then
    printf 'FAIL %s: output missing %q\n%s\n' "$name" "$want_msg" "$out"
    fail=$((fail + 1)); return
  fi
  pass=$((pass + 1)); printf 'ok   %s\n' "$name"
}

finish() { printf '%s passed, %s failed\n' "$pass" "$fail"; [[ "$fail" -eq 0 ]]; }
trap finish EXIT
```

Add the cases (each a `- [ ]`-free code block in the test file):

```bash
d=$(new_fixture 432.added.md 431.fixed.md); expect valid 0 "" "$d"
d=$(new_fixture 432.feature.md);            expect unknown-type 1 "unknown type" "$d"
d=$(new_fixture 432.md);                    expect missing-type 1 "does not match" "$d"
d=$(new_fixture 432.added.txt);             expect wrong-suffix 1 "does not match" "$d"
d=$(new_fixture);                           expect empty-ok 0 "" "$d"
d=$(mktemp -d);                             expect absent-dir-ok 0 "" "$d"
d=$(new_fixture README.md .gitkeep);        expect ignored-ok 0 "" "$d"
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `ci/test-check-changelog.sh`
Expected: FAIL — `ci/check-changelog.sh` does not exist yet (bash: No such file).

- [ ] **Step 3: Implement `ci/check-changelog.sh`**

Mirror `ci/check-docs.sh`'s structure: `set -euo pipefail`, `ROOT` positional, a `report()` that prints `changelog: <msg>` and increments `violations`, and a non-zero exit when `violations > 0`.

```bash
#!/usr/bin/env bash
# Validate the changelog fragment convention (see .operator-shared/specs/changelog-fragments.md).
#
# - Shape (always): every file in changelog.d/ is named <slug>.<type>.md with a
#   known type; the ignored names are skipped.
# - Presence (--changed FILE): when FILE lists one changed path per line (git
#   diff --name-only), a changeset that touches code must include a
#   changelog.d/*.md fragment. The code rule mirrors .github/workflows/ci.yml's
#   `changes` job; keep the two in sync.
#
# Usage: ci/check-changelog.sh [ROOT] [--changed FILE]
set -euo pipefail

ROOT=""
CHANGED_FILE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --changed) CHANGED_FILE="$2"; shift 2 ;;
    *) ROOT="$1"; shift ;;
  esac
done
ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

CHANGELOG_DIR="$ROOT/changelog.d"
TYPES='added|changed|deprecated|docs|fixed'
IGNORE='README.md README.rst README .gitignore .gitkeep .keep'

violations=0
report() { printf 'changelog: %s\n' "$1"; violations=$((violations + 1)); }

check_shape() {
  local file base name
  [[ -d "$CHANGELOG_DIR" ]] || return 0
  while IFS= read -r file; do
    base="$(basename "$file")"
    name=" $base "
    [[ " $IGNORE " == *"$name"* ]] && continue
    if [[ ! "$base" =~ ^[0-9]+\.($TYPES)\.md$ ]]; then
      if [[ "$base" =~ ^[0-9]+\.([a-z]+)\.md$ ]]; then
        report "changelog.d/$base: unknown type '${BASH_REMATCH[1]}'"
      else
        report "changelog.d/$base: does not match <slug>.<type>.md"
      fi
    fi
  done < <(find "$CHANGELOG_DIR" -maxdepth 1 -type f | sort)
}

check_presence() {
  local line code=0 fragment=0
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    if [[ ! "$line" =~ ^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/) ]] \
       && [[ ! "$line" =~ \.md$ ]] && [[ "$line" != ".gitignore" ]]; then
      code=1
    fi
    [[ "$line" =~ ^changelog\.d/.*\.md$ ]] && fragment=1
  done <"$CHANGED_FILE"
  if [[ "$code" -eq 1 && "$fragment" -eq 0 ]]; then
    report "code changed but no changelog.d/<slug>.<type>.md fragment was added"
  fi
}

check_shape
[[ -n "$CHANGED_FILE" ]] && check_presence

if [[ "$violations" -gt 0 ]]; then
  printf 'changelog: %d violation(s) found\n' "$violations"
  exit 1
fi
exit 0
```

Make it executable: `chmod +x ci/check-changelog.sh ci/test-check-changelog.sh`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `ci/test-check-changelog.sh`
Expected: `8 passed, 0 failed` and exit 0.

- [ ] **Step 5: Wire the Makefile**

Add to the `.PHONY` list `changelog-check changelog-check-test`. Add the targets near `docs-linkcheck`:

```make
# Validate the changelog fragment convention (shape; see specs/changelog-fragments.md).
changelog-check:
	ci/check-changelog.sh

# Test the changelog fragment checker itself.
changelog-check-test:
	ci/test-check-changelog.sh
```

Extend three lines to include them:

```make
check: fmt lint test compile-all docs-linkcheck docs-linkcheck-test changelog-check changelog-check-test deadcode-test
verify: validate-fmt lint test test-race compile-all vulncheck deadcode docs-linkcheck docs-linkcheck-test changelog-check changelog-check-test deadcode-test
all: fmt lint test docs-linkcheck docs-linkcheck-test changelog-check changelog-check-test build
```

- [ ] **Step 6: Run `make changelog-check`**

Run: `make changelog-check`
Expected: no output, exit 0 (there is no `changelog.d/` yet).

- [ ] **Step 7: Commit**

```bash
git add ci/check-changelog.sh ci/test-check-changelog.sh Makefile
git commit -m "ci: add changelog fragment shape checker"
```

---

### Task 2: Presence rule for code-changing PRs

**Files:**
- Modify: `ci/check-changelog.sh` (the `check_presence` block already scaffolded in Task 1 becomes exercised)
- Modify: `ci/test-check-changelog.sh` (add presence fixtures)

**Interfaces:**
- Consumes: `ci/check-changelog.sh [ROOT] [--changed FILE]` from Task 1.
- Produces: the `--changed FILE` behavior — exit non-zero when `FILE`'s changeset touches code and lists no `changelog.d/*.md` path.

- [ ] **Step 1: Write the failing presence tests**

In `ci/test-check-changelog.sh`, add a helper that writes a changeset list and an `expect_changed` runner:

```bash
# expect_presence <name> <want-status> <want-substring> <fixture-dir> <changed-lines...>
expect_presence() {
  local name="$1" want_status="$2" want_msg="$3" dir="$4"; shift 4
  local changed out status
  changed="$(mktemp)"
  printf '%s\n' "$@" >"$changed"
  out="$(bash "$CHECK" "$dir" --changed "$changed" 2>&1)" && status=0 || status=$?
  rm -f "$changed"
  # same status/msg assertions as expect(), then pass/fail accounting
}
```

Cases:

```bash
d=$(new_fixture)
expect_presence code-no-fragment   1 "no changelog.d"      "$d" "internal/foo/foo.go"
d=$(new_fixture 432.added.md)
expect_presence code-with-fragment 0 ""                    "$d" "internal/foo/foo.go" "changelog.d/432.added.md"
d=$(new_fixture)
expect_presence docs-only          0 ""                    "$d" "docs/commands.md"
d=$(new_fixture)
expect_presence md-only            0 ""                    "$d" "README.md"
d=$(new_fixture)
expect_presence gitignore-only     0 ""                    "$d" ".gitignore"
d=$(new_fixture)
expect_presence release-pr-all-md  0 ""                    "$d" "CHANGELOG.md" "changelog.d/432.added.md"
d=$(new_fixture)
expect_presence workflow-yml       1 "no changelog.d"      "$d" ".github/workflows/ci.yml"
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `ci/test-check-changelog.sh`
Expected: FAIL on `release-pr-all-md` and `workflow-yml` (the regex/`code` logic in Task 1's scaffold must be confirmed; if Task 1's `check_presence` is already correct these pass — then instead introduce a deliberately wrong message and watch it fail before tightening). The point: each presence case runs the real checker.

- [ ] **Step 3: Verify/fix the presence rule**

Confirm `check_presence` classifies `.github/workflows/ci.yml` as code (it must not match the exempt regex) and `CHANGELOG.md` / `changelog.d/*.md` / `docs/*` / `README.md` / `.gitignore` as non-code. Adjust the regex only if a case fails; the shipped rule must match the `changes` job.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `ci/test-check-changelog.sh`
Expected: `15 passed, 0 failed`.

- [ ] **Step 5: Commit**

```bash
git add ci/check-changelog.sh ci/test-check-changelog.sh
git commit -m "ci: require a changelog fragment for code-changing PRs"
```

---

### Task 3: Require the `changelog` check in CI

**Files:**
- Modify: `.github/workflows/ci.yml` (new `changelog` job)
- Modify: `.operator-shared/specs/ci-job-gating.md` (required set + description/read_if)
- Modify: `CONTRIBUTING.md` (the required-check list in the Merging section)

**Interfaces:**
- Consumes: `ci/check-changelog.sh [ROOT] [--changed FILE]` from Task 2.
- Produces: a required status check named `changelog`.

- [ ] **Step 1: Add the job**

In `.github/workflows/ci.yml`, add a `changelog` job that always runs and mirrors the `changes` job's base computation. Place it beside `changes`:

```yaml
  # Required check. Shape always; presence on the changed-file list. It must
  # always run: a skipped required check reports Success without running. The
  # base computation mirrors the `changes` job.
  changelog:
    name: changelog
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - name: Compute changed files
        run: |
          set -euo pipefail
          if [ '${{ github.event_name }}' = 'pull_request' ]; then
            git fetch --no-tags origin "refs/heads/${{ github.base_ref }}:refs/remotes/origin/${{ github.base_ref }}"
            base=$(git merge-base "origin/${{ github.base_ref }}" '${{ github.sha }}')
          elif [ '${{ github.event_name }}' = 'merge_group' ]; then
            base='${{ github.event.merge_group.base_sha }}'
          else
            base='${{ github.event.before }}'
            if ! git cat-file -e "${base}^{commit}" 2>/dev/null; then base='HEAD^'; fi
          fi
          git diff --name-only "$base" '${{ github.sha }}' > /tmp/changed.txt || true
      - name: Check fragments
        run: ci/check-changelog.sh . --changed /tmp/changed.txt
```

- [ ] **Step 2: Update the gating spec**

In `.operator-shared/specs/ci-job-gating.md`: change "always runs `lint` … `test` … `race` … and `deadcode` — the four required branch checks" to include `changelog` (`ci/check-changelog.sh`, an always-run check that decides presence from the diff). Update the merge-queue paragraph ("The required set is exactly `lint`/`test`/`race`/`deadcode`") to include `changelog` and why it is cheap/deterministic. Update the ruleset invariant bullet that lists the required checks. Update the frontmatter `description` and `read_if` to name the `changelog` check and this spec.

- [ ] **Step 3: Update CONTRIBUTING's required-check list**

In `CONTRIBUTING.md`'s Merging section, change the parenthetical ``(`lint`, `test`, `race`, `deadcode`)`` to add `changelog`, keeping the "always run" sentence accurate.

- [ ] **Step 4: Verify the workflow parses**

Run: `yq -e '.jobs.changelog.name == "changelog"' .github/workflows/ci.yml`
Expected: `true`.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml .operator-shared/specs/ci-job-gating.md CONTRIBUTING.md
git commit -m "ci: add the required changelog check"
```

- [ ] **Step 6 (maintainer, out of band): add `changelog` to the `protect main` ruleset's required checks.**

This is a repository setting, not a file; note it in the PR body. The check must be present on `main`'s default branch (via a merged PR) before the ruleset can require it.

---

### Task 4: Introduce `changelog.d/` and this change's fragment

**Files:**
- Create: `changelog.d/README.md`
- Create: `changelog.d/<this-change-number>.changed.md`
- Modify: `CHANGELOG.md` (a note under the header)

**Interfaces:**
- Consumes: the fragment contract from `specs/changelog-fragments.md`.
- Produces: the live `changelog.d/` directory; the first real fragment, which the presence check (Task 2) accepts.

- [ ] **Step 1: Write the directory README**

`changelog.d/README.md`: a short workflow note — what a fragment is, the `<issue-or-pr>.<type>.md` name, the five types, that the file contains one bullet's text without the leading `- `, that fragments are assembled at release, and a link to `.operator-shared/specs/changelog-fragments.md`.

- [ ] **Step 2: Write the fragment for this change**

Create `changelog.d/<number>.changed.md` where `<number>` is this work's issue number if one is opened, else the PR number assigned when the PR is opened. Content is the bullet text (no leading `- `):

```
Changelog entries are now added as per-PR fragment files in `changelog.d/` instead of editing
`CHANGELOG.md`'s `[Unreleased]` section directly, so two pull requests no longer conflict on the
changelog and are not ejected from the merge queue over it. A required `changelog` check enforces a
fragment for code-changing PRs. See `CONTRIBUTING.md`.
```

- [ ] **Step 3: Note the mechanism in `CHANGELOG.md`**

Under the intro paragraph (after line 7) add a single sentence: this file is updated only by the release process; between releases, entries live in `changelog.d/`.

- [ ] **Step 4: Verify the checker accepts the new directory**

Run: `make changelog-check`
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
git add changelog.d CHANGELOG.md
git commit -m "docs: introduce changelog.d fragments"
```

---

### Task 5: Contributor-facing documentation

**Files:**
- Modify: `CONTRIBUTING.md` (Changelog & versioning; Submitting a PR)
- Modify: `.github/PULL_REQUEST_TEMPLATE.md`
- Modify: `AGENTS.md`

**Interfaces:**
- Consumes: the fragment contract; the required `changelog` check from Task 3.
- Produces: the written rule every contributor and agent reads.

- [ ] **Step 1: Rewrite CONTRIBUTING's Changelog section**

Replace "Keep `CHANGELOG.md` current. When you make a user-visible change, add an item under the `[Unreleased]` section." with the fragment rule: add `changelog.d/<issue-or-pr>.<type>.md` (types, one-bullet content, that `CHANGELOG.md` is touched only at release). Mention the `changelog` required check. Add a checklist line to the Submitting-a-PR list ("Added a `changelog.d/` fragment for any code change").

- [ ] **Step 2: Add the PR-template item**

In `.github/PULL_REQUEST_TEMPLATE.md`'s checklist add: `- [ ] Added a changelog fragment under changelog.d/ (skip if docs-only)`.

- [ ] **Step 3: Update AGENTS.md**

In the Documentation bullet, replace "add a line to the `[Unreleased]` section in `CHANGELOG.md`" with "add a `changelog.d/<issue-or-pr>.<type>.md` fragment".

- [ ] **Step 4: Verify docs links still pass**

Run: `make docs-linkcheck`
Expected: no violations, exit 0.

- [ ] **Step 5: Commit**

```bash
git add CONTRIBUTING.md .github/PULL_REQUEST_TEMPLATE.md AGENTS.md
git commit -m "docs: document the changelog fragment workflow"
```

---

### Task 6: Release assembly and tracking the release skill

**Files:**
- Modify and track: `.agents/skills/release/` (currently untracked; steps 1, 6, 9 of `SKILL.md`, plus `RELEASE-NOTES.md`, `NON-GO-DEPS.md`, `agents/openai.yaml` — the whole `release/` directory enters the repo)
- Modify: `.operator-shared/index/index.md` (the `.agents/` line: release is now tracked, the rest stays untracked tooling)

**Ruling:** the `merge-queue-prs.md` guide update is **out of scope for this branch** — that file is another session's untracked work in the `IDE` worktree, and the user chose to leave it to its owner. The `[Unreleased]`/`CHANGELOG.md` conflict guidance therefore stays until that guide lands; this spec and the release skill carry the durable fix.

**Interfaces:**
- Consumes: the assembly contract from `specs/changelog-fragments.md`.
- Produces: a release step that consumes and deletes fragments; the `release` skill tracked in the repo.

- [ ] **Step 1: Update the release skill**

- Step 1 (Curate the changelog): read `changelog.d/*` and assemble per the spec's order (sections `Added`, `Changed`, `Deprecated`, `Docs`, `Fixed`; fragments within a section sorted by filename ascending), then curate; still fold any legacy `[Unreleased]` entries. Point at `specs/changelog-fragments.md`.
- Step 6 (Land the changelog): promote the assembled content + legacy `[Unreleased]` into `## [X.Y.Z] - <date>`, delete the fragment files in the same PR, and add a fresh empty `[Unreleased]`.
- Step 9 (Post-release): confirm `changelog.d/` is empty (only `README.md` remains).

- [ ] **Step 2: Update the project index**

In `.operator-shared/index/index.md`, change the `.agents/` entry so it says the `release/` runbook is tracked and the rest of `.agents/skills/` is local tooling.

- [ ] **Step 3: Verify the release skill still reads coherently**

Run: `sed -n '26,60p' .agents/skills/release/SKILL.md`
Expected: step 1 names `changelog.d/` and the spec.

- [ ] **Step 4: Track the release skill and commit**

`.agents/skills/` is otherwise local tooling; the `release/` directory is repo-specific and now ships. Add only it, plus the index update:

```bash
git add .agents/skills/release .operator-shared/index/index.md
git commit -m "docs: track the release skill and teach it changelog fragments"
```

- [ ] **Step 5: Confirm the tracked skill resolves**

Run: `git ls-files .agents/skills/release`
Expected: the four files (`SKILL.md`, `RELEASE-NOTES.md`, `NON-GO-DEPS.md`, `agents/openai.yaml`).

---

## Self-Review

- **Spec coverage:** fragment contract → Tasks 1–2, 4; enforcement → Tasks 1–3, 5; assembly → Task 6; transition (frozen `[Unreleased]`) → Tasks 4, 6; non-goals honored (no generation, no bot, no tool). Covered.
- **Review Focus tests:** unknown/missing type, code-with/without fragment, docs-only, `*.md`-only, ignored names, absent dir, and merge-group/push reporting — the first six are explicit test cases in Tasks 1–2; the last is the always-run job (Task 3) plus the base-computation branch.
- **Type consistency:** `ci/check-changelog.sh [ROOT] [--changed FILE]` is defined in Task 1 and used unchanged in Tasks 2–4; the required check is named `changelog` everywhere.
- **Open edge (accepted):** a release PR that also strengthens tests under `internal/` (from mutation triage) would be "code" and need a fragment; the maintainer adds one. Not special-cased.
