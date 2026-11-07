# Design: a hermetic default test suite

Date: 2026-11-07
Status: draft for approval

## Intent

The default `go test ./...` / `make test` suite must produce the same result on
any machine and in CI, independent of the host it runs on. Today it depends on
the execution environment in ways the ambient-environment gate only partially
covers: the git checkout it happens to sit in, the host's RAM and CPU count, the
inherited process environment, real network access, and wall-clock timing.

The immediate motivation is the ambient gate landed on 2026-11-06. It made the
common *direct* leak visible and sanctioned, but it is a source heuristic: it
cannot see an indirect read (a production function reaching `/proc/meminfo`), it
does not cover wall-clock, and by design it documents some host dependencies
rather than removing them. This design turns "decoupled from the execution
environment" into an executable bar and drives the suite to it.

## Goals

- Define hermeticity operationally: the suite passes identically when run from a
  copy of the module outside any git checkout, with a scrubbed environment, with
  no external network, and with host figures and wall-clock not changing a result.
- Make that bar executable as a repeatable check (`make test-hermetic` and a CI
  job), so a regression is caught, not merely described.
- **Run it on every release, and make it cheap enough to run more often**, with a
  stated promotion path to the default suite. The minimum is the release gate;
  the aspiration is the default `make test` once it is fast enough, so the
  guarantee is strong and always exercised.
- Fix the host dependencies at their surface: identity/CWD, host resource
  figures, environment, network, wall-clock.
- Keep the durable rule in one place: fold the resulting contract into
  `.operator-shared/specs/test-isolation.md` rather than starting a second
  source of truth, and update the user-facing docs and the release runbook.

## Non-goals

- Making the `integration`-, `msb`-, or `test/harness`-tagged suites hermetic.
  They exist to exercise the real host/KVM/network/docker stack and are excluded
  from the default suite by design.
- Full dependency injection of every ambient surface. We inject only where a
  value must be *supplied* rather than *denied* (host RAM, CPU count; a clock
  only if a test proves it needs an injected instant).
- macOS parity for the network namespace. The hard envelope is Linux; macOS gets
  the scrubbed-env/temp-copy part and relies on seams plus the static gate.
- A mocking library or stdlib interception (`bou.ke/monkey`, `-overlay` on
  `GOROOT`). Both were considered and rejected (see Approaches).

## Anti-goals

- A "hermetic" target that re-runs the suite in-tree and inherits the
  developer's environment; that proves nothing.
- Adding `//testenv:allow` markers to `time.Sleep` so the suite passes rather
  than justifying each wait, or new production seams where denying is enough.
- Weakening real-behavior tests (the loopback forwarder, the port probe) to make
  the envelope pass; their sockets stay real and loopback-only.

## Acceptance bar

`make test-hermetic` succeeds, and a normal in-tree `make test` is also green
(the requirement is "green in both"; the stricter per-test identical set is only
meaningful on Linux, where both runs are the strict envelope). Both run with
`-count=1` and no test-result cache, so a cached PASS cannot stand in for a real
one. Under all of:

1. The module copied to a fresh temp directory outside any git checkout.
2. `env -i` with only a minimal allowlist: `PATH`, `HOME`, `GOCACHE`,
   `GOMODCACHE`, `GOFLAGS`, `CGO_ENABLED`, `TMPDIR`, and the toolchain's needed
   Go vars.
3. A network namespace with only loopback up (Linux); the module cache
   pre-warmed so no download is attempted inside it.
4. No assertion whose expected value is read from the host (RAM, CPU count,
   git layout) or from elapsed wall-clock time.

The envelope is the falsification tool for 1-3. It does **not** falsify 4:
the host RAM and CPU count inside the envelope are the same as outside it, so a
host-figure assertion still passes. Requirement 4 needs either a separate
executable check or an explicitly audited status — this is the open decision
below (see "Open decision: host-figure enforcement").

## Approaches

### A. Hermetic envelope drives root-cause fixes (chosen)

Build the executable envelope first, use its failures as the worklist, then fix
each surface (a seam where a value must be injected, removal where a wait is not
the behavior under test, the gate where a direct call is the whole problem).
Enforcement is Linux-only for the network half; the temp-copy and scrubbed-env
halves run everywhere.

Trades off: more moving parts (a make target, a CI job, a netns wrapper) and a
period where the envelope is red. In return the bar is real and falsifiable, and
effort goes only to what actually breaks.

