# CI build optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut CI runner-minutes and lower main-push wait time without weakening any gate, by gating the Go-toolchain work on an `exec`-inert classifier and moving the cold image build plus the podman engine job to a nightly.

**Architecture:** A fail-closed `changes` job emits two booleans, `code` (unchanged semantics) and `exec` (true unless every changed path is execution-inert); `lint`/`test`/`race`/`deadcode` stay always-run required checks but gate their heavy *steps* on `exec`. A code-affecting main push imports the GHCR BuildKit cache and re-exports it; a `schedule` trigger adds a cold `integration-fresh` build and moves `integration-podman` off the main push while keeping `release.needs`.

**Tech Stack:** GitHub Actions YAML, Bash (`ci/` scripts + `ci/test-check-*.sh` harnesses), GNU Make, actionlint.

**Spec:** `.operator-shared/plans/2026-11-14-ci-build-optimization-design.md`

## Global Constraints

- **Scope:** `.github/workflows/ci.yml`, `ci/*.sh`, `Makefile`, `scripts/changed-targets.sh`, `.operator-shared/specs/ci/*.md`, `CONTRIBUTING.md`, `changelog.d/*`. No Go or product code changes.
- **Required check contexts stay exactly:** `lint`, `test`, `race`, `deadcode`, `changelog`, `integration (docker build)`. Do not add or remove a required check.
- **Required checks are never skipped.** Gate *steps*, never a required job's `if`. A job-level `if` uses `!cancelled()`, never `always()`.
- **`code` inert set (verbatim):** `docs/**`, `.operator-shared/**`, `Formula/**`, `.github/ISSUE_TEMPLATE/**`, `.agents-sandbox/**`, `**.md`, `.gitignore`.
- **`exec` inert set = `code` inert set minus `docs/**`** (a `.md` is inert unless under `docs/`).
- **Fail-closed:** a base SHA is decidable only for (a) a `push` whose `before` is a real commit, (b) a `pull_request` whose base-branch merge base resolves, (c) a `merge_group` (its `base_sha`). Anything else — `schedule`, `workflow_dispatch`, a branch-creation/force-push `before`, a failed fetch — is undecidable and runs the full set. Remove the `HEAD^` fallback.
- **Timeouts (`timeout-minutes`, observed max × ~2–4):** `changes` 5, `changelog` 5, `lint` 15, `test` 15, `race` 15, `deadcode` 15, `vulncheck` 15, `integration` 30, `integration-cache-writer` 30, `integration-fresh` 30, `integration-podman` 60 (with `INTEGRATION_TIMEOUT` 120m → 45m), `release` 30, `publish` 15.
- **podman stays in `ci.yml`** so `release.needs` still gates on it.
- Run `make actionlint` after each workflow edit and `make check` before the final commit.

## Review Focus

- A `docs/**` change must **not** skip `test`/`race`: `TestPortsDocumentedExampleLoads` reads `docs/configuration/networking.md`. A reasonable person would expect a docs edit to be cheap; here it must still run the Go suite. Pinned by the guard fixture in Task 8.
- A failed or undecidable `changes` job must still let the required `integration (docker build)` run (no silent Success-on-skip). A reasonable person expects a broken classifier to be loud, not to disable the gate. Pinned by the guard assertions in Task 8.
- A `merge_group` must run `lint`/`test`/`race`/`deadcode` in full — the queue's whole point. Pinned by the guard assertion that the step conditions name `merge_group`.
- A `schedule`/`workflow_dispatch` must run the full set, not be classified by the last commit. Pinned by `ci/event-base.sh`'s tests (Task 1).
- A fork PR must still build uncached and must not fail on the GHCR login. Existing behavior, unchanged by this plan; verify by review of the untouched fork conditions.

---

## File Structure

- `ci/event-base.sh` — new. Prints the base SHA for the event, or exits non-zero.
- `ci/test-check-event-base.sh` — new. Its fixture harness.
- `ci/check-ci-guards.sh` — extended. Adds the `exec`/fast-path/fail-closed invariants.
- `ci/test-check-ci-guards.sh` — extended. Fixtures for each new invariant.
- `Makefile` — new `event-base-test` target, added to `CHECK_TARGETS`.
- `scripts/changed-targets.sh` — map `ci/event-base.sh` to its targets.
- `.github/workflows/ci.yml` — the bulk: `changes` two outputs; `changelog` helper; fast path; fail-closed consumers; timeouts; writer cache import; `schedule`; `integration-fresh`; podman cadence.
- `.operator-shared/specs/ci/ci-pipeline.md`, `.operator-shared/specs/ci/quality-gates.md` — contract sync.
- `CONTRIBUTING.md` — Testing/labels note if any.
- `changelog.d/<PR>.changed.md` — the fragment.

