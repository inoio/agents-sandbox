---
description: Where `go test ./...` wall time actually goes (test-binary linking, not test bodies), how the unit and integration suites drop the SDK's ~36 MB embedded native library, and the per-package timeout seams that decide failure latency.
read_if: Trying to speed up `go test`/`make test`, investigating a slow or hanging test run, adding a polling/wait seam or a `TestMain`, adding a real-SDK test, or deciding whether a test may consume a production timeout budget.
---

# Test runtime cost

## The run is build-bound, not test-bound

`go test ./...` spends most of its wall time building and starting test binaries, not running test bodies.

Measured on 2026-10-24 (16-core VM, warm `GOCACHE`, `-count=1`):

- Full run: ~12 s wall, ~55 s user + ~31 s sys.
- Compile-and-start only (`go test -count=1 -run '^$' ./...`): ~10 s wall, ~49 s user + ~24 s sys.

The gap is ~1-2 s. Optimizing test bodies alone can therefore move wall time only marginally; the lever with
real headroom is the build/link step. (On a loaded host the absolute numbers are larger and the ratio shifts, but
the split holds: a 2026-11-07 re-measurement on a shared 16-core VM saw compile-and-start roughly equal the full
run, with test bodies adding only a few seconds.)

`make test-uncached` is `make test` plus `-count=1`, so it re-runs every test rather than returning Go's cached
result — the target to use when measuring or profiling test bodies. A second `make test` on an unchanged tree is
cache-fast.

## Why linking is paid on every run

`go test` in **run** mode links each test binary into the ephemeral work dir, so the link command's `-o` is a
random `$WORK/b0NN/<pkg>.test` path on every invocation. That path is part of the action's cache key, so the
cached link is never reused:

- Repeated `go test -c -o <same path> ./internal/agent` — second run links zero times (cache hit).
- Repeated `go test -c -o <different path> ...` (or any run-mode `go test`) — links every time, with an
  identical `-buildid`.

So every `go test ./...` re-links all ~39 test binaries; there is no way to make run mode reuse the link.

## The embedded native library, and the tag that drops it

The microsandbox Go SDK embeds a native shared library into `internal/bundle`
(`libmicrosandbox_go_ffi-linux-amd64.so`, ~36 MB; analogous dylibs/dlls per platform) via `//go:embed`. Any
test binary that imports the `msb` package — hence `ffi` and `bundle` — carries it, so every test binary was
~44-55 MB regardless of how small the package under test is.

The SDK ships a build tag that removes the embed: `-tags microsandbox_ffi_path` swaps `bundle.go` for
`bundle_path.go`, which reads the library bytes from `$MICROSANDBOX_FFI_PATH` at runtime instead. The library is
`dlopen`ed either way, so the tag only changes where the bytes come from.

Because the unit and integration suites reach the SDK only through seams (`specs/test-isolation.md`), they never
load the library, so they build with the tag and need no `MICROSANDBOX_FFI_PATH`:

| package | default | tagged |
|---|---|---|
| `internal/humanize` | 47 MB | 13 MB |
| `internal/sandbox/msb` | 44 MB | 9.4 MB |
| `internal/sandbox/vm` | 54 MB | 19 MB |
| `internal/sandbox/image` (integration) | 50 MB | 15 MB |

`make test`, `test-race`, `coverage`, and `coverage-junit` pass `NO_EMBEDDED_FFI_TAG`
(`microsandbox_ffi_path`), and `test-integration` passes `integration $(NO_EMBEDDED_FFI_TAG)`. The `msb`-tagged
tests (`make test-msb`) keep the embed: they are the tests that actually load the library.

Go cannot make a module's build tag the default from inside a repo, and putting the tag in `GOFLAGS` would also
hit `go build`/`go run`, producing a launcher with no embedded library. So a bare `go test ./...` still builds
the embedded (slower) configuration; use the make targets.

Measured warm effect on this machine: full `make test` ~10.6 s → ~10.0 s, build-only ~9.5 s → ~9.0 s
(user ~50 s → ~46 s, sys ~24 s → ~20 s). The wall win is modest because go-tool and compile overhead remain;
the binary size and the ~1.3 GB/run of writes are what drop.

## Test-body hotspots

Wall time is build-bound (above), and test bodies add only a few seconds; the
packages run in parallel, so an individual package's cost rarely reaches the
critical path. The numbers below are per-package test-binary time (min of 3,
`-count=1`, warm `GOCACHE`) on a 16-core VM, after the timing-seam work. The host
is shared with other agents, so absolute wall times swing with load; treat them
as relative.

| package | test body | dominant cost |
|---|---|---|
| `cmd/agents-sandbox` | ~1.5 s | 300+ small tests; `setupCommandFixtures` rebuilds the whole root command per test |
| `internal/testutil` | ~0.9 s | four module-wide contract scans (below) |
| `internal/opener` | ~0.5 s | `TestForwarderKeepsReusedListenerWhenEarlierCallbackReturns` waits 150 ms for a wrong close to manifest |
| `test/harness` | ~0.5 s | deliberate pty timeout waits (`waitForTUI`/`waitForMarker`/`waitSettled`) |
| `internal/sandbox/prepare` | ~0.4 s | many small tests, none over 0.1 s |
| `internal/sandbox/image` | ~0.4 s | `TestHostOpenerShimTimesOutWithoutLauncher` (real shim subprocess, 0.1 s timeout) |
| `internal/sandbox/state` | ~0.2 s | `TestCountActiveClients_AcquireVsCountRace` filesystem loop |
| `internal/notify` | <0.05 s | was ~1.3 s before the clock seam |

