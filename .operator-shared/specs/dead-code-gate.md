---
description: Design contract for a hard CI gate that fails when production code is unreachable from any main package — the `make deadcode` target over a pinned x/tools deadcode, a reason-bearing `//deadcode:ignore` marker we own (deadcode has no suppression feature) with file and declaration scope, a `ci/check-deadcode.sh` report parser because deadcode always exits 0, stale-marker enforcement, and the host-GOOS/cgo constraint forced by the microsandbox SDK.
read_if: Implementing or reviewing the dead-code CI gate, the `make deadcode` target, the `//deadcode:ignore` marker convention, `ci/check-deadcode.sh`, or the DEADCODE_VERSION pin.
---

# Dead-code gate

## Intent

`staticcheck`'s `unused` (U1000) counts test files as usage, so a production function whose only callers are in
`_test.go` looks alive and is invisible to every linter the repo runs. `golang.org/x/tools/cmd/deadcode` closes this: it
builds an RTA call graph from **main packages only** and reports everything else as unreachable, by default without even
loading test files. See `guides/dead-code-detection-go.md` for the survey and evidence.

The contract is not "no test-only code exists" — intentional test seams are legitimate and numerous. It is: *every
function unreachable from production is either deleted, or carries an explicit reason-bearing marker*. A function that
is merely dead fails the build. deadcode has no suppression mechanism, so the marker scheme is ours to define and
enforce.

## Contract

- **Hard gate, in `verify` and in CI.** Wired into `make verify` (not `make check`) and into a dedicated CI job that
  mirrors the `vulncheck` job's shape. Unlike `make mutation`, this **is** a gate: it fails the build.
- **Pinned, no global install.** `DEADCODE_VERSION` is a `Makefile` constant, invoked as
  `go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)`, exactly like `GOVULNCHECK_VERSION`/`vulncheck`. As with
  `GOMUTANTS_VERSION`, dependabot (`gomod`, `github-actions`) does not see it, so it is bumped by hand.
- **The exit code is not the signal.** `ci/check-deadcode.sh` runs deadcode with `-json`, applies the marker and
  configuration filters, prints the remaining findings in the compiler-diagnostic form deadcode uses by default, and
  exits non-zero when any survive. Its statuses are 0 (clean), 1 (a diagnosis — a finding no marker excuses, a marker
  that excuses nothing, or an unusable marker), and 2 (the analysis could not be trusted); an empty finding set is never
  inferred from a failed analysis. The Makefile target is a thin wrapper over that script, in the style of
  `ci/check-docs.sh` + `ci/test-check-docs.sh`.
- **Offline-testable seams.** `DEADCODE_FILES` (the source list to scan) and `DEADCODE_FIXTURE_DIR` (per-configuration
  payloads) let `ci/test-check-deadcode.sh` exercise the parse-and-verdict path without invoking deadcode or the network.
  `deadcode-test` runs it in `check` and `verify`, alongside the other script tests (`docs-linkcheck-test`,
  `changelog-check-test`, `cleanup-branches-test`); the analysis itself stays out of the fast loop. The script exports
  `CGO_ENABLED=1`, so a host without a C compiler fails the analysis (exit 2) rather than loading a package set the
  module is not built from.
- **`//deadcode:ignore reason="…"` is our contract, in two scopes.**
  - *Declaration scope* — in the contiguous comment block immediately preceding the declaration deadcode reported, or as
    a trailing comment on the declaration line (the repo has one-line methods such as `termio.OutToVerboseRedirect.Out`,
    which has no doc comment). Covers exactly that function or method.
  - *File scope* — in the comment block preceding the `package` clause. Covers every finding in the file. This exists for
    the test-support files, where a marker per function would be pure noise.
  - The `reason` is **required and non-empty** in both scopes, double-quoted like
    `// gomutants:disable <TYPE> reason="..."`. An empty or missing reason is a failure, not a default.
- **Stale markers fail too.** A marker whose declaration or file is *not* reported as dead is an error. This is the
  mechanism's self-defence: markers cannot accumulate as decoration after the code they justified is deleted or becomes
  live. Enforced by `ci/check-deadcode.sh`, in the spirit of the deprecation-register regression tests.
- **Test-only helpers move to test code; they are not marked.** A production declaration reached only from `_test.go`
  files that is a convenience helper — a fixture constructor, a string builder — is **moved into a `_test.go` file** (or
  deleted), not excused with a marker. Markers are for declarations that must stay in production code: a substitution
  seam (`image/fetch.go`, `vm/daemon.go`) or a build-tag/test-support file (`*/testmock.go`, `test/harness/*`). Deciding
  which one a finding is belongs to the reviewer at gate time, not to the marker.
- **Configuration matrix.** deadcode analyses one configuration per run. The gate runs one run per configuration in a
  matrix over the build tags (`''`, `integration`, `msb`, `integration msb`) and fails if **any** configuration reports an
  unmarked finding. `GOOS` is the host's own (see Decisions); `GOARCH` is left at the host default because the module has
  no architecture-specific files.
