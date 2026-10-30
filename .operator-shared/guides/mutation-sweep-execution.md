---
description: Runbook for churning the #268 mutation-survivor tracker down over turns with one coordinator session and non-verifying implementer subagents — the 37 downloaded reports as an immutable FULL-sweep snapshot, file-disjoint units, the handback contract, one PR per turn, and end-of-turn verification by re-sweeping each touched package.
read_if: Coordinating or executing a mutation-survivor reduction pass over #268, or changing the coordinator/subagent split, the queue and cursor format, the handback shape, the landing model, or the verification protocol.
---

# Mutation sweep execution

## Intent

`#268` records 1811 LIVED survivors of 10315 mutants across 36 packages (the 37 attached reports, one per package plus
an empty `notify/assets`). One row is out of the working queue (see *Snapshot caveats*): `internal/agent` (36) was closed
by #259, leaving **1775 survivors across 35 packages**. The reports are the expensive artifact, so the goal is to burn the queue down
**over turns without putting `make mutation` in anyone's inner loop**: a coordinator owns the reports, assignment,
verification and issue text, while implementer subagents only fix. Progress is **reduction, not elimination** — a turn
ends with a smaller, re-verified survivor count, and a human decides whether another turn is worth it.

The closing rule itself lives in `specs/mutation-testing.md` (a survivor is killed, deleted, or recorded) and the
recurring types and their kills in `guides/mutation-triage.md`. This runbook is only the execution around them.

## Inputs

The source data is the **37 JSON reports attached to `#268`**, treated as an immutable snapshot.

- Download with plain unauthenticated `curl -L` (302-redirects). The URLs are the issue's **"Done when" checklist**,
  which is the source of truth for the list; the `#issuecomment-6327799337` link in the prose is dead (404). Reading the
  list from the issue (`gh issue view 268 --json body`) means no hardcoded URL table to rot.
- Verified: 37 files download, and their top-level `mutants_lived` sums to exactly 1811 (10315 mutants).
- Report shape: `{go_module, files: [{file_name, mutations: [{id, type, status, line, column, original, replacement}]}],
  mutants_lived, …}`. A mutant **id** is `<module-path>:<func>:<TYPE>#n` (`<func>` may be empty → `::`), carrying the
  package and file. **`#n` is an ordinal per (file, function, type) in source order, not a stable key** (see
  *Verification*).
- **Scope is a FULL per-package sweep, not changed-line.** Verified against the tree: `./internal/humanize` full-sweeps
  to 34 mutants and the report holds 34, while `--changed-since v0.5.0` yields 0; `yamlfmt` likewise 6/6 vs 0. The
  issue's "default scope `--changed-since v0.5.0`" line is inconsistent with the attachments — reproduce with
  `make mutation FULL=1 PKG=<pkg>`, not the default scope.
- The snapshot is **immutable**. Progress is never written back into it — it lives in the issue text (below).

## Snapshot caveats

- `notify_assets` is an empty report (`files: null`, 0 mutants) — nothing to close.
- The `./cmd/agents-sandbox` row is a real full sweep now (`mutation-report._cmd_agents-sandbox.json`), **184 survivors of
  1190** — the largest concentrated rows are `commands_system.go` (59), `commands_cli.go` (49), `inspect_output.go` (26),
  `tree.go` (26). The earlier `mutation-report-cmd-agents-sandbox.json` (322 ids byte-identical to `internal/agent`'s) was
  a duplicate and is superseded; read the `._cmd_agents-sandbox` attachment.
- Report → package maps by the filename convention (`mutation-report-._internal_sandbox_vm.json` → `./internal/sandbox/vm`).

## Roles

- **Coordinator** — one agent session in a dedicated git worktree. Owns the snapshot, the queue, assignment,
  verification, the `#268` text, and the turn PR. The only writer of the issue.
- **Worker** — an `implementer-small` subagent. **Fixes only.** It never runs `make mutation`, and never runs the
  module-wide `make fmt`/`make lint`/`make check` (see *Worker protocol*). Workers do not self-select work.

## Queue

- The **worker unit** is a source file plus the `_test.go` files that exercise it (a cluster inside one package) — never a
  single file, because a `kill` edits tests and a test-support helper's mutants need tests in the test files. Units are
  **file-disjoint**, which is what lets several workers run without colliding. The **coordinator assigns** each unit;
  workers do not pull from a shared queue, because disjointness depends on assignment and the coordinator cannot
  arbitrate a mid-flight collision.
- **Two workers in one package interfere even when file-disjoint: they share the package's compile and test run.** A
  worker that runs `go test ./pkg/...` while a peer is mid-edit sees the peer's broken intermediate state and reports
  phantom failures (observed in the spike). Either serialize workers within a package — parallelize across packages
  instead — or have each worker re-run at the end and treat mid-run failures as noise.
- **Across packages they still interfere: the module build and the gomutants cache are shared.** The first real turn
  (14 packages, one `implementer-small` per package) hit this hard: one peer's broken in-flight edit
  (`filewalk.go:63: undefined: data`) broke every other worker's `go test` and coverage build, because a changed
  dependency recompiles for all of them; `.gomutants-cache.json` is a single file too. One worker also wandered off its
  unit into another package and reverted that worker's edits. **Run units serially** (one worker; commit each handback,
  then start the next) or give each worker **its own worktree**; do not fan a dozen out in one checkout.
