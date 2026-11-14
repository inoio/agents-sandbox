# Design: an optimized CI build

Date: 2026-11-14
Status: draft for approval

## Intent

Cut CI runner-minutes and keep (or lower) PR/main wait time without weakening any
gate. Two structural levers, both in `.github/workflows/` and `ci/`; no Go or
product code changes.

1. **Stop paying whole-module work for changes that cannot affect it.** Today a
   docs- or brain-only PR still runs `lint`, `test`, `race`, and `deadcode` in
   full. The required-check contexts must not be *skipped* (a skipped required
   check reports Success, which would leave the gate green without running), so
   the fast path must gate steps inside a job that still runs.
2. **Move the whole-image build off the critical path where it is only a cadence
   signal.** The from-scratch build and the podman engine job are correctness
   checks, not per-merge verdicts; a scheduled run gives the same signal without
   costing every merge.

Measured baseline (last day of `ci.yml` runs; wall time is job span, not workflow
span):

| Event | Wall | Critical path | Other jobs |
|---|---|---|---|
| Code PR | 7m21s | `integration` 427s | lint 144s, race 113s, test 88s, deadcode 34s |
| Non-code PR | 2m28s | `lint` 145s | race 111s, test 77s, deadcode 33s |
| Main push | 15m46s | `integration-cache-writer` 927s | podman 847s (parallel) |
| Merge queue | 2m06s | `race` 122s | lint 112s, test 79s, deadcode 33s |

Consequences: on a code PR the only wait-time lever is `integration` (already
cache-tuned; parallelizing it breaks the layer-cache assertions). On a main push
the writer dominates; podman runs under it and adds minutes, not wait. So minutes
and wait time come apart, and the changes below separate cleanly.

## Goals

- A change that touches only the execution-inert paths (see the split below) runs
  every required check but skips golangci-lint, `compile-all`, `validate-fmt`,
  `test`, `race`, and `deadcode`.
- The classifier fails **closed**: an undecidable base or a failed diff runs the
  full set, never a silent skip.
- Every `ci.yml` job carries a bounded `timeout-minutes` (today they inherit
  360m).
- The base-SHA computation lives in one `ci/` helper, guarded, instead of twice in
  `ci.yml`.
- A code-affecting push to `main` imports the GHCR BuildKit cache and re-exports
  it, instead of building cold.
- A scheduled run performs a cold (uncached) Docker image build and the podman
  engine build, so both keep their proof off the merge path.
- The release gate is unchanged: `release.needs` still includes `integration` and
  `integration-podman`.

## Non-goals

- No Go or product code changes; `ci/*.sh` and `.github/workflows/*.yml` only.
- No change to the required-check set or the `protect main` ruleset — the check
  contexts (`lint`, `test`, `race`, `deadcode`, `changelog`,
  `integration (docker build)`) stay exactly as they are.
- No single aggregator job (the "tier 3" option); it collapses the check set,
  needs ruleset surgery, and risks queue latency.
- No reduction in podman's suite coverage or its serialization.
- No attempt to cache podman builds — not reachable through the launcher seam.

## Anti-goals

- Gating a required check by skipping the *job* and relying on Success-on-skip.
- A timeout so tight it flakes a legitimate slow run.
- A separate nightly workflow that drops the podman release gate (GitHub has no
  cross-workflow `needs`).
- Marking `docs/**` execution-inert while a default-suite test reads a docs file.

## Constraints

- **Required checks cannot be conditionally skipped.** `lint`/`test`/`race`/
  `deadcode`/`changelog` are required and always run; `integration` is required
  and relies on Success-on-skip only for `merge_group` and non-code PRs (kept).
  The fast path therefore gates *steps*, and keeps the job running.
- **GitHub has no cross-workflow `needs`.** The podman job must stay in `ci.yml`
  to remain in `release.needs`.
