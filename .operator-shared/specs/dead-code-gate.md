---
description: Design contract for a hard CI gate that fails when production code is unreachable from any main package — the `make deadcode` target over a pinned x/tools deadcode, a reason-bearing `//deadcode:ignore` marker we own (deadcode has no suppression feature) with file and declaration scope, a `ci/check-deadcode.sh` report parser because deadcode always exits 0, stale-marker enforcement, the linux-only GOOS constraint forced by the microsandbox SDK, and the evidence that `integration`/`msb` tag configurations are inert today.
read_if: Implementing or reviewing the dead-code CI gate, the `make deadcode` target, the `//deadcode:ignore` marker convention, `ci/check-deadcode.sh`, or the DEADCODE_VERSION pin.
---

# Dead-code gate

## Intent

`staticcheck`'s `unused` (U1000) counts test files as usage, so a production function whose only callers are in
`_test.go` looks alive and is invisible to every linter the repo runs. `golang.org/x/tools/cmd/deadcode` closes this: it
builds an RTA call graph from **main packages only** and reports everything else as unreachable, by default without even
loading test files. See `guides/dead-code-detection-go.md` for the survey and evidence.

This change turns that report into a **hard gate**. The contract is not "no test-only code exists" — intentional test
seams are legitimate and numerous. It is: *every function unreachable from production is either deleted, or carries an
explicit reason-bearing marker*. A function that is merely dead fails the build.

The mechanism and rules below describe what the code does today.

## Evidence this design is built on

All measured on this VM on 2026-10-21 with `golang.org/x/tools/cmd/deadcode@v0.52.0`, linux/amd64, module `github.com/inoio/agents-sandbox`.

- **deadcode always exits 0**, findings or not (`exit_code_with_dead_code=0`). The gate cannot read the exit code; it
  must collect the report and fail itself.
- **It has no suppression mechanism.** Its whole flag set is `-test`, `-tags`, `-filter`, `-generated`, `-whylive`,
  `-f`, `-json`, `-cpuprofile`, `-memprofile`. Grepping `deadcode.go` for `ignore`/`exclude`/`directive`/`suppress`
  returns only implementation comments. The one source-level opt-out it honours is Go's generated-code marker
  (`// Code generated … DO NOT EDIT.`), which is whole-file and not abusable as a per-function lever. **Any marker scheme
  is ours to define and enforce.**
- **`-tags integration` and `-tags msb` change nothing today.** All four configurations (none, `integration`, `msb`,
  `integration msb`) report exactly **249** functions. The reason: every file behind those tags is a `_test.go` file
  (`internal/sandbox/docker/docker_real_test.go`, `internal/sandbox/image/render_integration_test.go`,
  `cmd/agents-sandbox/cli_doctor_test.go`, `test/harness/smoke_msb_test.go`) and deadcode skips `_test.go` files by
  default. The tags are included anyway as future-proofing (see Decisions).
- **The baseline is 249 findings in 26 files**, and 228 of them are concentrated in ten test-support files:
  `internal/sandbox/msb/testmock.go` (107), `internal/termio/mock.go` (31), `internal/configpaths/testmock.go` (25),
  `internal/sandbox/docker/testmock.go` (24), `test/harness/pty.go` (20), `test/harness/harness.go` (7),
  `internal/sandbox/doctor/testmock.go` (7), `internal/testutil/testutil.go` (3), `internal/testutil/testgit.go` (3),
  `internal/testutil/mocks/testutil.go` (1). The remaining 21 are spread over 16 files, mostly production files holding
  one or two test seams or small dead helpers.
- **Cgo is required.** The FFI types above come from cgo-generated code: `CGO_ENABLED=0` fails identically
  (`undefined: CustomNetworkPolicy` and friends), and only `CGO_ENABLED=1` on the **host's own** GOOS produces a report.
- **A cross-GOOS run is not currently possible.** `GOOS=darwin` on linux fails to type-check the module before deadcode
  gets anywhere: the microsandbox SDK's `internal/ffi/restore.go` has `undefined: CustomNetworkPolicy` / `MountSpec` /
  `PortBindingOptions` / `VsockRouteOptions`, because those FFI types do not exist for the cross configuration.
  `GOOS=darwin go build ./...` fails identically, so this is a pre-existing module-level condition, not a deadcode
  problem. Combined with the cgo requirement, this means the analysis can only run for the host's own GOOS unless a cross
  C toolchain is supplied.
- **No `GOARCH` variants exist** in the module (`git ls-files '*.go' | rg '_(amd64|arm64|386|arm)\.go$'` is empty), so the
  report does not vary by architecture and `GOARCH` needs no pinning.
- Reference run cost: ~10 s for the whole module, linux, no tags.

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
  `ci/check-docs.sh` + `ci/test-check-docs.sh`; the script gets a matching `ci/test-check-deadcode.sh`.