### B. Injectable host-environment object, threaded through production (selective)

One `hostenv` value (clock, sysinfo, env lookup, cwd/identity, dialer/listener,
exec) threaded the way clients already are through `options.Seams`; tests build a
deterministic fake. This is the strongest and most uniform answer, but it is a
large production API change, and it cannot cover real sockets, child re-execs,
or `t.TempDir`, so it does not remove the need for A. Used here only where a value
must be injected: `vm`'s `BootMaxMemoryMiB`/CPU defaults and `prepare`'s available
memory, via narrow package seams (matching what `session` already did).
Rejected as the whole approach as speculative generality.

### C. Sandbox-only hardening (mechanism inside A)

Change no production code; confine the test process (netns, `Landlock` for
`/dev`, scrubbed env, temp CWD). Strong for network and filesystem denial, but a
sandbox can only deny: it cannot inject a RAM size or a clock, so
`BootMaxMemoryMiB`/CPU assertions and any elapsed-time assertion stay host-
dependent. This is how A's envelope is built, not a competing whole.

### Rejected outright

- Mocking libraries (gomock/mockery/testify): they mock interfaces, not package
  functions; blocking surfaces (`net.Listen`, `os.Getenv`) have no interface to
  implement. They reduce fake boilerplate after a seam exists, which is not the
  problem here.
- Runtime patching (`bou.ke/monkey`): amd64/arm64-only, broken by inlining and
  by darwin/arm64, and process-global (the opposite of per-test isolation).
- `-overlay` on `GOROOT` to instrument `net`/`os`: catches indirect calls but
  requires a patched stdlib per Go release, a non-cacheable stdlib build, and a
  wrapper around every `go test`/IDE invocation.

## Decisions

- **A is the process; B is applied selectively** where a value must be supplied
  rather than denied. C is the envelope's mechanism.
- **Host-figure independence is enforced by a sentinel pass**, not by the
  envelope alone: host figures live on `options.Seams`, and a second envelope run
  pins them to non-host sentinels so a real-host read fails. See "Host-figure
  enforcement".
- **The envelope is Linux-strict, cross-platform-lenient.** `make test-hermetic`
  is Linux (netns + scrub + temp copy). `make test-scrubbed` (temp copy + scrub,
  no netns) runs on macOS too and is the floor everywhere.
- **The static gate stays the fast local guard**; the envelope is the CI
  backstop. The gate is not replaced by the envelope (it runs in milliseconds
  and catches a leak at the call site before tests even execute).
- **The static gate grows a wall-clock category.** `time.Sleep` becomes a gate
  category requiring a reason-bearing marker, the same shape as the existing
  ambient categories, so each wait is either removed or explicitly justified as
  a dwell-under-test. `time.Now` is *not* gated: a source scan cannot tell a
  relative fixture (`now.Add(-time.Hour)`) from an absolute-instant expectation,
  and 191 sites are mostly the former. The `time.Now` audit is manual; add a
  clock seam only if a test turns out to assert an absolute instant.
- **Identity is made hermetic by seam, not by a gate category.** Once the
  convenience accessors read a pinned seam, a correct call and an ambient one are
  syntactically identical, so a source gate could not distinguish them. The seam
  (or the `testutil.CheckoutSlug` + `t.Chdir` alternative) is the enforcement; no
  identity gate category is added. The envelope then falsifies any residual
  layout dependence, because CWD is outside any checkout.

## Host-figure enforcement: the sentinel pass

The envelope cannot falsify a RAM/CPU dependence (the values are unchanged), and
the source gate cannot see an indirect host read through production code — the
same class as the `session` bug. Requirement 4 is enforced by a second envelope
run, the **sentinel pass**, that pins the host-figure seams to values
deliberately different from the host so any test still reading the real host
fails:

- The host figures live on `options.Seams` (the established cross-package
  injection point), falling back to package vars and then `sysinfo`. `vm`,
  `prepare`, and `session` all read them through that one path, so a
  `prepare` test that calls the exported `vm.BootMaxMemoryMiB` sees the same
  value the rest of the run does.
- A test-only installer — driven by an env var the sentinel run sets — pins the
  seams to fixed sentinels (RAM 1 GiB, CPUs 1) in every test binary that installs
  the fail-fast mocks. Every test package already calls
  `mocks.InitFailFastMocks`, so the override is comprehensive without threading
  per-`RunOptions` state; production code stays free of test env reads.