`internal/git` and `internal/sandbox/msb` are no longer hotspots: the real-git tests moved behind the `integration`
tag, and the msb real-client tests inject `ensureRuntime` or run under the `msb` tag.

The four `internal/testutil` contract tests (`TestUntaggedTestsExecuteNoAmbientTool`,
`TestUntaggedTestsReachNoUnsanctionedEnvironment`, `TestEveryTestPackageInstallsItsAcyclicFailFastMocks`,
`TestSeamFamiliesMatchDeclaredInstallers`) all need the same module-wide view, so they share it through
`module_scan_cache_test.go` — `modulePackages`, `defaultBuildTestSources`, and a per-path parsed-AST cache
(`cachedParseGoFile`). Before the cache each walked the tree and parsed every source four times (~2.1 s); now the
walk, the read, and the parse each happen once (~0.9 s). A new contract gate must go through those helpers, not a
fresh `filepath.WalkDir`.

There is no `t.Parallel()` in the repo. It is not absent by oversight: `configpaths.WithMockConfigPaths` uses
`t.Setenv`, and the `InstallFailFast*`/`Get` seams are process-global, so within-package parallelism is
precluded by the same design that makes the fail-fast defaults work.

## Timeout seams and failure latency

Tests that poll production budgets can hide a hang: with a missed mock the run waits the production timeout
before failing. The catchable package vars and their production values:

- `vm`: `daemonReadyTimeout` 60 s / `daemonPollInterval` 2 s; `dockerdReadyTimeout` 30 s /
  `dockerdPollInterval` 1 s; `sandboxLifecyclePollInterval` 100 ms.
- `session`: `waitQuiescentPollInterval` 2 s.
- `termio`: `spinnerInterval` 100 ms.
- `options`: `SandboxStopTimeout` 30 s; `DefaultVMIdleTimeout` 10 s (consts).
- `notify`: `backoffStart` 1 s / `backoffMax` 30 s / `longLived` 30 s (the reconnect timing reads `watchClock`'s
  `now`/`wait`, which the backoff test fakes instead of sleeping); HTTP clients in `agent`/`upgrade` use
  10 s (and a 5 min download).

`vm`'s and `session`'s `TestMain`s cap `daemonReadyTimeout`/`daemonPollInterval`,
`dockerdReadyTimeout`/`dockerdPollInterval`, `sandboxLifecyclePollInterval`, and `waitQuiescentPollInterval`,
so a mock that never answers fails in ~2 s or ~10 ms instead of the production budget. A test that needs the
production window (or a deliberately expired one) saves and restores the var itself. The dockerd production
default is a named const (`dockerdReadyTimeoutDefault`) so `TestDockerdReadyTimeoutAllowsFailOver` keeps
asserting the default, not the test-shortened var.

`make test`/`coverage`/`coverage-junit` pass `-timeout $(TEST_TIMEOUT)` (default 5m), so a wedged binary panics
with a stack dump instead of blocking for Go's default 10 minutes per package.
`test-integration`/`test-msb` already pass `-timeout 60m`.

## Levers, with cost

- **Build unit and integration with `-tags microsandbox_ffi_path`** (done). Drops the embedded library from
  every unit and integration test binary; needs the adapters seamed, which they now are.
- **Cap every polling seam in its package `TestMain`** (done for `vm` and `session`). Cheap, directly bounds
  failure latency. A test that needs the long path overrides the var locally.
- **Add `-timeout` to `make test`/`make coverage-junit`** (done; `TEST_TIMEOUT`, default 5m). Bounds a wedged
  binary; must exceed the slowest single package on the slowest runner.
- **Keep the network and the real runtime out of the unit tier** (done). The SDK installer test injects
  `ensureRuntime`; the real-client round trips are `msb`-tagged.
- **Memoize the module-wide test scans** (done, `internal/testutil/module_scan_cache_test.go`). The four contract
  gates re-walked and re-parsed the whole module; sharing `modulePackages`/`defaultBuildTestSources`/parsed ASTs cut
  the package from ~2.1 s to ~0.9 s. New gates must use those helpers.
- **Give a timing test a clock seam, not a real sleep** (done for `notify.Watch` via `watchClock`, and
  `opener.CallbackForwarder` via `forwardTimer`/`newForwardTimer`). A test that proved a dwell by sleeping now drives
  the seam: notify's backoff test fell from ~1.3 s to <0.05 s. Prefer channel synchronization where the test only
  needs the signal; reach for a seam when the dwell itself is the assertion. This also removes a
  `specs/test-isolation.md` "wall-clock waits" residual.
- **Keep a timeout-path test's timeout just above its poll interval** (done: pty waits 50–100 ms, the opener shim
  0.1 s). A timeout only needs to be long enough to elapse.