---

### Task 1: `ci/event-base.sh` and its test harness

**Files:**
- Create: `ci/event-base.sh`
- Create: `ci/test-check-event-base.sh`
- Modify: `Makefile` (target + `CHECK_TARGETS`)
- Modify: `scripts/changed-targets.sh`

**Interfaces:**
- Produces: `ci/event-base.sh` — reads `GITHUB_EVENT_NAME`, `GITHUB_SHA`, `GITHUB_BASE_REF`, `GITHUB_BEFORE`, `GITHUB_MERGE_GROUP_BASE_SHA` from the environment; prints the base SHA to stdout and exits 0, or prints a diagnostic to stderr and exits 1. Operates on the git repo at the current directory.

- [ ] **Step 1: Write the failing test**

In `ci/test-check-event-base.sh` (same `ok`/`bad`/`pass`/`fail` shape as `ci/test-check-changelog.sh`), create a temp repo fixture and cover:

```sh
# fixture: a git repo with two commits on `main`
#   A -> B, and a branch `feature` forked at B with commit C
# (set user.email/user.name via `git -c` so the fixture needs no global config)

# push with a real before
run_event push GITHUB_BEFORE="$A" GITHUB_SHA="$B"  expect_stdout "$A"

# push whose before is absent / the zero SHA
run_event push GITHUB_BEFORE="0000000000000000000000000000000000000000" GITHUB_SHA="$B"  expect_exit 1

# schedule / dispatch
run_event schedule  GITHUB_SHA="$B"  expect_exit 1
run_event workflow_dispatch GITHUB_SHA="$B"  expect_exit 1

# merge_group
run_event merge_group GITHUB_MERGE_GROUP_BASE_SHA="$B"  expect_stdout "$B"
run_event merge_group GITHUB_MERGE_GROUP_BASE_SHA=""   expect_exit 1

# pull_request: merge-base of the base branch and the head
run_event pull_request GITHUB_BASE_REF=main GITHUB_SHA="$C"  expect_stdout "$B"
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `bash ci/test-check-event-base.sh`
Expected: FAIL — `ci/event-base.sh` does not exist.

- [ ] **Step 3: Implement `ci/event-base.sh`**

`#!/usr/bin/env bash`, `set -euo pipefail`, a `case "$GITHUB_EVENT_NAME"` on `pull_request` / `push` / `merge_group` / `*`:
- `pull_request`: `git fetch --no-tags origin "refs/heads/$GITHUB_BASE_REF:refs/remotes/origin/$GITHUB_BASE_REF"` then `git merge-base "origin/$GITHUB_BASE_REF" "$GITHUB_SHA"`.
- `push`: print `$GITHUB_BEFORE` only if `git cat-file -e "${GITHUB_BEFORE}^{commit}"` succeeds; else `exit 1`.
- `merge_group`: print `$GITHUB_MERGE_GROUP_BASE_SHA` if non-empty; else `exit 1`.
- `*`: `exit 1`.

A header comment states the contract and that callers pick the fallback (`changes` fails closed; `changelog` degrades to shape-only), pointing at `specs/ci/ci-pipeline.md`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `bash ci/test-check-event-base.sh`
Expected: all cases `ok`, exit 0.

- [ ] **Step 5: Wire it into `make` and the diff map**

Add to `Makefile` beside the other `*-test` targets:

```make
# Test the CI base-SHA resolver itself (fixtures only: never touches this repo).
event-base-test:
	./ci/test-check-event-base.sh
```

Append `event-base-test` to `CHECK_TARGETS`. In `scripts/changed-targets.sh`, add a case:

```sh
ci/event-base.sh|ci/test-check-event-base.sh) add_targets event-base-test ci-guards-check; known=1 ;;
```

- [ ] **Step 6: Run the target and commit**

Run: `make event-base-test` → pass.
```bash
git add ci/event-base.sh ci/test-check-event-base.sh Makefile scripts/changed-targets.sh
git commit -m "ci: add a fail-closed base-SHA resolver"
```

---

### Task 2: Rewire the `changes` and `changelog` jobs

**Files:**
- Modify: `.github/workflows/ci.yml` (`changes`, `changelog`)

**Interfaces:**
- Consumes: `ci/event-base.sh` (Task 1).
- Produces: `changes` outputs `code` and `exec` (strings `"true"`/`"false"`), steps id `filter`.

- [ ] **Step 1: Replace the `changes` job body**

