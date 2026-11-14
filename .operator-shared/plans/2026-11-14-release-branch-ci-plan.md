# Release-branch CI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give a `release/<major>.<minor>` push the same CI validation as a main push, and make a release tag publish only from a commit on `main` or a release branch.

**Architecture:** Widen `on.push.branches` to include `release/*`; let the read-only `integration` reader build (cache-importing) on a release-branch push while the single cache writer stays `refs/heads/main`-only; add a fast `tag-guard` job that refuses a tag whose commit is not an ancestor of `main` or some `release/*`, and make `release.needs` include it.

**Tech Stack:** GitHub Actions YAML, Bash (`ci/` scripts + harnesses), GNU Make, actionlint.

**Spec:** `.operator-shared/plans/2026-11-14-release-branch-ci-design.md`

**Base:** the main optimization plan's Task 9 head (branch `ci/build-optimization`).

## Global Constraints

- **Scope:** `.github/workflows/ci.yml`, `ci/check-ci-guards.sh`, `ci/test-check-ci-guards.sh`, `.operator-shared/specs/ci/ci-pipeline.md`, `.operator-shared/specs/release/release-branching.md`, and the existing `changelog.d/<PR>.changed.md` fragment. No Go or product code.
- **The writer is the single writer.** `integration-cache-writer` keeps `github.ref == 'refs/heads/main'`. Do not add a second writer for release branches.
- **No `AGENTS_SANDBOX_IMAGE_CACHE_FROM` change is needed for the reader.** Its blanking set is `ref == 'refs/heads/main' || startsWith(ref, 'refs/tags/')`; a `refs/heads/release/...` push is already outside that set, so `_FROM` resolves to the cache ref. Only the reader's `if:` needs a release-push clause.
- **The tag rule must accept patch tags.** A patch release tags the tip of `release/<x.y>`, not `main` (specs/release/release-branching.md). The guard accepts ancestor-of-`main` OR ancestor-of-any-`origin/release/*`.
- **Required check contexts stay exactly** `lint`, `test`, `race`, `deadcode`, `changelog`, `integration (docker build)`. No job rename.
- `tag-guard` is not a required check; it is a `needs` of `release`.
- Run `make actionlint` after each workflow edit; `make ci-guards-check` after each guard-affecting edit; `make check` before the final commit.

## Review Focus

- A release-branch push must run the **same build** as main and must not create a second cache writer. Pinned by the guard assertion that the writer keeps `refs/heads/main`.
- A patch tag (release-branch tip) must PASS `tag-guard`, and a stray tag (a commit on neither line) must FAIL it. Verify by the manual cases in Task 2's report.
- `release.needs` must name `tag-guard`, or the precondition is advisory. Pinned by the guard in Task 3.

---

## File Structure

- `.github/workflows/ci.yml` — trigger, reader clause, `tag-guard`, `release.needs`.
- `ci/check-ci-guards.sh` — new assertions (release trigger, tag-guard job, release.needs, writer main-only).
- `ci/test-check-ci-guards.sh` — one fixture per new assertion.
- `.operator-shared/specs/ci/ci-pipeline.md`, `.operator-shared/specs/release/release-branching.md` — contract sync.
- `changelog.d/<PR>.changed.md` — extend the existing fragment.

---

### Task 1: release-branch trigger and the (import-only) build