- **Exempt-path lists are hardcoded** in `ci.yml`'s `changes` regex,
  `mutation.yml`/`codeql.yml` `paths-ignore`, and `ci/check-changelog.sh`;
  `ci/check-ci-guards.sh` keeps them in sync. Adding an `exec` set adds a fifth
  site the guard must know about.
- The only default-suite dependency on a non-code path is
  `internal/viperconfig/resolver_flow_test.go` (`TestPortsDocumentedExampleLoads`)
  reading `docs/configuration/networking.md`; `make docs-linkcheck`
  (`ci/check-docs.sh`) reads root markdown, `docs/**`, and Go sources. These
  constrain the inert set (below).

## The inert-set split

The `changes` job emits two booleans instead of one:

- **`code`** (unchanged semantics): the complement of
  `docs/**, .operator-shared/**, Formula/**, .github/ISSUE_TEMPLATE/**,
  .agents-sandbox/**, **.md, .gitignore`. Gates `integration`,
  `integration-cache-writer`, and `vulncheck`.
- **`exec`** (new): true unless *every* changed path is execution-inert for the
  Go-toolchain jobs. Its inert set is `code`'s with exactly one pattern removed —
  `docs/**`. `**.md` stays inert **except** under `docs/`. So the inert set is
  `.operator-shared/**`, `Formula/**`, `.github/ISSUE_TEMPLATE/**`,
  `.agents-sandbox/**`, `.gitignore`, and every `.md` that is not under `docs/`
  (root `README.md`/`AGENTS.md`, `changelog.d/*.md`, …). Only `docs/**` (and any
  path outside the set above) makes `exec` true.

Why the narrower set: a `docs/**` change can fail `test`/`race`
(`TestPortsDocumentedExampleLoads` parses the published-ports example) and
`lint` (`docs-linkcheck` validates it). Everything else in `code`'s inert set is
genuinely inert for all four jobs — no default-suite test reads `.operator-shared`,
`Formula`, `.github/ISSUE_TEMPLATE`, `.agents-sandbox`, or `.gitignore`, and the
`go:embed` assets are `.sh`/`.wav`/`LICENSE`, never `.md`.

Consequence to accept: a `docs/**` change still runs `test`/`race` (the single
default-suite docs read above) and always runs `lint`'s `docs-linkcheck`. A root
`README.md`/`AGENTS.md` or `changelog.d/*.md` change gets the fast path: it skips
the Go jobs but still runs `docs-linkcheck` (which reads root markdown) because
that step is not `exec`-gated. If `TestPortsDocumentedExampleLoads` is later moved
out of the default suite, `docs/**` can join the `exec` inert set.

## Approaches

**A. Step-level fast path inside the existing jobs (chosen).** `changes` gains an
`exec` output; `lint`/`test`/`race`/`deadcode` gain `needs: [changes]` +
`if: !cancelled()` and gate their heavy steps on `exec`. Required contexts are
unchanged, the jobs still run (so success is real, not a skip), and the split is
enforced by `ci/check-ci-guards.sh`. Costs: the four jobs now start after
`changes` (~10s), and `lint` must keep its non-Go steps (notably `docs-linkcheck`)
running always.

**B. Single required aggregator (rejected).** Make the expensive checks
non-required and add one required `verify` job that inspects
`needs.<job>.result` and fails when a needed check was skipped. Removes the
skip=success hazard outright, but collapses six required contexts into one, needs
ruleset surgery, and adds a failure mode where a `needs`-graph mistake makes the
aggregator vacuously pass.

**C. One workflow per concern (rejected).** Move `test`/`race`/`deadcode` into
path-filtered workflows. A workflow filtered out by `paths-ignore` leaves its
checks Pending and blocks the PR, and the podman job cannot leave `ci.yml`
without dropping `release.needs`.

## Decided design

### 1. Classifier: two outputs, fail-closed