Keep `if: github.event_name != 'merge_group'`, `runs-on`, `actions/checkout@v7` with `fetch-depth: 0`. Add `timeout-minutes: 5` and the second output:

```yaml
    outputs:
      code: ${{ steps.filter.outputs.code }}
      exec: ${{ steps.filter.outputs.exec }}
```

Replace the step with `id: filter`, `name: Classify the changeset`, `env:` passing `GITHUB_EVENT_NAME`, `GITHUB_SHA`, `GITHUB_BASE_REF`, `GITHUB_BEFORE`, `GITHUB_MERGE_GROUP_BASE_SHA` from the `github.*` contexts, and this `run:`:

```bash
set -euo pipefail
code=false
exec=false
if base=$(ci/event-base.sh) && files=$(git diff --name-only "$base" "$GITHUB_SHA"); then
  # The `code` set is the complement of mutation.yml/codeql.yml's paths-ignore and
  # check-changelog.sh's presence rule; keep the four copies in sync
  # (ci/check-ci-guards.sh).
  if printf '%s\n' "$files" | grep -vqE '^(docs/|\.operator-shared/|Formula/|\.github/ISSUE_TEMPLATE/|\.agents-sandbox/)|\.md$|^\.gitignore$'; then
    code=true
  fi
  # `exec` is `code` with docs/** removed: a docs/ path is inert for the image build
  # but not for the Go jobs (a default-suite test reads docs/configuration/networking.md).
  if [ "$code" = true ] || printf '%s\n' "$files" | grep -qE '^docs/'; then
    exec=true
  fi
else
  # Fail closed: an undecidable base or a failed diff runs everything.
  code=true
  exec=true
fi
echo "code=$code" >> "$GITHUB_OUTPUT"
echo "exec=$exec" >> "$GITHUB_OUTPUT"
```

Do **not** keep any `git diff … || true` swallow.

- [ ] **Step 2: Point `changelog` at the helper**

Replace its inline base block with:

```bash
base=$(ci/event-base.sh || true)
git diff --name-only "${base:-}" "$GITHUB_SHA" > /tmp/changed.txt 2>/dev/null || true
```

This preserves its existing shape-only degradation; add `timeout-minutes: 5` to the job.

- [ ] **Step 3: Verify and commit**

Run: `make actionlint` → exit 0. Run: `make ci-guards-check` → still passes (the `code` regex is unchanged).
```bash
git add .github/workflows/ci.yml
git commit -m "ci: classify changes into code/exec outputs, fail closed"
```

---

### Task 3: Fast path in `lint`/`test`/`race`/`deadcode`

**Files:**
- Modify: `.github/workflows/ci.yml` (`lint`, `test`, `race`, `deadcode`)

**Interfaces:**
- Consumes: `changes` outputs `exec` (Task 2).

- [ ] **Step 1: Add `needs` + `if` to the four jobs**

Each of `lint`, `test`, `race`, `deadcode` gains:

```yaml
    needs: [changes]
    if: ${{ !cancelled() }}
```

- [ ] **Step 2: Gate the Go-toolchain steps**

In `lint`, add to the `golangci/golangci-lint-action@v9` step, the `make compile-all` step, and the `make validate-fmt` step:

```yaml
    if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}
```

Leave `make docs-linkcheck`, `make docs-linkcheck-test`, `make ci-guards-check`, `make ci-guards-check-test`, `make actionlint`, `make cleanup-branches-test`, `make envelope-test`, `make upstream-drift-test`, `make rebase-status-test` unconditional.

- [ ] **Step 3: Gate the `test`/`race`/`deadcode` body steps**

On `make coverage-junit` (and its two Codecov steps), `make test-race`, and `make deadcode`, add:

```yaml
    if: ${{ github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false' }}
```

Keep `actions/checkout@v7` and `actions/setup-go@v7` unconditional (cheap; keeps the job a real run).

- [ ] **Step 4: Verify and commit**

Run: `make actionlint` → exit 0. Confirm every required job still has no condition that can be false on an event where it must run.
```bash
git add .github/workflows/ci.yml
git commit -m "ci: skip Go-toolchain work for execution-inert changes"
```

---

### Task 4: Fail-closed `code` consumers

**Files:**
- Modify: `.github/workflows/ci.yml` (`integration`, `integration-cache-writer`, `vulncheck`)

**Interfaces:**
- Consumes: `changes` output `code` (Task 2).

- [ ] **Step 1: `integration` (reader)**

Change its `if:` to test the negation and survive a failed `changes`:

```yaml
    if: >-
      !cancelled() &&
      (github.event_name == 'workflow_dispatch' ||
      startsWith(github.ref, 'refs/tags/') ||
      (github.event_name == 'pull_request' && needs.changes.outputs.code != 'false'))
```