- **`//deadcode:ignore reason="…"` is our contract, in two scopes.**
  - *Declaration scope* — in the contiguous comment block immediately preceding the declaration deadcode reported, or as
    a trailing comment on the declaration line (the repo has one-line methods such as
    `internal/termio/termio.go` `OutToVerboseRedirect.Out`, which has no doc comment). Covers exactly that function or
    method.
  - *File scope* — in the comment block preceding the `package` clause. Covers every finding in the file. This exists for
    the test-support files above (227 findings over 10 files), where a marker per function would be pure noise.
  - The `reason` is **required and non-empty** in both scopes. An empty or missing reason is a failure, not a default.
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
  before pushing: `PKG` to scope, and the tags/GOOS matrix overridable.

## Decisions

- **Marker, not a register.** A checked-in `pkg.Function  reason` file would have been cheaper to implement (a `jq`
  filter, no source parsing) but puts the reason away from the code and rots as entries outlive their functions. The
  marker keeps the reason next to the thing it excuses and survives refactors. The cost, accepted: we own a small source
  parser in `ci/`. Recorded decision of the maintainer.
- **Move, do not mark, a test-only helper, decided.** The first branch to trip the gate on test-only convenience
  constructors (`naming.ProjectVMName` and its `Legacy…` siblings, added for parser/fuzz fixtures) marked them
  declaration-scope instead of relocating them; on review the maintainer ruled they belong in `_test.go` next to the tests
  that use them. A marker on a test-only helper keeps production surface with no production caller; moving it keeps the
  production package and the dead-code baseline honest. The test files define the fixtures (`naming/fixtures_test.go`)
  and the production shape is asserted against the real constructors in `vm`/`volume`.
- **`verify`, not `check`.** `check` is the fast local loop (`fmt lint test compile-all docs-linkcheck docs-linkcheck-test`);
  `verify` is the read-only CI-parity target and already carries `vulncheck`, the closest analogue. This works because the
  gate analyses the host's own GOOS, so it is runnable on every supported development host (see Decisions).
- **GOOS: the host's own, decided.** CI (`ubuntu-latest`) analyses linux; a macOS developer analyses darwin natively. No
  `GOOS` pin and no cross C toolchain. Accepted consequence: the two platforms' finding sets need not agree, and the
  darwin baseline is unverified because it cannot be computed from the linux dev VM — a Mac run may surface a few extra
  markers for whoever runs it to contribute. `GOARCH` stays at the host default (no architecture-specific files exist).
- **The CI job blocks PRs, not releases, decided.** `deadcode` is **not** added to `release`'s
  `needs: [lint, test, integration]`, exactly like `vulncheck`. A release tag can therefore publish past a failing
  dead-code gate; this matches the existing deliberate budget choice for `vulncheck`. The gate's enforcement point is the
  pull request.
- **Resolved by the builder:** the marker finder and the verdict live in a small Go program, `ci/deadcodecheck`, built
  once by the script and then run directly — `go run` collapses any non-zero program exit to 1, which would erase the
  diagnosis-vs-untrusted-analysis distinction. Go, not awk, because the hard part is positional: a marker is resolved by
  AST position, not line arithmetic. The script keeps the parts that are naturally shell: the configuration matrix, the
  `go build`/`go run` invocations, and the `git ls-files` source list.
- **Resolved by the builder:** two environment seams make the script testable without the tool or the network —
  `DEADCODE_FIXTURE_DIR` (a directory of per-configuration JSON payloads) and `DEADCODE_FILES` (the source list to scan).
  `ci/test-check-deadcode.sh` uses both, never invokes deadcode, and is wired into `check` and `verify` as
  `deadcode-test` (mirroring `docs-linkcheck-test`), so the everyday loop stays fast and offline.
- **Resolved by the builder:** a `PKG`-scoped run is an inspection aid, not the gate. It prints findings, skips
  stale-marker enforcement (which needs the whole module to know what is justified), and says so in its output. deadcode
  also needs at least one `main` package in the pattern — a leaf package prints "no main packages" and exits 1 — so the
  documented narrow form is `PKG=./cmd/...`.
- **Resolved by the builder:** the script exports `CGO_ENABLED=1`, so a host without a C compiler fails the analysis
  (exit 2) instead of loading a package set the module is not really built from.
- **Resolved by the builder:** spelling is `//deadcode:ignore reason="..."` — the `reason` value is double-quoted, like
  `// gomutants:disable <TYPE> reason="..."`. A marker that is present but inert (attached to no declaration or file, or
  carrying an empty reason) fails like a stale one; a marker that silently excuses nothing would be the "nothing to see
  here" failure mode the anti-goals name.