- `changes` computes `base` via the shared helper (below); on **any** undecidable
  base or failed `git diff` it emits `code=true` and `exec=true` and exits 0. It
  emits `false` only when the diff succeeded and every path matched the relevant
  inert pattern. The current `git diff … || true` swallow (which turns a failed
  diff into an empty file list, and empty into `code=false`) is removed.
- **Decidability contract.** The helper returns a base SHA only when it can prove
  one: a push whose `before` is a real commit; a pull_request whose base-branch
  merge base resolves. Everything else is *undecidable*: `schedule` and
  `workflow_dispatch` (no `before` in the payload), a branch-creation or
  force-push whose `before` is the zero/absent SHA, and a failing base fetch. The
  current `before`-not-a-commit → `HEAD^` fallback is **removed**, because on a
  schedule or dispatch it would classify the whole run by the single last commit
  and could skip the Go jobs on the very nightly that exists to run them. So a
  schedule, a dispatch, or an unusual push runs the full set (fail-closed).
- Consumers test the *negation*: heavy steps run when the output `!= 'false'`
  (so an empty output — a failed or skipped `changes` — runs them). The job-level
  condition is `!cancelled()` (not `always()`), so a failed `changes` dependency
  does not skip the job (fail-closed) but a cancelled run's jobs do not start.

### 2. Fast path in `lint`/`test`/`race`/`deadcode`

- Each gains `needs: [changes]` and `if: ${{ !cancelled() }}`.
- `test`/`race`/`deadcode` gate their single body step:
  `if: github.event_name == 'merge_group' || needs.changes.outputs.exec != 'false'`.
