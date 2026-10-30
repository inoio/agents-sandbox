---
description: Contract for keeping tests off real host state - the fail-fast seam mocks installed by every test package's TestMain, the host-home redirect in WithMockConfigPaths, the seam import-chain exceptions, and the rule that seam overrides must be restored so the suite stays idempotent under -count>1.
read_if: Adding a test package or a TestMain, debugging a test that reads or writes real host state (config, home, docker, msb, doctor), or a test that passes at -count=1 but fails under -count>1.
---

# Test isolation

## Contract

Tests must never read or write real host state. The tool dispatches all side-effecting work through injectable seams;
under tests each seam defaults to a **fail-fast** implementation that panics on first use, so a test that reaches real
resolution fails loudly instead of silently touching the host.

Every test package installs the fail-fast defaults in `TestMain`, for every seam family its tests can import without a
test import cycle. The normal call is
`mocks.InitFailFastMocks(m)` (`internal/testutil/mocks`), which installs all six seam families:

- `configpaths.InstallFailFastConfigPaths()`
- `docker.InstallFailFastGet()`
- `doctor.InstallFailFast()`
- `msb.InstallFailFastGet()`
- `notify.InstallFailFast()`
- `opener.InstallFailFast()` (the host opener's `xdg-open`/`open` spawn)

A test that legitimately exercises a seam opts in with `WithMockConfigPaths(t)` (or the docker/msb/doctor/notify/opener
equivalent).

The set is enforced, not hand-maintained. Three tests in `internal/testutil/isolation_contract_test.go` pin the chain:

- `TestSeamFamiliesMatchDeclaredInstallers` checks the family registry against the code: every exported
  `InstallFailFast*` declaration in the module's non-test sources must be registered, so a new family cannot go
  unchecked.
- `TestInitFailFastMocksInstallsEveryFamily` reads the wrapper's own body, so a dropped installer cannot leave its ~30
  callers silently unprotected.
- `TestEveryTestPackageInstallsItsAcyclicFailFastMocks` derives each package's expected families from the module import
  graph: a family is skipped only when the family's package imports the test package, which is exactly when importing it
  would cycle. A new test package, a dropped installer, or a `main_test.go` without a `TestMain` fails it.

The per-package check credits only direct calls that run before `m.Run()`: installers must be called directly in
`TestMain`, not from a helper, a `defer`, or a closure, and never after the tests start. An unqualified
`InitFailFastMocks(m)` counts as the wrapper, so the wrapper's own package can install it without importing itself. The
walk covers the module root too, so a package beside `go.mod` is checked like any other.

Packages in the seam import chain cannot import `internal/testutil/mocks` without a test import cycle; they install the
reachable subset directly, mirroring that body:

- `configpaths` (installs configpaths + docker; doctor and msb are unreachable and would cycle)
- `docker` (installs configpaths + docker + msb + notify; doctor imports docker, so it would cycle)
- `msb` (installs configpaths + docker + msb; doctor imports msb, so it would cycle)
- `termio` (installs configpaths + docker + msb; doctor imports termio, so it would cycle)
- `doctor` (installs configpaths + docker + doctor + msb + notify; importing `internal/testutil/mocks` would cycle
  through doctor)
- `runtime` (installs configpaths + docker + msb + notify; doctor imports runtime, so `InitFailFastMocks` would cycle
  and doctor is unreachable from runtime)
- `notify` (installs configpaths + docker + doctor + msb + notify; the wrapper installs notify, so importing it would
  cycle)
- `docslink` (installs configpaths + docker + msb + notify; doctor imports docslink, so the wrapper, which installs
  doctor, would cycle)
- `atomicwrite`, `mounts`, `naming`, `network`, `state` (install configpaths + docker + doctor + msb; notify imports
  them, so they cannot import the wrapper, which installs notify)
- `agent` and `filewalk` install docker only: `configpaths` imports `agent`, which imports `filewalk`, and `notify`
  imports both, so docker is the only family importable. Neither package reaches a seam in its own code; if one ever
  gains such a call, the seam must be reached through an injected dependency rather than a global.
- `internal/testutil/mocks` calls the wrapper unqualified from its `TestMain`, since a package cannot import itself.
  `TestInstalledFailFastDefaultsPanic` asserts the wrapper's effect there — each family's fail-fast is the one in
  effect, panic message included — which also puts the wrapper's body under that package's own coverage profile
  (`make coverage` measures per package, so a package with no test files reports 0%).

Add a `main_test.go` with such a `TestMain` to every new package that has tests; the contract test fails without one.

## The SDK and the native library

Tests must not reach the real microsandbox SDK either. `realMsbClient` delegates every SDK call through a
package seam var — `getSandbox`, `createSandbox`, `listSandboxesWith`, `getVolume`, `createVolume`,
`listVolumes`, `removeVolume`, `imageGet`, `imageList`, `imageRemove`, `imageLoad`, `realImageInspect`,
`imagePrune`, `storageUsage`, plus `resolveRuntime`/`ensureRuntime` — so its unit tests inject fakes and never
load the embedded native library or read the developer's real runtime. A new adapter routes its SDK call
through a seam and gets a fake-based unit test; the real round trip, if wanted, goes in an `msb`-tagged test
(`make test-msb`), which keeps the embedded library.

This is also why the unit and integration suites build with `-tags microsandbox_ffi_path` (see
`guides/test-runtime-cost.md`): the tag drops the SDK's ~36 MB embedded library from every test binary, and
because the tests reach the SDK only through seams, no `MICROSANDBOX_FFI_PATH` is needed.

An `msb`-tagged test keeps the embedded library **and** provisions its own runtime home. `internal/sandbox/msb`'s
`TestMain` points `MSB_HOME` at a fresh temp home, and each real-client test resolves the SDK-pinned runtime there
before its first SDK call (`ensureIsolatedRuntime`). The runtime install is lazy — it runs on the first test, not in
`TestMain` — so `compile-all`'s `-run=^$` stays inert. Without this the SDK resolves its default `~/.microsandbox`,
where a runtime left by a pre-decoupling agents-sandbox install fails sandbox creation with the native library's
"no tested sandbox launch contract for runtime <v>" after an SDK bump; the launcher repairs only its own private home
(`runtime-home-decoupling.md`).

## Host-home redirect

`configpaths.WithMockConfigPaths(t)` roots a `mockConfigPaths` in `t.TempDir()` **and** redirects `$HOME` into
`<tmp>/home`, so a stray `os.UserHomeDir()` / `os.Getenv("HOME")` in a test resolves inside the temp tree. Tests using
it must not be `t.Parallel` (`t.Setenv` forbids it). `WithMockConfigPathsHomeError(t, err)` installs a mock whose
`UserHomeDir` fails, to exercise host-home resolution failures.

## Project-path CWD pin

`configpaths.WithRealConfigPaths(t)` — the opt-in for the real path resolver — also pins the CWD to a fresh
`t.TempDir()`, for the same reason `WithMockConfigPaths` pins `$HOME`: the real project base is derived from the CWD's
checkout layout. Without the pin, a test that inherited the suite's CWD resolves a different base inside a bare
checkout's worktree (that checkout's bare root, not the relative `.agents-sandbox`), so a project-path assertion depends
on where the suite runs. The helper fails fast if the pinned temp dir still resolves to a bare-root base (i.e. `TMPDIR`
sits inside a bare checkout). A test that needs a specific layout chdirs into its own fixture after the call, as the
bare-checkout tests do. This is why `configpaths`' project-path accessor tests assert the relative base without
pinning the CWD themselves, and it closes the incident below.