- **The baseline is resolved, not merely marked.** The gate must be green from the commit that introduces it, or it is
  never switched on. Triaging the 21 scattered findings showed 9 are intentional test seams (`agent.Register`,
  `git.RepoSlug`, `git.CheckoutSlug`, `viperconfig.NewResolverWithConfig`, `docker.RealClient`,
  `image.WithMockAgentVersion`, `image.WithMockAgentVersionResolver`, `vm.SetDaemonHealthFunc`,
  `vm.SetRuntimeHomeMigrationOffer`) and carry a reason-bearing marker, while **12 are genuinely dead and are deleted**:
  `homeconfig.MergeManifests` / `BuildHomeFiles` / `DescribeManifest` (superseded by the production
  `BuildHomeFilesWithModes` / `DescribeLayers` / `LoadLayers`), `upgrade.IsNewer` (a wrapper existing only for its own
  test), `termio.OutToVerboseRedirect` and its `Out`/`Outf` (duplicated by `cmd`'s live
  `autoPruneOutToVerboseRedirect`), `imageIdentity.values`, `reprovision.AgentConfigPath`, `state.RootTxn.Exists`,
  `volume.VolumeAction.String` (the live formatter is `actionLabel`), and `notify.Tracker.HandleEventType` (the live
  entry point is `Tracker.Handle`). Three of those needed their tests retargeted rather than deleted, because the
  deleted symbol was only a *wrapper* over live logic: `runtime.InspectRuntime` (the exported one-liner; `inspectRuntime`
  is live, as `prepareRuntime`/`repairRuntime` switch on its result), `imageIdentity.values` (the tests now build their
  label fixture through a test-side oracle), and `RootTxn.Exists` (which took its whole substrate with it — the `exists`
  field, `rootStatePresent`, the `statRootStateFile` seam — because `ReadRootState` already returns a zero state for a
  missing file and an error otherwise, leaving the stat redundant). The ten test-support files holding the other 228
  findings get one file-scope marker each, reason `test support: referenced only by _test.go files`. Each marked
  declaration's reason names its justification: `agent.Register` "internal registry extension point; only registry tests
  register stubs"; `git.RepoSlug`/`git.CheckoutSlug` "cwd-derived convenience for tests; production reads the
  ResolveIdentity fields"; `viperconfig.NewResolverWithConfig` "construction seam for cmd tests; see doc comment";
  `docker.RealClient` "integration tests opt back into the real docker daemon"; `image.WithMockAgentVersion` "test seam
  establishing a pinned agent version"; `image.WithMockAgentVersionResolver` "test seam establishing a custom agent
  version resolver"; `vm.SetDaemonHealthFunc` "test seam replacing the daemon health"; `vm.SetRuntimeHomeMigrationOffer`
  "test seam replacing the runtime migration offer". Marking a genuinely dead function to silence the gate is the rubber
  stamp the anti-goals forbid.
- **The gate has flagged two more seams on code that landed after it was designed** — the dry-run preview's
  `image.RunnerDockerfileID` (exported so higher-layer preview tests can seed a matching label) and
  `vm.SetAgentLatestVersion` (keeps the upgrade check off the network) — both marked in the same declaration-scope
  category, with no change to the mechanism.
- **Resolved by the builder:** markers are additive, so if the BuildKit plan (issue #15) removes `internal/sandbox/docker`
  first, the 24 `docker/testmock.go` markers disappear with the package and the totals in this document are stale while the
  mechanism is unaffected.
- **Tag configurations included despite being inert.** The user asked for `integration` and `msb`; the evidence says they
  add nothing today. They are kept because the failure mode they guard against is silent: the first non-`_test.go` file
  to land behind `//go:build integration` or `//go:build msb` would otherwise be analysed by nobody. The cost is ~10 s × 4.

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
- The private/CI script follows the `ci/check-docs.sh` precedent: a POSIX-sh script plus a `ci/test-check-*.sh` test, both
  wired into `verify` (the existing pair is wired into `check` via `docs-linkcheck-test`).
- `GOMUTANTS_VERSION`'s lesson applies: the pin is not visible to dependabot, so it must be documented where the other
  hand-bumped pins are.

## Shape

```make
DEADCODE_VERSION = v0.52.0

# Hard gate: production code unreachable from any main package must be deleted or
# carry a //deadcode:ignore marker with a reason. deadcode always exits 0, so
# ci/check-deadcode.sh (through the ci/deadcodecheck helper) makes the decision.
#   make deadcode                       # the four-configuration matrix
#   make deadcode PKG=./cmd/...         # inspection only; stale markers not checked
#   make deadcode TAGS=integration      # one configuration instead of the matrix
deadcode:
	DEADCODE_VERSION='$(DEADCODE_VERSION)' PKG='$(PKG)' $(if $(TAGS),TAGS='$(TAGS)',) ci/check-deadcode.sh

# Test the gate script itself (fixtures only: never runs deadcode).
deadcode-test:
	ci/test-check-deadcode.sh

verify: validate-fmt lint test test-race compile-all vulncheck deadcode docs-linkcheck docs-linkcheck-test deadcode-test
```

`make check` gains only `deadcode-test` (the script's own test); the analysis itself stays out of the fast loop.

CI gains a job mirroring `vulncheck`, deliberately absent from `release`'s `needs:`:

```yaml
  deadcode:
    name: deadcode
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: .go-version
          cache: true
      - run: make deadcode
```

