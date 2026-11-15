# Design: release-branch CI and a tag precondition

Date: 2026-11-14
Status: draft for approval

## Intent

Two gaps in how a release is validated:

1. **A `release/<major>.<minor>` push runs no CI.** `ci.yml`'s
   `on.push.branches` is `[main]`, so a backport merged onto a release branch is
   never checked by the workflow until the patch tag is pushed. The release line
   depends on the tag-triggered run for verification, and nothing runs at the
   merge that introduced the change.
2. **A tag can publish a commit that never passed the required checks.**
   `release.needs` is `[lint, test, integration, integration-podman]` — it omits
   `race`, `deadcode`, and `changelog`, and branch protection's required checks
   exist only in the `protect main` ruleset. A tag on a commit that is not the
   tip of a checked line, or that was pushed directly to an unprotected
   `release/*` branch, publishes without those checks.

Both were surfaced while optimizing `ci.yml`, but they are a separate concern
(release integrity, not runner-minutes) and a separate change on top of the same
PR.

## Goals

- A `release/<major>.<minor>` push runs the same checks a main push runs:
  `lint`, `test`, `race`, `deadcode`, `changelog`, `vulncheck`, and the Docker
  `integration` build.
- A release tag publishes only if the tagged commit is the tip of a checked
  line — `main` or a `release/<major>.<minor>`.
- The single-writer property of the GHCR build cache is preserved: exactly one
  job writes `ghcr.io/...:buildcache`, on a `refs/heads/main` push.

## Non-goals

- No second cache writer; release-branch builds import only.
- No change to the `merge_group` flow or to `main`'s merge queue.
- No cross-workflow `needs` (GitHub has none) — everything stays in `ci.yml`.

## Constraints (verified today)

- `specs/release/release-branching.md`: a patch release tags the **tip of the
  release branch**, not `main`; a minor release tags a commit reachable from
  both. Any "the tag must be an ancestor of `main`" rule is therefore wrong and
  would reject legitimate patch tags.
- `specs/ci/ci-pipeline.md`: the `integration-cache-writer` is the single writer
  (`packages: write` confined there); the reader `integration` is
  `packages: read` and currently runs only on PR/tag/dispatch.
- The writer's ref is the shared registry tag the reader imports
  (`type=registry,ref=ghcr.io/inoio/agents-sandbox:buildcache`).

## Decided design

### 1. Trigger parity

`on.push.branches: [main, 'release/*']`. Every check job that keys off a push
then runs on a release-branch push exactly as on `main`.

### 2. The build on a release-branch push (import-only)

The reader `integration` gains a `push` on `refs/heads/release/` clause (its
`if`, alongside the PR/tag/dispatch clauses), gated on
`needs.changes.outputs.code != 'false'` for parity with `main` — a
docs/brain-only main push skips the build, so a docs/brain-only release push
does too. Its `AGENTS_SANDBOX_IMAGE_CACHE_FROM` already resolves to the cache
ref for a release push (the blanking set is `main` + tags), so it imports the
previous chain. The writer `integration-cache-writer` keeps
`github.ref == 'refs/heads/main'` — unchanged.
So a release push runs the same build as main, reading the same cache, while
exactly one job (main's writer) ever writes it.

### 3. Tag precondition (B)

A new job `tag-guard` runs on tags (`if: startsWith(github.ref, 'refs/tags/')`),
`needs: []`, and fails unless the tagged SHA is an ancestor of `main` or of some
`refs/remotes/origin/release/*`:

- fetch `origin main` and `refs/heads/release/*:refs/remotes/origin/release/*`
  (no tags);
- succeed when `git merge-base --is-ancestor "$GITHUB_SHA" origin/main`, or for
  any `origin/release/*`, that ref is an ancestor;
- else print the reason and exit 1.

`release.needs` gains `tag-guard` (in addition to the existing
`lint, test, integration, integration-podman`), so a tag on an uncheckable
commit never publishes. This directly covers both a stray tag and a patch tag
whose release branch bypassed review.

### 4. Release-branch protection (D)

A `protect release/*` ruleset (a maintainer admin action; the workflow cannot set
it) requires `lint`, `test`, `race`, `deadcode`, `changelog`, `integration (docker
build)` on release branches, mirroring `protect main` without the merge queue.
With the trigger parity from §1 and this ruleset, every commit on a release line
passed the checks, so a `tag-guard`-approved tag is transitively verified.

Consequence: the release tag does not need to re-run `race`/`deadcode` as
blocking checks — `tag-guard` plus the ruleset prove the tree. (Adding
`race`/`deadcode` to `release.needs` remains a possible backstop if a release
branch is ever pushed to without the ruleset; not chosen here.)

### 5. Guard assertions

`ci/check-ci-guards.sh` gains:
- `on.push.branches` includes `release/*`;
- a `tag-guard` job exists, runs on tags, and `release.needs` names it;
- the writer's `ref == 'refs/heads/main'` condition is unchanged (single writer).

`ci/test-check-ci-guards.sh` gains one fixture per assertion.

## What is left to the builder

- The exact `tag-guard` shell (fetch refspec, ancestor loop) and its diagnostics.
- Whether the reader's release-push clause shares the existing `docker login`
  step condition (it must: a same-repo release push may log in; a fork cannot
  push a branch here, so the fork caveat is moot).
- The guard's exact token assertions and fixture mutations.

## Risks

- A release-branch push now runs the ~10-minute cached build, adding minutes to
  backport merges. That is the intended parity the user asked for.
- `tag-guard` adds a fast job to every tag; negligible.
- The `protect release/*` ruleset is manual; until set, `tag-guard` enforces the
  ancestry but not the "was reviewed" property. Documented as the required admin
  step.