- **`make deadcode` is available on demand** with the same knobs as the other audit targets, so a developer can inspect
  before pushing: `PKG` to scope, and the tags/GOOS matrix overridable. A `PKG`-scoped run is an inspection aid, not the
  gate: it prints findings, skips stale-marker enforcement (which needs the whole module to know what is justified), and
  says so. deadcode also needs at least one `main` package in the pattern — a leaf package prints "no main packages" — so
  the documented narrow form is `PKG=./cmd/...`.

## Decisions

- **Marker, not a register.** A checked-in `pkg.Function  reason` file would have been cheaper to implement (a `jq`
  filter, no source parsing) but puts the reason away from the code and rots as entries outlive their functions. The
  marker keeps the reason next to the thing it excuses and survives refactors. The cost, accepted: we own a small source
  parser in `ci/`. Recorded decision of the maintainer.
- **Move, do not mark, a test-only helper, decided.** A marker on a test-only helper keeps production surface with no
  production caller; moving it into `_test.go` keeps the production package and the dead-code baseline honest. The test
  files define the fixtures (`naming/fixtures_test.go`) and the production shape is asserted against the real
  constructors in `vm`/`volume`.
- **`verify`, not `check`.** `check` is the fast local loop; `verify` is the read-only CI-parity target and carries
  `vulncheck` and the `deadcode` analysis. This works because the gate analyses the host's own GOOS, so it is runnable on
  every supported development host.
- **GOOS: the host's own, decided.** CI (`ubuntu-latest`) analyses linux; a macOS developer analyses darwin natively. No
  `GOOS` pin and no cross C toolchain. Accepted consequence: the two platforms' finding sets need not agree, and the
  darwin baseline is unverified because it cannot be computed from the linux dev VM — a Mac run may surface a few extra
  markers for whoever runs it to contribute. `GOARCH` stays at the host default (no architecture-specific files exist).
- **The CI job blocks PRs, not releases, decided.** `deadcode` is **not** added to `release`'s
  `needs: [lint, test, integration]`, exactly like `vulncheck`. A release tag can therefore publish past a failing
  dead-code gate; this matches the existing deliberate budget choice for `vulncheck`. The gate's enforcement point is the
  pull request.
- **Behavior seams carry no marker after #518.** The four run behavior seams (`vm.SetProjectVMLock`,
  `vm.SetDaemonHealthFunc`, `prepare.SetAgentLatestVersion`, `prepare.SetRuntimeHomeMigrationOffer`) moved to the
  injected `options.Seams` value on `options.RunOptions`, so they are no longer package state and need no
  `//deadcode:ignore` (see `client-injection.md`).
- **Tag configurations included despite being inert.** The `integration` and `msb` tag sets add no findings today,
  because every file behind those tags is a `_test.go` file and deadcode skips `_test.go` files by default. They are kept
  because the failure mode they guard against is silent: the first non-`_test.go` file to land behind
  `//go:build integration` or `//go:build msb` would otherwise be analysed by nobody. The cost is one analysis per
  configuration.

## Goals

- A PR fails when a function becomes unreachable from production and carries no reason-bearing marker.
- Test seams stay expressible without per-function ceremony, and with a machine-checked reason.
- Markers cannot outlive their justification.

## Non-goals

- Covering darwin/arm64 analysis.
- Finding dead *code* that is reachable but unexercised by tests (that is coverage's job), or dead *types*, *fields*,
  *constants*, and unused imports (staticcheck's job, and it does those correctly).
- Gating on the mutation-testing model (a score, a threshold).

## Anti-goals

- A gate that fails on day one because the baseline was not cleared, and is therefore disabled by the first person who
  hits it.
- Markers as a rubber stamp: a `reason` that says nothing ("test", "unused"), or file-scope markers applied to production
  files to silence an inconvenient finding.
- The gate silently weakening when `ci/check-deadcode.sh` fails to parse deadcode's output — an empty finding set must be
  distinguishable from a failed analysis. A non-zero deadcode exit, a non-JSON payload, a missing `-json` key, or a
  report-less directory is an error, never "no findings".
- Making `make verify` unusable on a supported development host.

## Constraints

- Follow `AGENTS.md`: idiomatic Go for any helper, `jq` for the JSON (never brittle text parsing), `make fmt`/`make lint`,
  `make check` before the final commit.
- `CHANGELOG.md` `[Unreleased]`, `CONTRIBUTING.md` (Testing), and `.agents/skills/release/NON-GO-DEPS.md` (the
  `DEADCODE_VERSION` row, alongside the existing `gomutants`/`govulncheck` pins) stay in sync.
- The CI script follows the `ci/check-docs.sh` precedent: a POSIX-sh script plus a `ci/test-check-*.sh` test, both wired
  into `verify` (the existing pair is wired into `check` via `docs-linkcheck-test`).
- `GOMUTANTS_VERSION`'s lesson applies: the pin is not visible to dependabot, so it must be documented where the other
  hand-bumped pins are.