- A small sentinel-exempt set (the seam-contract tests that assert the production
  wiring, marked `//testenv:allow`) is the only place the real value is read, and
  it is named explicitly so the exemption is reviewable.

Alternatives considered and rejected: a reachability analyzer (catches the
indirect case but noisy, slow, whole-program SSA) and an audited-only requirement
(cheapest, but leaves the residual as a permanent review responsibility).

## The surfaces and what each needs

### 1. Git identity / ambient CWD

`git.MustRepoSlug`/`MustCheckoutSlug` are cwd-derived test conveniences
(`internal/git/identity.go`); they call `ResolveIdentity(".")`. Callers:
`cmd/agents-sandbox` (5 files), `internal/sandbox/prepare`
(`prepare_sandbox_flow_test.go`, `reconfig_test.go`, `preview_test.go`),
`internal/sandbox/session`, and `internal/git`'s own tests. `configpaths`
derives the project base from CWD too.

Because both the tests and production resolve `"."`, the suite is currently
self-consistent *inside* the repo; the envelope removes that scaffolding and
exposes any place that assumed the module's real checkout. The fix is one
deterministic identity per test binary:

- **Preferred:** a `git` package seam for the cwd the convenience accessors use
  (`ResolveIdentity` keeps taking an explicit `cwd`; only the `Must*` wrappers
  read the seam), pinned in each affected `TestMain` to a synthesized checkout.
  The synthesized layout is created in-process with go-git
  (`testutil.InitRepo` already initializes via go-git, not the git binary), so
  it needs no external command and no network.
- **Alternative:** `testutil.CheckoutSlug(t)` returning a deterministic slug and
  a `t.Chdir(testutil.InitRepo(t))` at each site. More churn (dozens of sites),
  no global mutable state.

The gate should learn to flag `git.MustRepoSlug`/`MustCheckoutSlug` in the
default suite the same way it flags host probes, so a new call site cannot
reintroduce the dependency once the residuals are closed.

### 2. Host resource figures

Production reads: `vm.BootMaxMemoryMiB` (`sysinfo.TotalMemoryGiB`),
`vm` CPU defaults (`sysinfo.NumCPUs`), `prepare/reconfig.go`
(`sysinfo.AvailableMemoryMiB`); `session` already seams its two reads.

- Add narrow package seams in `vm` (`totalMemoryGiB`, `numCPUs`) and `prepare`
  (available memory), defaulting to `sysinfo`, pinned in the owning package's
  `TestMain` — the pattern `session`'s `bootMaxMemoryMiB`/`hostAvailableMemoryMiB`
  and the existing interval seams already use.
