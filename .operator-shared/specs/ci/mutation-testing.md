---
description: Contract for the on-demand, per-package mutation-testing practice (issue #253) — a version-pinned `make mutation` target over gomutants, scoped by default to mutants on lines changed since the last release tag, never a required CI or `make check` gate (an advisory `mutation` workflow runs the sweep on every PR as a signal only); the kill/delete/record triage rule, the reduction-not-elimination stance, and the release-skill/CONTRIBUTING homes for the recurring practice.
read_if: Changing the `make mutation` target, the `GOMUTANTS_VERSION` pin, the triage convention for surviving mutants, the advisory `mutation` CI workflow, or the release-skill verify step; or evaluating a Go mutation-testing tool.
---

# Mutation testing

## Intent

Line coverage proves a test *executes* a line, not that it would *notice* the line changing. A test can run new code
and assert nothing, and generated tests are prone to exactly that. Mutation testing closes the gap: it mutates the code
and checks whether the suite fails. A mutant the suite does not react to ("LIVED") marks either a missing assertion or a
mutation that cannot change behaviour.

This is a **standing, cautious practice**, not a fire drill. The advisory `mutation` workflow sweeps every
code-changing pull request, so tests that guard behaviour are strengthened as they land rather than after a defect
escapes. A release-time sweep over the previous tag is optional — a cross-PR view, not a step the release requires.

## Contract

- **Never a gate, never a score.** A `make mutation` target, invoked by a person on demand (a release-time sweep is
  optional), plus an advisory CI workflow that runs the sweep on every PR. It is **never** wired into `make check`, `make verify`, `make all`,
  or a required CI check: runtime is minutes-to-hours and a LIVED mutant needs human judgement, so a gate would be wrong.
  gomutants has no threshold, so the target exits zero on survivors and the workflow stays green; a red step means the
  sweep (or its reporting) failed, not that mutants survived.
- **Pinned, no global install.** `GOMUTANTS_VERSION` is a `Makefile` constant, invoked as
  `go run github.com/gomutants/gomutants@$(GOMUTANTS_VERSION)`, exactly like `GOVULNCHECK_VERSION`/`vulncheck`.
  Dependabot watches only `gomod` and `github-actions`, so this pin drifts silently and is bumped by hand (see
  `.agents/skills/release/NON-GO-DEPS.md`).
- **Default scope is lines changed since the last release.** `SINCE` defaults to `$(shell git describe --tags
  --abbrev=0)` and is passed as gomutants' `--changed-since`. At release time that ref is the previous release, so the
  default sweep covers exactly this release's changes. The named-package argument stays available.
- **An unresolvable default must fail loud, not silently widen.** If `SINCE` resolves empty (no tags, tagless clone),
  dropping the flag would produce the hours-long full-module run the anti-goals forbid. A full run is opt-in via an
  explicit `FULL=1`, so an empty resolved `SINCE` is an error, never a silent full sweep.
- **The unit of interest is a LIVED mutant on a covered line.** `NOT COVERED` mutants are already the patch-coverage
  check's job and are out of scope here.
- **Triage is a judgement, not a merge gate.** A survivor is closed one of three ways:
  - **Kill it** — strengthen the test so the mutant now fails it. This is the default and the point.
  - **Delete it** — when the mutant exposes a redundant construct (e.g. `strings.HasPrefix(p, "!") && p == "!"`, whose
    first operand `p == "!"` implies), remove the redundancy. Prefer this to recording an equivalent mutant: a line-scoped
    `disable` directive is type-scoped, so it cannot name the redundant operand without also suppressing a killable sibling
    on the same line.
  - **Record it** — if the mutant cannot change behaviour (or is genuinely uninteresting), record it in source next to
    the mutant with a reason: `// gomutants:disable <TYPE> reason="..."`. A project-wide pattern (e.g. logging arguments)
    may use `--exclude-calls` instead. A record is a claim of unobservability, not a shortcut: demonstrate it by hand-applying
    the mutant and trying and failing to make a test fail before recording, then re-sweep the package — a too-eager record hides
    a testable change and can leave the package reading 0 LIVED while survivors remain.
  A survivor left open is tracked debt in the `#268` queue, not a blocked PR. A PR's survivor list is a signal to
  investigate, not a mandate to reach zero. A dedicated reduction pass (`#268`) works the backlog and aims at reduction, not
  elimination. See `guides/mutation-testing.md` for the recurring types and their kills, and
  `guides/mutation-testing.md` for the reduction-pass runbook.

## Tool and invocation

`gomutants` (`github.com/gomutants/gomutants`), pinned at the version in the `Makefile`. Chosen over `gremlins` (mature
but needs `/cmd/gremlins` in the `go run` path, copies the whole module per run, no overlay, no `-race`) and the rest;
see `guides/mutation-testing.md` for the full survey and evidence.

`make mutation` wraps `CGO_ENABLED=1 go run github.com/gomutants/gomutants@$(GOMUTANTS_VERSION)` with knobs:

- `PKG` (default `./...`) — the package argument.
- `SINCE` (default `$(git describe --tags --abbrev=0)`) — passed as `--changed-since`; see the scope contract above.
- `FULL=1` — drops `--changed-since` for the whole-module sweep.
- `ANNOTATIONS=github` — adds `--annotations github` (CI only; empty by default so a local run prints no workflow
  commands).
- `--detect-equivalent` is always on.

The target fails loud when `SINCE` resolves empty and `FULL` is unset, rather than silently widening to the full run.
`CGO_ENABLED=1` mirrors every other test-invoking target: the microsandbox SDK uses cgo, so the inner `go test` needs it.

Facts the design leans on:

- **Module root is the binary**, so `go run github.com/gomutants/gomutants@<ver>` works with no `cmd/` path (verified on
  this VM's Go 1.27.1).
- **`--changed-since <ref>`** runs `git diff --unified=0 --merge-base <ref>` and keeps only mutants on added/modified
  lines. It includes uncommitted edits to tracked files; a **new** file is invisible to the diff until `git add`ed.
  (The flag name is `--changed-since` in the pinned v0.7.3 — an earlier draft of this spec and the survey said `-n`,
  which v0.7.3 rejects; verified by running the pinned tool.)
- **`-detect-equivalent`** recompiles each survivor and marks it `EQUIVALENT` when the assembly is byte-identical to the
  original (trivial compiler equivalence). Enabled by default in the target. `EQUIVALENT` mutants drop out of the efficacy
  denominator, so they need no record.
- **`// gomutants:disable <TYPE> reason="..."`** directive forms (plain / `disable-next-line` / `disable-func` /
  `disable-regexp`) suppress a mutant at its site; suppressed mutants leave every count.
- **Artifacts land in the CWD**: the JSON report at `mutation-report.json` (default) and the incremental cache at
  `.gomutants-cache.json`. The cache is worth keeping — it makes reruns cheap. Both are gitignored.
- **Statuses**: `KILLED`, `LIVED`, `NOT COVERED`, `NOT VIABLE`, `TIMED OUT`, `INFRA ERROR`, `EQUIVALENT`.

## CI signal (advisory)

`.github/workflows/mutation.yml` runs `make mutation` on every PR that can change the code and on manual dispatch.
The contract above makes it advisory: nothing depends on it, and because it lives in its own workflow file it can never
join `ci.yml`'s `release.needs`.

- **A PR scopes to its own changeset.** `SINCE=origin/<base_ref>`, fetched at full depth so gomutants has a merge base;
  the sweep then covers only lines the PR changed, not every unreleased line.
- **A dispatch leaves `SINCE` unset**, so it falls back to the last release tag — the release sweep. Dispatch is only
  available once this workflow is on the default branch (`specs/ci/ci-pipeline.md`).
- **Docs-only PRs are filtered out.** `paths-ignore` on the `pull_request` trigger drops changes that cannot alter which
  mutants exist — `**.md`, `docs/**`, `.operator-shared/**` (the operator brain), `Formula/**`, the issue templates, and
  `.gitignore`. The sweep is about Go code, so paying the coverage pass for a markdown edit is pure waste. A PR that
  starts docs-only and later touches code runs on that push; the filter does not apply to a dispatch.
- **Three surfaces carry the survivors, because the annotation channel caps at 10 warnings per step.**
  - **Inline annotations** (`--annotations github`, set only on the PR path): one `::warning file=,line=,col=::` per LIVED
    mutant on a changed line, rendered on the PR's "Files changed" view. The flag is passed through `make mutation
    ANNOTATIONS=github`; the Makefile variable is empty by default so a local run prints no workflow commands. A dispatch
    sweep skips it — its whole-tree mutants land on lines no diff contains, so the annotations would only clutter the run.
  - **A sticky PR comment**: the uncapped record — the totals and the full survivor list. It is found by an HTML marker
    *and* the bot author, so a human who pastes a report as a new comment is not overwritten; a new push rewrites the
    one comment in place. The list is bounded two ways, because GitHub rejects a comment past 65,536 characters: rows
    are capped at 200, and the assembled body is size-checked, the table dropped in favour of a pointer to the artifact
    if it would not fit. Needs `pull-requests: write` on the job, and is skipped on a fork PR, whose token is read-only.
  - **The step summary and `mutation-report` artifact**: the complete machine-readable record.
- **Even a zero-mutant sweep is not free.** gomutants collects module-wide coverage before it discovers mutants on the
  changed lines, so a code PR whose changed lines carry no mutant still costs ~2 minutes. The `paths-ignore` filter is
  what keeps that cost off a markdown-only PR.
- **A timeout is a wasted run, not a failure.** The job is capped (90 min) so a large changeset cannot tie up a runner, and
  a cancelled or superseded run supplies neither annotations nor a comment.

## Non-goals

- Triaging `NOT COVERED` mutants.

## Anti-goals

- A target so slow or awkward that the release step gets skipped.
- Triage that becomes a rubber stamp: blanket `disable-func`/`disable-regexp` directives that suppress rather than
  strengthen. A recorded reason must name why the mutant cannot change behaviour.
- The unscoped `FULL=1` run becoming the release default — the documented default stays the changed-line sweep.