## Deliberate real-state reads

The `integration`-tagged image tests (`internal/sandbox/image/render_integration_test.go`) are the one sanctioned
exception to "tests never read real host state". They resolve the agent release to bake from the launcher's real root
state — `RootState.updates.agents[<agent>].latest_version`, the release the upgrade prompt offers — through
`configpaths.WithRealConfigPaths(t)`, so a machine that has used the agent bakes a recent real release with no network
dependency and no hard-coded version that goes stale. When the cache is empty (a fresh CI runner) the test resolves the
release once through `agent.LatestVersion` and records it in the real root state, preserving `last_checked` so it does
not suppress the developer's own upgrade check; the rest of the suite then reuses it. The read/record helpers are
`cachedAgentVersion` and `recordCachedAgentVersion` (`agent_version_cache_test.go`,
`render_integration_test.go`).

## Seam overrides and suite idempotence

A test that overrides a package-level seam var — the fail-fast seams above, the SDK `realMsbClient` vars, or behavior
seams such as `agentLatestVersion`, `probeHostPort`, `watchReconnectPolicy`, `spinnerInterval` — must restore it with
`t.Cleanup`/`defer`. A leaked override survives into later tests in the same binary, and under `-count>1` (or any run
that repeats a package) into the next iteration. The suite must be idempotent: `go test -count=2 ./...` must agree with
`-count=1`.

The incident: `TestResolveBuildVersionPromptNamesThisProjectsVersion` replaced `agentLatestVersion` without restoring
it, so the coverage tests asserting the real default closure passed at `-count=1` (alphabetical file order runs them
first) but failed 19/20 at `-count=20`.

## Audit status (2026-10-16)

A full sweep of `internal/**` and `cmd/**` found the seam model holding: no production path outside the documented
`configpaths` exceptions (`mounts` `~/` sources, `docker`'s `~/.docker`) reads or writes host config/home state, and no
seam family is reached without its injectable factory. The remaining ambient paths are read-only or transient:

- `options.FirstFreeHostPort` probes a real loopback port through the `probeHostPort` var seam (added so tests inject a
  deterministic probe); production still binds/frees a transient socket. Read-only.
- `notify`'s backend runs the notifier through the `execCommand` var seam, whose `TestMain` now installs an
  `InstallFailFast` panic by default (tests opt in by overriding it); the temp WAV still goes to the real `os.TempDir`.
- `os.Getuid`/`Getgid` (image build args), `os.Getwd` (workspace source), `time.Now` (names/polling/spinner), and
  `viperconfig`'s env reads are read-only or the input itself, not leaks.

`agent` and `filewalk` keep the docker fail-fast as their only backstop (every other family either cycles through
`configpaths` or is declared in `notify`, which imports both). Keep any future host-path need in them injected, never
global.

## Why (the incident this encodes)

`TestConfigAgentPrintsMergedAndHostFiles` built the host opencode dir from `os.Getenv("HOME")` and wrote
`{"model":"host"}` into the developer's real `~/.config/opencode/opencode.jsonc`. The next launcher start read that file
as the agent's merged config, found it different from the host snippet merge, and raised the daemon-restart
"agent config changed" prompt (see `reconfig-decision.md`). `go test` must not leave the host in a state a later run
reacts to. The tree-wide fail-fast defaults and the `$HOME` redirect close this class.

A later incident closed the sibling class: `TestProjectDirsAndFiles`/`TestProjectConfigDirAbs` read the real resolver
against the suite's ambient CWD, so once the bare-checkout resolver made the checkout layout an input, they failed when
run from a bare checkout's worktree (`/workspace/.agents-sandbox` instead of the relative base) — and CI, on a non-bare
clone, never saw it. The `WithRealConfigPaths` CWD pin above closes it.