- [ ] **Step 2: `integration-cache-writer`**

```yaml
    if: >-
      !cancelled() &&
      github.event_name == 'push' &&
      github.ref == 'refs/heads/main' &&
      needs.changes.outputs.code != 'false'
```

- [ ] **Step 3: `vulncheck`**

```yaml
    if: >-
      !cancelled() &&
      github.event_name != 'merge_group' &&
      (github.event_name == 'workflow_dispatch' ||
      startsWith(github.ref, 'refs/tags/') ||
      needs.changes.outputs.code != 'false')
```

- [ ] **Step 4: Verify and commit**

Run: `make actionlint` → exit 0.
```bash
git add .github/workflows/ci.yml
git commit -m "ci: fail closed when the changes gate is undecidable"
```

---

### Task 5: `timeout-minutes` on every `ci.yml` job

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Add the values from the Global Constraints table**

`changes` 5, `changelog` 5 (if not already added in Task 2), `lint` 15, `test` 15, `race` 15, `deadcode` 15, `vulncheck` 15, `integration` 30, `integration-cache-writer` 30, `integration-podman` 60 (and change its `INTEGRATION_TIMEOUT` from `120m` to `45m`), `release` 30, `publish` 15.

- [ ] **Step 2: Verify and commit**

Run: `make actionlint` → exit 0. Confirm no `ci.yml` job lacks `timeout-minutes`.
```bash
git add .github/workflows/ci.yml
git commit -m "ci: bound every job with a timeout"
```

---

### Task 6: Writer imports the cache

**Files:**
- Modify: `.github/workflows/ci.yml` (`integration-cache-writer`)

- [ ] **Step 1: Set `_FROM` to the reader's ref**

```yaml
      AGENTS_SANDBOX_IMAGE_CACHE_FROM: 'type=registry,ref=ghcr.io/inoio/agents-sandbox:buildcache'
```

Leave `_TO` at `type=registry,ref=ghcr.io/inoio/agents-sandbox:buildcache,mode=max`. Update the env comment: the writer now imports the previous chain and re-exports only changed layers; a cold build is validated by `integration-fresh` (Task 7) and the release tag.

- [ ] **Step 2: Verify and commit**

Run: `make actionlint` → exit 0.
```bash
git add .github/workflows/ci.yml
git commit -m "ci: import the build cache on main and re-export changed layers"
```

---

### Task 7: `schedule`, `integration-fresh`, and podman cadence

**Files:**
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Produces: job id `integration-fresh`.

- [ ] **Step 1: Add the trigger**

Add to `on:` (after `workflow_dispatch:`):

```yaml
  schedule:
    # The cold-image and podman validations, off the merge path. Runs from the
    # default branch only.
    - cron: "43 2 * * *"
```

(Chosen so it does not collide with `codeql`'s `27 4 * * 1` or `fuzz`'s `17 3 * * *`.)

- [ ] **Step 2: Add `integration-fresh`**

```yaml
  integration-fresh:
    name: integration (docker build, fresh)
    # A scheduled cold build: no cache, so it proves the runner image builds from
    # scratch against today's upstream agent releases. It exports nothing, so it
    # cannot race the writer's cache tag.
    if: github.event_name == 'schedule'
    runs-on: ubuntu-latest
    timeout-minutes: 30
    env:
      GH_TOKEN: ${{ github.token }}
      AGENTS_SANDBOX_IMAGE_CACHE_FROM: ''
      AGENTS_SANDBOX_IMAGE_CACHE_TO: ''
      AGENTS_SANDBOX_CACHE_REPORT: ${{ github.workspace }}/runner-image-cache-report.txt
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: .go-version
          cache: true
      - run: make test-integration
      - name: Runner-image build cache report
        if: always()
        run: cat "$AGENTS_SANDBOX_CACHE_REPORT" 2>/dev/null || echo "no cache report"
```

- [ ] **Step 3: Move podman off the main push**

In `integration-podman`'s `if:`, delete the `(github.ref == 'refs/heads/main' && needs.changes.outputs.code == 'true')` clause and add `github.event_name == 'schedule' ||`. Keep tag, `workflow_dispatch`, and the `run-integration-podman` label. Update the job comment.

- [ ] **Step 4: Verify and commit**

Run: `make actionlint` → exit 0. Run: `make ci-guards-check` → passes (`schedule` is now declared, so the new `event_name == 'schedule'` comparison is legal).
```bash
git add .github/workflows/ci.yml
git commit -m "ci: add a nightly fresh build and move podman off the main push"
```