**Files:**
- Modify: `.github/workflows/ci.yml` (`on.push.branches`, the `integration` reader's `if:`)

- [ ] **Step 1: Widen the branch trigger**

Change `on.push.branches: [main]` to:

```yaml
  push:
    branches: [main, 'release/*']
    tags: ["v*"]
```

- [ ] **Step 2: Let the reader build on a release-branch push**

In `integration`'s `if:`, add a release-push disjunct (keep the existing workflow_dispatch / tags / pull_request clauses), gated on `code` for parity with `main` — a docs/brain-only main push skips the build (the writer is gated on `code != 'false'`), so a docs/brain-only release push must skip it too. The tag push still builds uncached, so a release is never left unbuilt:

```yaml
      (github.event_name == 'push' && startsWith(github.ref, 'refs/heads/release/') && needs.changes.outputs.code != 'false')
```

Do not touch the reader's `AGENTS_SANDBOX_IMAGE_CACHE_FROM` (already unblanked for a release push), its `permissions: packages: read`, or its `docker login` step condition. Do not touch `integration-cache-writer` (it stays `refs/heads/main`).

- [ ] **Step 3: Verify and commit**

Run `make actionlint` (exit 0) and `make ci-guards-check` (exit 0).
```bash
git add .github/workflows/ci.yml
git commit -m "ci: run the checks and build on release branches"
```

---

### Task 2: the `tag-guard` job and `release.needs`

**Files:**
- Modify: `.github/workflows/ci.yml` (`tag-guard`, `release.needs`)

**Interfaces:**
- Produces: job id `tag-guard`, which fails a tag whose commit is not on `main` or a release branch.

- [ ] **Step 1: Add `tag-guard`**

Insert after `integration-podman` (before `release`):

```yaml
  tag-guard:
    name: tag-guard
    # A release tag must be on main or a release branch, so a tag can never
    # publish a commit that bypassed the required checks. A patch release tags
    # the tip of release/<major>.<minor>, not main, so both lines are accepted.
    if: startsWith(github.ref, 'refs/tags/')
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - name: The tag must be on main or a release branch
        run: |
          set -euo pipefail
          git fetch --no-tags origin main
          git fetch --no-tags origin '+refs/heads/release/*:refs/remotes/origin/release/*' || true
          if git merge-base --is-ancestor "$GITHUB_SHA" origin/main; then
            echo "tag ${GITHUB_REF_NAME} is on main"
            exit 0
          fi
          while IFS= read -r ref; do
            if git merge-base --is-ancestor "$GITHUB_SHA" "$ref"; then
              echo "tag ${GITHUB_REF_NAME} is on $ref"
              exit 0
            fi
          done < <(git for-each-ref --format='%(refname)' 'refs/remotes/origin/release/')
          echo "::error::tag ${GITHUB_REF_NAME} (${GITHUB_SHA}) is not on main or a release/* branch" >&2
          exit 1
```

- [ ] **Step 2: Gate the release on it**

Change `release`'s `needs` to:

```yaml
    needs: [tag-guard, lint, test, integration, integration-podman]
```

- [ ] **Step 3: Verify and commit**

Run `make actionlint` (exit 0). Confirm `release.needs` names `tag-guard`.
```bash
git add .github/workflows/ci.yml
git commit -m "ci: refuse a release tag that is not on main or a release branch"
```

---

### Task 3: guard the release invariants

**Files:**
- Modify: `ci/check-ci-guards.sh`
- Modify: `ci/test-check-ci-guards.sh`

**Interfaces:**
- Reuses the `job_block`/`job_names` helpers added in the main plan's Task 8.

- [ ] **Step 1: Write the failing fixtures**

Extend the harness's `new_fixture` ci.yml to include a `release/*` branch trigger, the reader's release-push clause, a `tag-guard` job, and `release.needs` naming `tag-guard`. Add one fixture per assertion, each mutating exactly one thing and expecting exit 1 naming it:

- `release/*` removed from `on.push.branches` → FAIL;
- the `tag-guard` job deleted → FAIL;
- `tag-guard` dropped from `release.needs` → FAIL;
- the writer's `github.ref == 'refs/heads/main'` changed (e.g. to `startsWith(github.ref, 'refs/heads/')`) → FAIL.

- [ ] **Step 2: Run the test to verify it fails**

Run `bash ci/test-check-ci-guards.sh`; expect the new fixtures to FAIL (the checker lacks the assertions).

- [ ] **Step 3: Implement the assertions**

Add to `ci/check-ci-guards.sh`:
- assert `on.push.branches` includes `release/*` (parse the `on.push` branch list);
- assert a `tag-guard` job exists (via `job_names`) and its `if:` contains `startsWith(github.ref, 'refs/tags/')`;
- assert `release`'s `needs:` names `tag-guard`;
- assert `integration-cache-writer`'s `if:` contains `github.ref == 'refs/heads/main'`;
- assert the reader `integration`'s `if:` contains `refs/heads/release/` AND `needs.changes.outputs.code != 'false'`.

Each violation names the job and the missing token.

- [ ] **Step 4: Run the test to verify it passes**

Run `bash ci/test-check-ci-guards.sh` (all ok) and `make ci-guards-check` (exit 0).

- [ ] **Step 5: Commit**

```bash
git add ci/check-ci-guards.sh ci/test-check-ci-guards.sh
git commit -m "ci: enforce the release-branch and tag-guard invariants"
```

---

### Task 4: sync the specs and the changelog fragment

**Files:**
- Modify: `.operator-shared/specs/ci/ci-pipeline.md`
- Modify: `.operator-shared/specs/release/release-branching.md`
- Modify: `changelog.d/<PR>.changed.md`

- [ ] **Step 1: Update the CI contract**

In `ci-pipeline.md`: document `on.push.branches` including `release/*`; that the reader `integration` builds (import-only) on a release-branch push while the writer stays `refs/heads/main`; the `tag-guard` job and that `release.needs` includes it.

- [ ] **Step 2: Update the release contract**

In `release-branching.md`: document that a release branch runs the same CI as `main` (a `protect release/*` ruleset requires the same six checks — the maintainer admin step), and that a tag is refused unless it is on `main` or a release branch.

- [ ] **Step 3: Extend the changelog fragment**

Add one bullet: CI now runs on `release/*` branches and a release tag is refused unless it is on `main` or a release branch.

- [ ] **Step 4: Verify and commit**

Run `make changelog-check` (exit 0) and `make check` (all pass; `make fmt` first if it reports drift).
```bash
git add .operator-shared/specs/ci/ci-pipeline.md .operator-shared/specs/release/release-branching.md changelog.d
git commit -m "docs(ci): document release-branch CI and the tag precondition"
```