- **A worker's step budget bounds the unit.** The per-survivor evidence loop (hand-apply → run the named test → revert)
  is expensive, and in the first real turn almost every worker exhausted its step budget, several mid-investigation with
  nothing landed. Size a unit to what one worker closes in a session — a file-cluster of **≲10 survivors** — and keep
  the analysis (mutant-type grouping, reachability) inside the same budget, not ahead of it.
- The **package is the verification unit** (see *Verification*), which is coarser than the worker unit. A turn's
  breadth — how many packages it targets — is the main cost knob: verification re-sweeps every touched package, so a
  turn that spreads across all 35 pays for all 35. Prefer concentrating a turn in fewer packages.
- Inside a unit, survivors are ordered **by mutant type**, using `guides/mutation-triage.md` as the playbook: a worker
  that closes ten `ERRORF_WRAP`s back to back applies `errors.Is`/`errors.AsType` once instead of re-deriving it.
- **The order that made turn 3's single-agent sweep work: re-sweep first, drive off the fresh report, batch by type
  within a file, and restructure an order-dependent loop/comparator first.** (1) Re-sweep before working: the attached
  snapshot drifts (its mutant counts and even the functions it names go stale), so the fresh per-package LIVED set is
  the worklist. (2) The **fresh report is the one with mutant types** — a posted residual summary often carries only
  `file:line` and function names, which cannot be batched; take `(file, type, original → replacement)` from the sweep's
  `mutation-report.json` (and, when only counts were posted, the per-package `jq` extracts survive in the session's
  shell-output logs). (3) Batch same-type survivors inside one file. (4) Restructure an order-dependent loop or
  comparator **before** the rest: a pre-sorted loop makes a redundant `sort.Slice` untestable, and a map-ordered loop
  makes a `continue`→`break` non-deterministic, so make the iteration order deterministic and the comparator
  load-bearing (or delete the redundant construct) first — otherwise later test edits are written against masked
  ordering and have to be redone.
- **Max unit size is discovered in the first turns.** A package is not a unit: in `internal/sandbox/vm`, 109 of 310
  survivors sit in `vm_lifecycle.go` and 61 in `reconfig.go`. The first real turn fixed the working figure: **one
  file-cluster of ≲10 survivors per worker** (e.g. `internal/sandbox/doctor`'s eight, all in `testmock.go`, closed
  cleanly; `internal/sysinfo`'s sixteen did not).
- **Durable progress** is the `#268` body's "Survivors by package" table, rewritten once per turn; a per-turn comment
  carries the detail. The in-turn queue, per-unit cursors, and worker dispositions live in a gitignored working file so
  that compaction or a fresh session loses nothing.

## Worker protocol

Given a unit, the worker closes each survivor one of three ways (per `specs/mutation-testing.md`): **kill** it
(strengthen the test), **delete** it (remove the redundant construct), or **record** it
(`// gomutants:disable <TYPE> reason="…"`). It hands back:

- a **disposition per id** — closed-by (`kill`/`delete`/`record`) or deferred with a reason;
- the **evidence** for each close: for a `kill`, the named test that fails once the mutant is applied; for a `record`,
  that applying the mutant left the suite green; for a `delete`, the reason the construct is redundant;
- a **resume cursor** (the last id it closed); and
- the **diff**.

**The prompt carries the mutant's content, not its line.** Snapshot line numbers drift as `main` moves — the spike's
report was 8 lines stale — so hand the worker `file:type:original → replacement` and let it locate the site.

**Only the coordinator writes shared files.** A worker never edits the triage guide, the issue, or another unit's files;
it surfaces a generalizable lesson in its handback and the coordinator folds it in. Two workers editing one shared
document collide exactly like two workers editing one source file — and the spike's worker did wander into
`guides/mutation-triage.md` unprompted.

**Partial returns are expected.** A unit larger than one session stops at its cursor, and the coordinator hands the
remainder to the next worker (or the next turn). The coordinator reuses a worker's session for its next unit until the
session has run long, then rotates to a fresh one — amortizing startup without letting context sprawl.

**Formatting and lint.** A worker formats only the files it touched (`gofmt -w <files>`), which is file-scoped and safe
beside other workers. The module-wide `make fmt`/`make lint`/`make check` runs run in the **coordinator**, serially, at
the turn boundary — a worker's `make fmt` would rewrite a peer's in-flight files, and concurrent `golangci-lint` shares
a cache.

## Review

Every unit's diff gets an adversarial review before acceptance. The `delete` and `record` closes edit production code,
and a green suite hides fragile tests: the spike's review caught a umask-fragile mode assertion (a false failure under
`umask 0077`) and a global `os.Stdout` swap with no restore-on-panic, neither visible from a passing run. Dispatch a fresh
reviewer with the diff and the invariant each deletion leans on, and require a counterexample or a confirmation.