- `lint` gates only the Go-toolchain steps (`golangci-lint-action`,
  `make compile-all`, `make validate-fmt`); it keeps `docs-linkcheck`,
  `docs-linkcheck-test`, the script tests, `actionlint`, and `ci-guards-*` running
  always (cheap, and `docs-linkcheck` is the docs change's own validator).
- `merge_group` always forces the full set (the queue exists to run these).
- The `code` consumers get the same fail-closed shape: `integration` and
  `integration-cache-writer` change `needs.changes.outputs.code == 'true'` to
  `!= 'false'` and wrap their existing condition in `!cancelled()`, so a failed
  `changes` job cannot silently skip the required `integration (docker build)`
  check (it would otherwise report Success-on-skip). `vulncheck` likewise.

### 3. timeouts

Add `timeout-minutes` to every `ci.yml` job; grounded in observed max × ~2–4:

| job | observed max | timeout |
|---|---|---|
| changes | 13s | 5m |
| changelog | 14s | 5m |
| lint | 145s | 15m |
| test | 88s | 15m |
| race | 122s | 15m |
| deadcode | 41s | 15m |
| vulncheck | 23s | 15m |
| integration | 427s (cached) | 30m |
| integration-cache-writer | 927s (cold) | 30m |
| integration-podman | 847s | 60m, with `INTEGRATION_TIMEOUT` 120m → 45m |
| integration-fresh (new) | ~927s expected | 30m |
| release (tag) | n/a in window | 30m |
| publish | n/a | 15m |

`integration-podman`'s test-level `INTEGRATION_TIMEOUT` drops from 120m to 45m so
a job timeout can actually bound a hung run; 45m is >3× the observed 14m.

### 4. Base-SHA helper

- New `ci/event-base.sh`: prints the base SHA for the event (push `before`,
  pull_request merge-base with the base branch, `merge_group` `base_sha`), or
  exits non-zero when undecidable. It does **not** decide the fallback policy —
  callers do.
- `changes` uses it and treats failure as `code=true`/`exec=true`; `changelog`
  uses it and keeps its existing shape-only degradation on failure. `mutation.yml`
  resolves a base *ref* for `--changed-since`, not a merge-base SHA, so it is not
  a caller.
- `ci/check-ci-guards.sh` gains an assertion that both `ci.yml` jobs call the
  helper (rather than re-inlining the block), plus an `exec`-set check (below).
- The helper and its test follow the `ci/check-*.sh` + `ci/test-check-*.sh`
  precedent.

### 5. Writer imports the cache

- `integration-cache-writer` sets `AGENTS_SANDBOX_IMAGE_CACHE_FROM` to the same
  `type=registry,ref=ghcr.io/inoio/agents-sandbox:buildcache` the reader uses, and
  keeps `_TO` at `…,mode=max`. A code-affecting main push then imports the
  previous chain and re-exports only changed layers.
- `packages: write` stays confined to this job (it is the single writer; the
  reader stays `packages: read`).
- Main no longer proves a cold build; that proof moves to the scheduled fresh job
  and the (already uncached) release tag. Every layer whose instruction changed is
  still a miss and rebuilt, so main still exercises new content cold.

### 6. Scheduled fresh build + podman cadence

- `ci.yml` gains a `schedule:` trigger (a cron; a separate workflow cannot keep
  `release.needs` on podman).
- New job `integration-fresh`: runs only on `schedule`, `needs: []`,
  `make test-integration` with `AGENTS_SANDBOX_IMAGE_CACHE_FROM=''` and
  `AGENTS_SANDBOX_IMAGE_CACHE_TO=''` (no export, so it cannot race the writer's
  ref); timeout 30m. It carries `GH_TOKEN: ${{ github.token }}` like its siblings,
  because it makes the same `api.github.com` release-resolution queries and would
  otherwise hit the anonymous 60-requests/hour limit; it needs no `packages` scope
  (it does not log in or import). It re-resolves current upstream agent releases,
  so upstream drift surfaces nightly instead of at the next release tag.
- `integration-podman`: drop the `github.ref == 'refs/heads/main' && code == true`
  clause; add `github.event_name == 'schedule'`. It keeps tag, `workflow_dispatch`,
  and the `run-integration-podman` PR label, so the release gate and the PR opt-in
  survive. On a main push it no longer runs.
- The required jobs also run on the schedule (a schedule triggers the whole
  workflow); that is a small, acceptable extra nightly run of already-fast checks.
- Result: main push wall drops to the cached writer (~7m) from ~15.7m.

### 7. Guard updates

- `ci/check-ci-guards.sh` currently derives the exempt set from the single
  `changes` `grep -vqE`. It must derive **both** sets, assert the `code` set across
  the existing four sites as today, and assert the `exec` set equals the `code` set
  with `docs/**` removed (the `**.md`-except-`docs/` rule). `ci/test-check-ci-guards.sh`
  gains fixtures for each, including a `docs/foo.md` path that is `code`-inert but
  `exec`-relevant.
- The `github.event_name` and `on.pull_request.types` invariants are unaffected
  (`schedule` joins the declared events; `labeled` is still required for the
  podman opt-in).

## What is left to the builder

- The exact `exec` derivation (a single `grep` cannot do the "`.md` unless
  `docs/`" rule without a lookahead; use `awk` or two `grep -v` passes) and how
  the guard models it.
- `ci/event-base.sh`'s interface (env inputs vs argv) and its test cases.
- The precise step-level `if:` expressions, including how `!cancelled()` composes
  with `needs` on each job.
- The cron time for the nightly (avoid the existing `codeql` `27 4 * * 1` and
  `fuzz` `17 3 * * *` slots).
- Whether `mutation.yml`/`codeql.yml` need any change (expected: none — their
  path filters are the `code` set and stay).

## Risks

- `needs: [changes]` adds ~10s to four jobs' start on a code PR; the code-PR wall
  is still integration-bound, so this is invisible there.
- Fail-closed means a broken classifier runs (and pays for) everything; that is
  the intended direction.
- The docs→`test` coupling is load-bearing for the inert set; a future move of
  `TestPortsDocumentedExampleLoads` out of the default suite is the trigger to
  widen the inert set, and the coupling should be noted where that test lives.
- Podman leaves the main-push path, so a podman-only regression lands at the
  nightly (or the next tag) rather than at merge. This is the accepted trade for
  the wait-time reduction.