- **Cross-package reads are the complication.** A package-private var in `vm` is
  only pinnable by `vm`'s own test binary, so `prepare`'s test of
  `bootMaxMemoryGrew` (which calls the exported `vm.BootMaxMemoryMiB`) still reads
  the real host. This is why the seam lives on `options.Seams` (see "Host-figure
  enforcement"), with the package vars as its fallback.
- Rewrite the host-dependent assertions to compare against the pinned seam value
  (as `TestOfferMemoryRaiseAfterAttachClampsToTheBootMaximum` now does) so the
  test stays meaningful and machine-independent.
- Keep one seam-contract test per read that asserts the default closure is the
  production function and maps through unchanged; delete direct `sysinfo` reads
  from test files.

### 3. Process environment

Inherited env reaches `viperconfig` and the launch path. The envelope scrubs it;
the gate already covers direct `os.Getenv`/`Setenv` in tests. No production
change. Verify under the envelope that a scrubbed `$HOME`/`$XDG_*`/`$MSB_*`
cannot alter a result; `configpaths.WithMockConfigPaths` and the runtime home
seams should already hold, and the envelope confirms it.

### 4. Network

Only sanctioned loopback sockets (opener forwarder, ports probe) and `httptest`
remain. The netns envelope makes any external egress or name resolution fail
loudly. No production change. Optionally add a local non-envelope assertion that
no test resolves a non-loopback host (the gate can flag `net.Lookup*`; already
done) — the netns is the enforcement.

### 5. Wall-clock

18 `time.Sleep` sites across 10 test files (watch/spinner/reaper/forward/
pressure) plus interval seams; 191 `time.Now`, mostly fixture timestamps or
relative offsets.

- Replace each sleep that merely waits for an event with channel
  synchronization on the production seam (forwarder completion, watch tick,
  reaper quiescence) so the test proceeds on the signal, not the clock.
- Keep a sleep only where the dwell *is* the behavior under test (a reconnect
  backoff window, a poll cadence), and mark it with a reason in the new gate
  category.
- Audit `time.Now`-derived expectations; most are relative (`now.Add(-time.Hour)`)
  and stay. Add a clock seam only if a test asserts an absolute instant, and only
  then — not preemptively.

### 6. Filesystem and external binaries

`t.TempDir` is fine (TMPDIR is scrubbed to a temp inside the envelope);
`os.UserHomeDir` is redirected by `WithMockConfigPaths`; `/dev/kvm` stays a
marked probe; real-git artifact tests stay `integration`-tagged. `testutil.InitRepo`
uses go-git in-process, so identity fixtures need no git binary. The envelope's
copy must not write into the source tree (build/test writes go to the scrubbed
`TMPDIR`/caches), which the temp-copy step enforces.

## Enforcement, cadence, and the durable rule

- `make test-hermetic` (strict: temp copy + scrub + netns + sentinel pass) and
  `make test-scrubbed` (temp copy + scrub + sentinel pass, no netns; runs on
  macOS too, where netns has no equivalent).
- **Cadence: the release gate is the floor.** The release workflow runs the
  strict suite on every release and refuses to publish if it is red — the same
  standing as the existing release verification. It runs on pull requests once
  its measured runtime is acceptable, and becomes the default `make test` when it
  is fast enough to be the normal local loop. Promotion is driven by a measured
  budget, not a date.
- **Runtime budget.** Phase 1 measures the strict and scrubbed runs against
  `make test`. Design target: the scrubbed run within a small multiple of
  `make test` (it adds a temp copy and a second, sentinel pass; the build is
  already the dominant cost per `guides/test-runtime-cost.md`), so the default
  promotion is realistic. If the netns/sentinel overhead is too large for the
  local loop, the default becomes the scrubbed run and the strict run stays the
  release/PR gate — the guarantee still holds where it matters.
- Wiring: the release runbook (`.agents/skills/release/`) gains the hermetic
  run as a verify step; `CONTRIBUTING.md` and the docs describe the two targets
  and the cadence; `AGENTS.md`'s command table gains them.
- Extend the ambient gate with the `time.Sleep` category; the optional type-aware
  analyzer upgrade (a `go/analysis` analyzer replacing the AST/text scanner) is a
  separate improvement, not required for this bar.
- After the work lands, fold the contract into
  `.operator-shared/specs/test-isolation.md` (new sections for the envelope, the
  host-figure seams, the wall-clock rule, and the cadence) and retire this design
  document.

## Sequencing

1. **Envelope + inventory + budget.** `make test-hermetic`/`test-scrubbed`, temp
   copy, env scrub, netns wrapper; record the first red run as the worklist and
   measure the runtime against `make test`.
2. **Identity/CWD seam** (surface 1).
3. **Host-figure seams + sentinel pass** (surface 2): move the reads onto
   `options.Seams`, wire the test-only pinning installer, and make any real-host
   read fail under the sentinel run.
4. **Wall-clock** (surface 5): remove sleeps, add the `time.Sleep` gate category.
5. **Cadence wiring**: strict run in the release workflow (required), the optional
   PR job, `test-scrubbed` on macOS; docs and `AGENTS.md` updated.
6. **Contract fold** into `test-isolation.md`; delete this design.

## Left to the builder

- The exact env allowlist and how `env -i` interacts with the Go toolchain
  (`GOFLAGS`, `-tags microsandbox_ffi_path`, `CGO_ENABLED`, cache dirs).
- The netns privilege story on the CI runner (rootless `unshare -rn` versus
  `sudo unshare -n`) and whether AppArmor/userns restrictions force a container.
- The precise `git` seam shape (package seam pinned in `TestMain` versus the
  `testutil.CheckoutSlug` helper), chosen against the failing inventory.
- The sentinel env-var name, the sentinel values, and the exact seam-exempt set.
- Which sleeps are genuine dwell tests and which collapse into channels.
- CI job naming/trigger, and whether it is advisory or blocking at introduction.
- The measured runtime threshold that promotes the suite to per-PR and then to the
  default `make test`, and where the release workflow wires the strict run.
