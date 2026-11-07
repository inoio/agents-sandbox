---
description: Contract for keeping tests off real host state - the fail-fast seam mocks installed by every test package's TestMain, the host-home redirect in WithMockConfigPaths, the seam import-chain exceptions, the msb-tagged isolated runtime home and its stop-then-remove sandbox teardown, and the rule that seam overrides must be restored so the suite stays idempotent under -count>1.
read_if: Adding a test package or a TestMain, debugging a test that reads or writes real host state (config, home, docker, msb, doctor), an msb-tagged test that leaks a sandbox/VM, or a test that passes at -count=1 but fails under -count>1.
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

Packages in the seam import chain cannot import `internal/testutil/mocks` without a test import cycle, so they call
the reachable `InstallFailFast*` functions directly in `TestMain` instead of the wrapper. Which families are reachable
is not enumerated here: `TestEveryTestPackageInstallsItsAcyclicFailFastMocks` derives each package's expected set from
the module import graph, so a family is skipped exactly when the family's package imports the test package. Some
illustrative cases:

- `configpaths` installs configpaths + docker + opener (doctor and msb would cycle).
- `sandbox/msb` installs configpaths + docker + msb + opener, with doctor deliberately omitted.
- `agent` and `filewalk` reach every family except notify — `notify` imports both — and neither reaches a seam in its
  own code; if one ever gains such a call, the seam must be reached through an injected dependency rather than a
  global.