## Landing

**One PR per turn.** Workers share the coordinator's worktree — a subagent inherits the coordinator session's directory, so
moving the coordinator into the worktree puts every worker there (verified) — so disjoint files compose without merges
and the coordinator commits each handback as its own commit on the turn branch, each independently revertable, with no
per-worker worktrees or cherry-picks.

**Turns sequence through `main`.** Turn N+1 branches from `main` **after** turn N is merged; if N is still open, stack
N+1 on N's branch rather than branching from `main`. This matters because verification re-sweeps from the branch's base:
branching from `main` before N merges would re-report N's own fixes as LIVED and regress the `#268` table. The table is
always written from the newest turn's base.

## Coordinator lifecycle

All progress state is external, so the coordinator survives context pressure losslessly:

- **Compaction** is safe. OpenCode auto-compacts near the model's context limit (keeping ~15k recent tokens beside a
  structured summary), and the coordinator may request one deliberately at a safe point:
  `opencode api post /api/session/<id>/compact`. The summary format (objective / next-move / decisions / files) is built
  for handoff, so even dropping the session and starting fresh costs only re-reading the queue.
- **Stops** are sized. The natural end is the **turn boundary** (sweep done, issue updated, PR opened); inside a turn, the
  **cursor** makes any stop resumable.

## Verification

At **end of turn**, for each package the turn touched, re-sweep with the snapshot's scope —
`make mutation FULL=1 PKG=<pkg>` — and join the fresh LIVED mutants to the snapshot's:

- **closed** = a snapshot survivor absent from the fresh LIVED set;
- **residual** = a snapshot survivor still LIVED, left for a later turn.

**Join on mutant identity as a multiset, not on the id.** The `#n` ordinal renumbers when a delete removes a sibling, so
a snapshot id can silently denote a different mutant; do not join on ids, and do not "strip `#n`" either — that key is
even coarser, merging every same-type mutant in a function. Join on `(file, type, original, replacement)`, **by count**:
three identical `t.Helper()` calls share that key, so a set-based comparison would mistake two still-LIVED copies for the
progress of closing the third. Compare the snapshot's per-key count against the fresh count; the difference is closed. A
key whose count reaches zero is fully closed — legitimately, whether we deleted it or an unrelated merge removed it. Do
**not** count a newly-appearing key as a survivor: our dispositions are tests, deletes, and directives.

**The sweep's `Suppressed` count is a cheap self-check.** It counts the `// gomutants:disable` directives that took effect,
so it should equal the unit's `record` closes (the spike's 6 records → `Suppressed: 6`). A `delete` disposition lowers the
package's `mutants_total`, so compare against the snapshot by identity, never by counts.

**Cost, honestly.** The incremental cache keys on the mutant's package **and its covering tests**, so test-only `kill`
dispositions re-run just the affected mutants, while a source edit (`delete`/`record`) invalidates the whole package and
forces a full re-sweep of it. The sweep's own cache is not in the repo, so a fresh worktree starts cold. Budget one
package sweep per touched package, and let that bound a turn's breadth. `-run-mutant-id <id>` re-measures a single
mutant fresh (skipping the cache) for a spot check — safe only while its `#n` has not shifted.

## Turns

One turn = one pass over the queue (or the subset of packages it chose to target), ending at verification + issue update
+ PR. The human reads the reduced totals and decides whether another turn is sensible.

## Not in scope

- Wiring the sweep into `make check`/`verify` as a gate, or making the advisory `mutation.yml` workflow a required check.
  Per `specs/mutation-testing.md` it is on-demand/advisory only.
- Eliminating every survivor. Reduction across turns is the goal.

## Open to the builder

- The unit-size / clustering rule and a turn's package breadth, tuned from the first turn's measured throughput and
  verification cost.
- Whether the in-turn queue and cursors are one file or per-unit files.
- The downloader's home (a `ci/` script plus a `make` target, or a standalone script).