---

### Task 8: Extend `ci/check-ci-guards.sh` and its test

**Files:**
- Modify: `ci/check-ci-guards.sh`
- Modify: `ci/test-check-ci-guards.sh`

**Interfaces:**
- The checker keeps its existing exit contract (non-zero on any violation).
- It reads the `changes` `run:` to derive the `code` regex (unchanged mechanism) and asserts the new invariants below.

- [ ] **Step 1: Add the failing fixtures**

In `ci/test-check-ci-guards.sh`, extend `new_fixture`'s `ci.yml` to the new `changes` shape (two outputs, `ci/event-base.sh`, the `grep -qE '^docs/'` line, `needs: [changes]` on the four jobs, `merge_group` in the body-step conditions, `!= 'false'` on the consumers, `timeout-minutes` on every job, an `integration-fresh` job). Then add one fixture per assertion, each mutating exactly one thing and expecting a FAIL naming it:

- the `exec` derivation's `grep -qE '^docs/'` anchor removed → FAIL;
- `git diff … || true` reintroduced in `changes` → FAIL;
- a `code` consumer back to `== 'true'` → FAIL;
- `merge_group` dropped from a body-step condition → FAIL;
- a job's `timeout-minutes` removed → FAIL;
- `ci/event-base.sh` replaced by an inline `git merge-base` → FAIL.

- [ ] **Step 2: Run the test to verify it fails**

Run: `bash ci/test-check-ci-guards.sh`
Expected: FAIL — the checker does not yet emit these violations.

- [ ] **Step 3: Implement the assertions**

Add to `ci/check-ci-guards.sh` a section that scans `CI_YML`:
- assert the `changes` `run:` calls `ci/event-base.sh` and contains no `git diff` line ending in `|| true`;
- assert the `changelog` job also calls `ci/event-base.sh` (neither job re-inlines the merge-base block);
- assert the `changes` `run:` contains `grep -qE '^docs/'` (the `exec` rule cannot be dropped) and that `exec` is derived from `code` (the `[ "$code" = true ]` guard);
- assert each of `lint`/`test`/`race`/`deadcode` has `needs:` including `changes`;
- assert the `test`/`race`/`deadcode` body steps' `if:` contains `merge_group` and `outputs.exec != 'false'`;
- assert the `integration`/`integration-cache-writer`/`vulncheck` `if:` contains `outputs.code != 'false'`;
- assert every job has `timeout-minutes`.

Keep the existing exempt-set, `github.event_name`, and `on.pull_request.types` checks. Each new violation names the job and the missing token.

- [ ] **Step 4: Run the test to verify it passes**

Run: `bash ci/test-check-ci-guards.sh` → all `ok`. Run: `make ci-guards-check` → exit 0.

- [ ] **Step 5: Commit**

```bash
git add ci/check-ci-guards.sh ci/test-check-ci-guards.sh
git commit -m "ci: enforce the fast-path and fail-closed workflow invariants"
```

---

### Task 9: Sync the specs, CONTRIBUTING, and the changelog fragment

**Files:**
- Modify: `.operator-shared/specs/ci/ci-pipeline.md`
- Modify: `.operator-shared/specs/ci/quality-gates.md`
- Modify: `CONTRIBUTING.md` (only if a label/Testing line is now wrong)
- Create: `changelog.d/<PR-number>.changed.md`

- [ ] **Step 1: Update the CI contract**

In `ci-pipeline.md`: document the two-output `changes` job and the `exec` inert set, the step-level fast path (why the jobs still run), the fail-closed decidability contract and the removed `HEAD^` fallback, the `ci/event-base.sh` helper, the `timeout-minutes` set, the writer's cache import, the `schedule` trigger with `integration-fresh`, and podman's moved cadence (still in `release.needs`). In `quality-gates.md`: note the new guard invariants and the `event-base-test` target. Confirm `mutation.yml` and `codeql.yml` are unchanged: their `paths-ignore` lists are the `code` set and stay as they are.

- [ ] **Step 2: Add the changelog fragment**

`changelog.d/<PR-number>.changed.md` — one bullet summarizing the CI changes. Use the PR number once it exists (see the fragment contract).

- [ ] **Step 3: Verify and commit**

Run: `make changelog-check` → exit 0. Run: `make check` → all targets pass (this is the finalize gate; run `make fmt` first if it reports drift).
```bash
git add .operator-shared/specs/ci/ci-pipeline.md .operator-shared/specs/ci/quality-gates.md CONTRIBUTING.md changelog.d
git commit -m "docs(ci): document the optimized build pipeline"
```