Nearly every seam-chain package installs `opener.InstallFailFast()` (the host opener's `xdg-open`/`open` spawn), so a
dropped opener installer is the accidental omission the derived check catches.

`internal/testutil/mocks` calls the wrapper unqualified from its `TestMain`, since a package cannot import itself.
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

Because that home is **fresh per test process**, a sandbox leaked by one run is invisible to the next: `WithReplace`
only replaces a same-name sandbox within one runtime home, and the teardown deletes the home, so a surviving VM can
never be reached by a later invocation. Three rules keep the msb-tagged tests from leaking:

- **Stop before removing.** The SDK contract is "`Remove` deletes the sandbox's persisted state. The sandbox must be
  stopped"; a detached sandbox (`WithDetached()`) keeps running past the Go process, so a bare `Remove` on a running
  one fails (and is easy to swallow) and the VM runs forever. Tests tear down with `SandboxHandle.Destroy`
  (identity-safe stop-and-remove), never `Remove`.
- **Sweep after the run.** `TestMain` destroys any sandbox left in the isolated home after `m.Run()` returns and
  before removing the home, so a test that failed or panicked without reaching its own cleanup cannot leak a VM
  pointing at a deleted home. It does **not** cover a hard kill — a `-timeout` expiry, SIGINT, SIGKILL, or an OOM
  exits the process without returning from `m.Run()`, so the sweep never runs and the idle-timeout backstop is what
  stops that VM. A failed sweep is reported and the home is kept, so the surviving sandbox stays reachable for manual
  cleanup instead of vanishing with the home. The sweep is a no-op when no test resolved the runtime, so
  `compile-all`'s `-run=^$` stays inert — the same guard `specs/harness-smoke-tests.md` gives its temp root.
- **Backstop with an idle timeout.** A sandbox a test creates also starts with `WithIdleTimeout`, so if the test
  binary is hard-killed — where neither `Destroy` nor the sweep can run — the runtime still stops the idle VM instead
  of leaving it up forever. The timer fires on *exec* silence (a sleeping job or a `Job.Inspect` poll does not reset
  it), so the value must exceed the test's own context budget; it is a stop, not a remove, so it reaps the process but
  not the persisted sandbox. The listener-free default (`IdleTimeout` zero) is unlimited.
- **Child processes borrow the parent's home.** A test that needs cross-process behaviour re-executes the test binary
  with the parent's home in its environment (`TestRealClientSandboxLifecycle`). The child points `MSB_HOME` at that home and
  never calls `ensureIsolatedRuntime`, so its own `TestMain` sweep stays a no-op and cannot destroy the parent's
  sandboxes.

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

## Ambient-environment gate

The fail-fast seams cover config, home, docker, msb, doctor, notify and the host opener. They do not cover
the ambient OS itself: a default-suite test can still open a real socket, read or write the process environment,
stat a device path, signal the process, or exec an absolute-path binary.
`TestUntaggedTestsReachNoUnsanctionedEnvironment` (`internal/testutil/ambient_environment_contract_test.go`)
closes that gap for known direct forms. It parses every `_test.go` the default build compiles and fails on such a call
unless the statement carries, or has immediately above it, a reason-bearing `//testenv:allow <reason>` marker,
mirroring the dead-code gate's `//deadcode:ignore`.

A marker sanctions the innermost statement that contains the call, so a formatter that wraps the call does not detach
the marker. It may sit inside that statement's own lines or on a standalone line immediately above it; a marker
elsewhere in the enclosing function does not sanction the call, and it must carry a reason (`//testenv:allow <reason>`;
a bare marker, or a longer word such as `allowance`, does not count). The gate covers these known direct forms:

- `net.Listen`/`Dial`/`Lookup*` (a real socket or name resolution; an `httptest` server is addressed through its
  own client and is not flagged);
- `os.Getenv`/`LookupEnv`/`Setenv`/`Unsetenv`/`Environ` (Go's testing package owns this surface through
  `t.Setenv`, so a direct call leaks across tests);
- `os.Open`/`OpenFile`/`Stat`/`Lstat`/`ReadFile`/`ReadDir`/`Readlink` of a `/dev/...` literal;
- `syscall.Kill`;
- `exec.Command`/`CommandContext` of an absolute-path literal (the existing ambient-tool check in
  `real_command_contract_test.go` still covers `docker`/`gh`/`git`/`gpg`/`msb`/`ssh`, and a shell interpreter is
  allowed there);
- a known host-figure read — `sysinfo.NumCPUs`/`TotalMemoryGiB`/`AvailableMemoryMiB` and
  `sandbox/vm.BootMaxMemoryMiB` (`hostProbeCalls`) — because such a read makes the test depend on the runner's
  hardware.

The scan is a source heuristic over known direct selector calls, not a proof of isolation. It does not see method
forms (`net.Dialer.Dial`, `net.DefaultResolver.LookupHost`, `net.ListenTCP`), an `http` client, `os.UserHomeDir`
(a `$HOME` read), a device path computed instead of written as a literal, or a shell interpreter running the command.
Those stay a review responsibility; the gate guards against the common direct form regressing.

`TestAmbientEnvironmentUses` pins the scanner and the marker semantics.

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

### Injected clients vs. the remaining seams

Per `client-injection.md`, the Docker client and the path resolver are explicit
parameters below each entry point, and `image` no longer imports `"testing"`:
its agent-release resolution is injected through `BuildOptions.ResolveVersion`,
and image's own tests override the unexported `defaultAgentVersionResolver`
default (via `withMockAgentVersion*`), never an exported hook. The
`docker.InstallFailFastGet` and `configpaths.InstallFailFastConfigPaths`
installers stay required: `docker.Get`/`configpaths.Get` are still the boundary
factories and still fail fast under tests, and the process-root residue named in
`client-injection.md` reads them.

The former exported behavior hooks (`vm.SetProjectVMLock`,
`vm.SetDaemonHealthFunc`, `prepare.SetAgentLatestVersion`,
`prepare.SetRuntimeHomeMigrationOffer`) are gone (#518). Their behavior is one
`options.Seams` value carried on `options.RunOptions` (the vm stages receive it
through `ProjectVMCreation.Seams` and the `SetUpSandbox` seams parameter); its
zero value is production, and each consumer falls back to its package var when a
field is nil. `cmd` threads the struct through its boundary `runSeams` var, so
no test outside a package mutates that package's state to stub these behaviors.
The package vars those seams default to (`daemonHealthFunc`,
`acquireProjectVMLock`, `agentLatestVersion`, `offerRuntimeHomeMigration`,
`buildVersionResolver`) stay unexported, and an in-package test that overrides
one must still restore it with `t.Cleanup`/`defer`.

The incident: `TestResolveBuildVersionPromptNamesThisProjectsVersion` replaced `agentLatestVersion` without restoring
it, so the coverage tests asserting the real default closure passed at `-count=1` (alphabetical file order runs them
first) but failed 19/20 at `-count=20`.

## Audit status (2026-11-06)

A full sweep of `internal/**` and `cmd/**` found the seam model holding: no production path outside the documented
`configpaths` exceptions (`mounts` `~/` sources, `docker`'s `~/.docker`) reads or writes host config/home state, and no
seam family is reached without its injectable factory. The remaining ambient paths are read-only or transient:

- `options.FirstFreeHostPort` probes a real loopback port through the `probeHostPort` var seam (added so tests inject a
  deterministic probe); production still binds/frees a transient socket. Read-only.
- `notify`'s backend runs the notifier through the `execCommand` var seam, whose `TestMain` now installs an
  `InstallFailFast` panic by default (tests opt in by overriding it); the temp WAV still goes to the real `os.TempDir`.
- `os.Getuid`/`Getgid` (image build args), `os.Getwd` (workspace source), `time.Now` (names/polling/spinner), and
  `viperconfig`'s env reads are read-only or the input itself, not leaks.
- The real `git` binary: tests that generate git artifacts — `internal/git`, `testutil.RunGit`, the bare-checkout CLI
  tests, and the prepare outside-worktree flows — live behind the `integration` build tag in `<unit>_integration_test.go`,
  so the default `go test ./...` executes no external command. `testutil.RunGit` also runs git with the developer's
  global and system config neutralized (`GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`, no askpass programs) and
  with git's per-invocation config channels cleared (`GIT_CONFIG_PARAMETERS` and the
  `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n` triplet, which stay active even with the files disabled), so
  a fixture commit can never reach commit signing or pop an askpass GUI (ksshaskpass). `TestUntaggedTestsExecuteNoAmbientTool`
  (`real_command_contract_test.go`) enforces the split in the default suite: it walks every `_test.go` the default build
  compiles and fails if one calls `testutil.RunGit`/`ConfigureRepo` — through a qualified or a same-package unqualified
  call — or execs an ambient tool (`git`, `ssh`, `gpg`, `gh`,
  `docker`, `msb`) named by an interpreted or raw string literal — a shell interpreter is allowed. A test that asserts on a
  git-generated artifact still pins the setting it depends on in its own fixture — a linked worktree's recorded gitdir is
  absolute only while `worktree.useRelativePaths` is false, and CI on a plain clone never exercises a developer's global
  setting (`TestResolveIdentityOutsideWorktreeReportsSharedGitDir`). Because those tests are `integration`-tagged they are
  outside the default coverage profile, so the test-support file `testgit.go` is excluded from the Codecov gate
  (`.codecov.yml`); its pure `gitIsolationEnvironment` is unit-tested in `testgit_test.go`.

A 2026-11-06 re-sweep of `internal/**` and `cmd/**` confirmed the model and closed the ambient-OS half with the
gate above. Every sanctioned use now carries a `//testenv:allow <reason>` marker. Three residuals remain:

- **Indirect host reads.** The gate catches a test's *direct* host-probe call, but a production function with no
  seam can still reach the host through a test's call path, which a scan of test sources cannot see.
  `sandbox.BootMaxMemoryMiB` (`sysinfo.TotalMemoryGiB` → `/proc/meminfo`) bounds the memory-raise offer, so
  `session`'s offer tests failed on any host with ≤ 8 GiB RAM (the boot maximum came out below the tests' fixed
  8192 MiB metric limit, `SuggestRaise` clamped to it, and the "already the host maximum" branch suppressed the
  prompt). The reads are now injected through the `bootMaxMemoryMiB`/`hostAvailableMemoryMiB` var seams, pinned in
  the package's `TestMain` like `waitQuiescentPollInterval`. When a production read of a host figure becomes
  reachable from a test, route it through a seam the same way rather than asserting against the runner's hardware.
  The gate's `hostProbeCalls` list should grow with each such production read, so a direct call from a test cannot
  reintroduce the dependency unnoticed.
- **Git identity from the ambient CWD.** Untagged `cmd/**` and `prepare`/`session` tests call
  `git.MustCheckoutSlug`/`MustRepoSlug`/`ResolveIdentity` on the suite's real checkout, so the suite must run inside
  the repo (the `Must*` variants panic otherwise). Read-only and self-consistent — the fixture lands under the mock
  state dir for that same key — but it is the last sizable ambient dependency not behind a seam. A
  `testutil.CheckoutSlug(t)` helper that owns the one intentional resolution (or `t.Chdir(testutil.InitRepo(t))`
  first) would centralize it.
- **Wall-clock waits.** Several tests `time.Sleep` through interval seams (`spinnerInterval`,
  `pressureWatchInterval`, `watchReconnectPolicy`); under load they are slower but not host-dependent. Prefer
  channel synchronization where a test does not need the interval dwell.

The gate also moved the `ports` exclusion tests off the hardcoded `5999` onto a bound-`:0` port, so they no longer
collide with whatever the host already holds.

`agent` and `filewalk` install every fail-fast family except `notify` (which imports both, so it cannot be installed
from them). Keep any future host-path need in them injected, never global.

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
