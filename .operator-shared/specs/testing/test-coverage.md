---
description: Contract for the coverage bar on added and changed lines — every added line must be executed by a test (including error, transport, and status branches), a branch unreachable by construction must be removed rather than left uncovered, and a red `codecov/patch`/`codecov/project` check is a real finding to classify Important, not Minor. Records that the numeric thresholds live in the repo's `.codecov.yml`, that codecov is not a required status check on `main`, and that its per-line report is anchored to the head merged with the base branch (so a branch behind `main` shows a spurious patch miss).
read_if: Adding or changing production code and deciding what to test, reviewing a diff for coverage gaps, or triaging a failing `codecov/patch` or `codecov/project` check on a PR.
---

# Test coverage

## Intent

Line coverage proves a test *executes* a line; mutation testing (see `../ci/mutation-testing.md`) proves a test would *notice*
the line changing. Both matter, and they are different jobs. `../ci/mutation-testing.md` explicitly defers uncovered lines
here: "`NOT COVERED` mutants are already the patch-coverage check's job."

The practical gate is **patch coverage**: the coverage of the lines a change adds or alters. A PR that adds code its own
tests never execute is the failure mode this catches.

## Contract

- **Every added or changed line must be executed by a test.** Error, transport, non-OK-status, and decode branches
  count. They are almost always coverable because this codebase puts IO and endpoints behind seams: a `package var` for
  a URL (`latestURL`, `downloadBase`, `releasesURL`), a `var` for a function (`LatestVersion`, `Update`,
  `InstalledViaHomebrew`), or an injected client. Reach for the seam rather than declaring a branch untestable.
- **A branch unreachable by construction must be removed, not left uncovered.** Two shapes recur, and both are defects
  in the line itself rather than gaps in the tests:
  - an error return that cannot fail given adjacent code (e.g. `Flags().GetString(name)` for a flag registered as a
    string two lines below — bind the flag with `pflag`'s `StringVar` and read the variable instead);
  - a re-parse or re-check whose success the preceding call already guaranteed (e.g. parsing a tag again after a
    predicate proved it parses — return the parsed value from the predicate, so there is only one parse).
- **Match the coverage of the sibling you mirror.** The existing tests show the expected depth: `TestLatestReleaseNon200`
  and `TestLatestReleaseDecodeError` cover `latestRelease`'s status and decode failures, and
  `TestDownloadAssetNetworkError` covers a transport failure. New code of the same shape carries the same tests.
- **Tests that mutate a package-level seam restore it with `t.Cleanup` and do not call `t.Parallel`,** matching the
  existing `serveReleases`/`serveLatest` helpers.
- **Test doubles (`testmock.go` and the role-split `testmock_*.go`) are not product code and are excluded from
  coverage** (`.codecov.yml` `ignore` matches `**/testmock*.go`, so a new double file is covered by policy without a
  config change).
  Their own tests pin only what would otherwise fail open: fail-fast clients panic, the `With*Mock`/`Install*` helpers
  install and restore the seam, and mock paths and `$HOME` stay inside the test's temp tree. Defaults, `Fn`
  delegation, accessors and call recording are not tested on the double: the consuming tests break when they are
  wrong. A lone `testmock.go` line is therefore no patch-coverage finding.
- **`testutil/testgit.go` is test support and is excluded from the gate** for the same reason: `RunGit` executes the
  real git binary, so its behaviour tests are `integration`-tagged and absent from the default coverage profile. Its
  pure `gitIsolationEnvironment` is unit-tested in `testgit_test.go`.

## Where the bar lives (and why it surprises)

- **The numeric thresholds live in the repo's `.codecov.yml`**, which overrides Codecov's app-level settings: `project`
  is `target: auto` with `threshold: 0.1%` (no drop beyond noise) and `patch` is `target: 100%` (every added line
  executed). The comment updates in place and only when coverage changes, and uncovered patch lines get inline
  annotations. A change to the file changes the gate, so review it like production code and validate it at
  `https://api.codecov.io/validate`. `.github/workflows/ci.yml` uploads the profile via `codecov/codecov-action`; its
  second upload (`report_type: test_results`, `junit.xml`) feeds Codecov Test Analytics, whose PR comment the
  `comment:` block does not govern.
- **codecov is not a required status check on `main`.** The required checks come from the repository ruleset
  `protect main`, which requires `lint`, `test`, `race`, and `deadcode` (see `specs/ci/ci-pipeline.md`); classic branch
  protection has no required contexts, so consult the ruleset, not `branches/main/protection`. A failing
  `codecov/patch` therefore leaves a PR `MERGEABLE` with
  `mergeStateStatus: UNSTABLE` — mergeable with a failing non-required check; while a required check is still running the
  status is `BLOCKED` instead. Do not read `UNSTABLE` as "nothing is wrong".
- **Observed behaviour (PR #271, 2026-10-22):** `codecov/patch` failed at 93.88% (6 of 98 patch lines missing) and
  `codecov/project` failed at 98.77% against a 98.81% base — i.e. the project status does not tolerate a drop, and the
  patch status does not tolerate several uncovered lines.
- **Therefore: treat a red `codecov/patch`/`codecov/project` as a real finding and classify uncovered new production
  lines as Important in review, never Minor.** The `upgrade.channel` feature's whole-branch review accepted five
  uncovered `internal/upgrade` branches as non-blocking; CI rejected them and a follow-up commit had to add the tests
  and delete two unreachable branches. A reviewer's "mirrors the sibling's coverage profile" is a claim to check against
  the sibling, not to accept.
- **A `codecov/project` drop need not come from the diff.** On PR #278 (docs-only) `codecov/project` failed by one line
  because `internal/notify/watch.go`'s "cancel the context while `Watch` waits out a reconnect backoff" branch was
  covered in the base run but missed in the head run — a test that only *sometimes* executes a branch (the other `Watch`
  tests use a zero backoff, so that select races an immediate reconnect). When the diff adds no production line, read the
  Codecov delta per file: a single missed line in untouched code is coverage nondeterminism, not a gap the change
  introduced, and the fix is a deterministic test pinning the branch rather than a production change.
- **A stale branch yields a spurious `codecov/patch` miss.** Codecov numbers its per-line report against the PR head
  *merged with the base branch*, while the patch diff comes from the branch's own history. When the branch is behind
  `main`, the two disagree by the lines `main` added, so an added line lands on a pre-existing uncovered line far away
  and is reported as a patch miss. On PR #554 (2026-11-05) this produced a single msb.go miss whose reported line was
  exactly `main`'s `+18` line shift of an already-uncovered line; every genuinely added line was executed locally. The
  fix is to bring the branch up to date with `main` (merge or rebase) and push, which re-aligns the trees — do not add a
  test for a line `go tool cover` already shows as executed.

## Verification

`make coverage` runs the suite and prints the total; `make coverage-junit` also writes `junit.xml`. Neither is a gate and
neither computes patch coverage — the per-PR patch number comes only from Codecov. So there is nothing to run locally
that reproduces the check exactly; the honest local substitute is reading `go tool cover -func` output for the files a
change touches and confirming every added line appears.
