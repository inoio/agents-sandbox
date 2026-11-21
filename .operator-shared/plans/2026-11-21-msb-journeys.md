# msb journeys v1 (learn-mode acceptance suite) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the first slice of the harness-journeys acceptance harness (#793): a second `msb`-tagged suite (`test/msb/journeys`) that drives learn mode end to end on opencode2, the shared untagged `test/msb/driver`, the `test/harness`→`test/msb/startup` rename, the gate-scope changes, and the host-side KVM-guest tooling that runs the host-only suites.

**Architecture:** The `msb` build tag keeps naming the *runtime* (KVM + docker + network); scope lives in the package and make-target name. Shared driving support (pty session, isolation env, root/reuse-lock, readiness, harness table, launcher-binary build, image prebuild) moves into the untagged `test/msb/driver` so both the existing startup smoke and the new journeys suite use one driver and its unit tests run in the default `make test`. Each journey is a list of named `Step`s from one shared vocabulary, so a scenario reads declaratively and an agent extends or reorders it by adding a step — BDD's readability without a parser or a framework. The journeys suite drives the built launcher binary (never the SDK) through real VMs. A host-side provisioner (libvirt + cloud-init) builds a KVM guest and runs the suite inside it, because `/dev/kvm` is unusable in the agents-sandbox dev VM.

**Tech Stack:** Go 1.27; `github.com/creack/pty`; Cobra/Viper; microsandbox `msb`; Docker; libvirt/`virsh`/`qemu-img`/`cloud-localds`; cloud-init (Ubuntu cloud image); bash.

**Spec:** `.operator-shared/specs/testing/msb-journeys.md` (design). Supporting: `.operator-shared/specs/testing/harness-smoke-tests.md`, `.operator-shared/specs/testing/test-isolation.md`, `.operator-shared/specs/testing/shell-script-harnesses.md`, `.operator-shared/specs/testing/release-surface-testing.md`, `.operator-shared/specs/ci/quality-gates.md`, `.operator-shared/specs/onboarding/learn-mode.md` (+ subspecs), `.operator-shared/specs/onboarding/onboarding.md`.

## Global Constraints

- Go 1.27; follow `.operator-shared/operator.md`, `AGENTS.md`, repo code style. TDD: failing test first, watch it fail, implement, watch it pass.
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand. `make shellcheck` must stay green for every tracked `*.sh`.
- The build tag stays `msb`; `test/harness`→`test/msb/startup`, `test/msb/driver` is untagged (its unit tests run in the default `make test`), `test/msb/journeys` is `msb`-tagged.
- Every test package's `TestMain` installs its acyclic fail-fast mocks (`mocks.InitFailFastMocks`). `test/msb/driver` (non-test) is imported by both suites and never starts a VM under the default tag.
- The harness table (`Specs`) must stay in sync with `agent.Names()`; its completeness test stays untagged in `driver` and runs in the default suite.
- `AGENTS_SANDBOX_SMOKE_ROOT` is **kept by name**; `make test-msb` keeps its contract (every registered harness reaches its TUI and exits cleanly).
- Non-test `.go` files under `test/` are **test code**: no `//deadcode:ignore` on the driver, and the dead-code gate ignores `test/`; host-touching calls keep their reason-bearing `//testenv:allow` markers.
- The msb suites are host-only: `make compile-all` compiles them on every PR; only a KVM host runs them. Real sandbox runs are a **human accept** step, never claimed done by an agent.
- The terminating proxy observes a credential→destination pair only when the destination is **host-side** and reached **through** the proxy; a guest-loopback stub cannot produce attribution.
- Assertions are **semantic** (env-var name + host scoping in `env.secret.yaml`, `${ENVVAR}` in the managed config, expected config keys present), never byte-for-byte goldens.
- v1 covers **opencode2 only**; the other harnesses are recorded exemptions (the journey spec is a table field).
- A new tracked `scripts/*.sh` or a non-test `.go` file under `test/` counts as production code: add a `changelog.d/<PR>.<type>.md` fragment (docs-only `.operator-shared/`/`docs/` changes need none). If `ci/check-changelog.sh`'s exempt set is touched, the four exempt-path copies move together or `make ci-guards-check` fails.

## Review Focus

Input classes and failure modes the spec implies but no task's tests pin by default — each is added to the task that owns the code:

1. **A host-only run outside a KVM host.** `make test-msb-journeys` must skip (never fail) with no `/dev/kvm`, an unreachable docker daemon, or a non-Linux host — the same preflight contract the startup suite keeps.
2. **A tagged-only package silently dropped.** `go test ./...` drops a package with no default-build file; the table and completeness test must live in the untagged `driver` file set (Task 7's unit test asserts the completeness test runs under the default build).
3. **`test/` non-test code reaching the gates.** A new non-test `.go` under `test/` must not be reported by the dead-code gate (Task 4) and its host calls must be scanned by the ambient gates (Task 5), without the two disagreeing.
4. **A stubbed provider that never traverses the proxy.** The attribution journey must fail loudly when its destination is guest-loopback (Task 17's positive assertion that the observation non-empty is what proves the route).
5. **A journey that flakes under load.** No fixed wall-clock against a real VM; readiness/quit reuse the driver's marker/settle + repeated-quit-tap pattern, and a full-file golden is forbidden.

---

## Phase 0 — Host-side KVM-guest tooling (new scope)

**Why:** `/dev/kvm` does not work in the agents-sandbox dev VM, so the `msb` suites can only run on a KVM host. The repo has no provisioning story; this phase commits a libvirt + cloud-init provisioner and make targets so a human can build the guest and run the suites inside it repeatably. The guest *is* the KVM host: the launcher runs directly in it (never inside an agents-sandbox VM), and the microsandbox VMs it creates are nested.

### Task 1: `scripts/kvm-guest.sh` — libvirt guest provisioner + runner

**Files:**
- Create: `scripts/kvm-guest.sh` (mode 0755)

**Interfaces:**
- Produces: `scripts/kvm-guest.sh {up|ssh|sync|run <cmd...>|firewall|down}`; env overrides `VM_NAME` (default `asb-kvm`), `VM_VCPUS` (8), `VM_MEMORY_MIB` (16384), `VM_DISK_GIB` (80), `VM_USER` (dev), `LIBVIRT_URI` (`qemu:///system`), `IMAGE_BASE_URL` (Ubuntu cloud-image current), `IMAGE_URL`, `REPO_URL` (`https://github.com/inoio/agents-sandbox.git`), `REPO_BRANCH` (default `$(git rev-parse --abbrev-ref HEAD)` at run time, else `main`), `GO_VERSION` (read from `.go-version` when unset), `SSH_PUBKEY`, `ARTIFACT_DIR` (`$PWD/.kvm-guest` — add to `.gitignore`).
- Consumes: nothing (host tooling).

- [ ] **Step 1: Write the script.** Base it on the untracked spike provisioner at `/workspace/main/provision-kvm-guest.sh` (read it first; it already has the cloud-init seed, the `virsh define` domain XML, the nftables firewall fix, and the `up/ssh/firewall/down` subcommands). Change from the spike:
  - Header/usage names tooling for the `msb` suites, not the learn-mode spike. `set -euo pipefail`, `# Usage:` block.
  - Guest packages: `docker.io git make gcc build-essential ca-certificates curl jq`; drop `openssl`, `dnsmasq`, `pipx`, `python3-venv`, and `mitmproxy` (spike-only). Drop the `opencode2` install block: harness binaries are installed into runner images inside the microsandbox VMs, not on the guest.
  - `runcmd`: enable and start docker; add `$VM_USER` to `docker`/`kvm`; install Go `$GO_VERSION`; install `msb` (`https://install.microsandbox.dev`); `git clone $REPO_URL ~/agents-sandbox` and `git checkout $REPO_BRANCH`.
  - Add `sync`: `rsync -az --delete --exclude .git/ --exclude .kvm-guest/ "$(git rev-parse --show-toplevel)/" "$VM_USER@$ip:~/agents-sandbox/"` so a host checkout can drive the guest without a push.
  - Add `run <cmd...>`: `ssh "$VM_USER@$ip" -- "cd ~/agents-sandbox && $*"`; with no args, print usage and exit 2.
  - Print a next-steps block that runs `make build`, `msb doctor`, then `make test-msb-journeys`.
  - Keep `up`'s post-boot egress probe and the `firewall` name-keyed nft accepts (they are the known Docker/libvirt interaction).

- [ ] **Step 2: Verify shellcheck and syntax.** Run: `shellcheck scripts/kvm-guest.sh && bash -n scripts/kvm-guest.sh`. Expected: no output, exit 0.

- [ ] **Step 3: Commit.**

```bash
git add scripts/kvm-guest.sh
git commit -m "chore(test): add libvirt KVM-guest provisioner for the msb suites"
```

### Task 2: `scripts/kvm-guest.sh` fixture harness

**Files:**
- Create: `test/scripts/kvm_guest_test.go`
- Modify: `test/scripts/main_test.go` (register the `virsh`, `qemu-img`, `cloud-localds` fakes if not already registered)
- Modify: `Makefile` (`kvm-guest-test` target, `.PHONY`)
- Modify: `scripts/changed-targets.sh` (map `scripts/kvm-guest.sh` and `test/scripts/kvm_guest_test.go` → `kvm-guest-test`)

**Interfaces:**
- Consumes: `testutil`'s `RunScript(tb, dir, extraEnv, script, args...)` (`test/scripts/runscript_helpers_test.go`), the `Register` re-exec fakes (`test/scripts/main_test.go`).
- Produces: a `kvm-guest-test` selectable-only target (NOT in `CHECK_TARGETS`), like every other `*-test` harness.

- [ ] **Step 1: Write the failing tests.** Cover: usage with no args → exit 2 and a `Usage:` line; an unknown subcommand → exit 2; `up` writes the cloud-init seed and domain XML under `ARTIFACT_DIR` with the faked `qemu-img`/`cloud-localds`/`virsh`, and the seed contains the repo URL + branch + `docker.io`; `run` without args exits 2; `down` calls the faked `virsh destroy`/`undefine`. Use fakes registered in `main_test.go`, never a real hypervisor.

- [ ] **Step 2: Run to verify failure.** Run: `go test ./test/scripts/ -run KvmGuest -v`. Expected: FAIL (script/target missing).

- [ ] **Step 3: Implement the Make target and `changed-targets.sh` entry.**

```make
kvm-guest-test:
	CGO_ENABLED=1 go test ./test/scripts/ -run TestKvmGuest
```

- [ ] **Step 4: Run to verify pass.** Run: `make kvm-guest-test`. Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add test/scripts/kvm_guest_test.go test/scripts/main_test.go Makefile scripts/changed-targets.sh
git commit -m "test(scripts): cover scripts/kvm-guest.sh with fixtures"
```

### Task 3: Make targets and guide for the KVM-guest workflow

**Files:**
- Modify: `Makefile` (new host-only phony targets; NOT in `CHECK_TARGETS`)
- Create: `.operator-shared/guides/kvm-guest.md`
- Modify: `.operator-shared/catalog.md` (guide entry)
- Modify: `CONTRIBUTING.md` (a one-paragraph pointer for maintainers)
- Create: `changelog.d/<PR>.added.md`

**Interfaces:**
- Consumes: `scripts/kvm-guest.sh`.
- Produces: `make kvm-up`, `make kvm-sync`, `make kvm-ssh`, `make kvm-run`, `make kvm-down`; `KVM_RUN ?= make test-msb-journeys`.

- [ ] **Step 1: Add the targets.**

```make
KVM_GUEST ?= asb-kvm
KVM_RUN   ?= make test-msb-journeys

kvm-up:    ; scripts/kvm-guest.sh up
kvm-sync:  ; scripts/kvm-guest.sh sync
kvm-ssh:   ; scripts/kvm-guest.sh ssh
kvm-run:   ; scripts/kvm-guest.sh run '$(KVM_RUN)'
kvm-down:  ; scripts/kvm-guest.sh down
```

Add all five (and `kvm-guest-test`) to `.PHONY`; document in the `Makefile` that they are host-only and run no CI.

- [ ] **Step 2: Write the guide.** `.operator-shared/guides/kvm-guest.md`: why (no `/dev/kvm` in the dev VM), the one-time host prerequisites (`qemu-kvm libvirt-daemon-system cloud-image-utils genisoimage`, nested virt, user in `kvm`/`libvirt`), the workflow (`make kvm-up` → `make kvm-sync` → `make kvm-run` → `make kvm-down`), the firewall fix, and the human-accept rule. Frontmatter `description`/`read_if`. One-line catalog entry per `guides/operator-brain-maintenance.md`.

- [ ] **Step 3: Add the changelog fragment.** `changelog.d/<PR>.added.md` — one bullet: the KVM-guest provisioner + make targets.

- [ ] **Step 4: Verify.** Run: `make shellcheck && make brain-check && make docs-linkcheck`. Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add Makefile .operator-shared/guides/kvm-guest.md .operator-shared/catalog.md CONTRIBUTING.md changelog.d/
git commit -m "chore(test): make targets and guide for the KVM guest"
```

---

## Phase 1 — Gate scope and `test/` layout

### Task 4: Dead-code gate ignores `test/`

**Files:**
- Modify: `ci/deadcodecheck/main.go` (`Check`, and the source-path list in `run`)
- Test: `ci/deadcodecheck/main_test.go`

**Interfaces:**
- Consumes: `Finding.File` (module-relative), `Check(findings, markers, checkStale)`.
- Produces: findings whose `File` is under `test/` are not reported, and markers in `test/` files are not scanned (so no marker there is reported stale).

- [ ] **Step 1: Write failing tests.** A finding with `File: "test/msb/driver/spec.go"` yields no diagnostic; the same finding under `internal/` still does; a marker recorded from a `test/` path is neither required nor stale.

- [ ] **Step 2: Run to verify failure.** Run: `make deadcode-test`. Expected: FAIL.

- [ ] **Step 3: Implement.** In `Check`, skip a merged finding when `strings.HasPrefix(finding.File, "test/")`. Filter `test/`-prefixed paths out of the marker source list so a marker there cannot be reported stale.

- [ ] **Step 4: Run to verify pass + the whole gate.** Run: `make deadcode-test && make deadcode`. Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add ci/deadcodecheck/
git commit -m "chore(ci): ignore test/ findings in the dead-code gate"
```

### Task 5: Ambient gates scan non-test `.go` under `test/`

**Files:**
- Modify: `internal/testutil/ambient_environment_contract_test.go` (`scanDefaultBuildTestSources`)
- Modify: `internal/testutil/real_command_contract_test.go` (the ambient-tool scan, same source helper)
- Test: the respective `*_test.go` fixtures in `internal/testutil`

**Interfaces:**
- Consumes: `scanDefaultBuildTestSources(t, root, packages)` — the `!strings.HasSuffix(name, "_test.go")` guard at the walk.
- Produces: the scan includes every default-build `.go` file under `test/` (not only `_test.go`), keeping the `build.Default.MatchFile` default-build guard so `//go:build msb` driver files stay out.

- [ ] **Step 1: Write failing fixtures.** A non-test file `test/foo/helper.go` that calls `os.Getenv("HOME")` without a marker is a violation; the same with `//testenv:allow <reason>` passes; an `msb`-tagged file under `test/` is not scanned.

- [ ] **Step 2: Run to verify failure.** Run: `go test ./internal/testutil/ -run Ambient -v`. Expected: FAIL.

- [ ] **Step 3: Implement** the walk change: include a non-test `.go` file under `test/` while keeping the `MatchFile` guard. Extend to the ambient-tool scan in `real_command_contract_test.go` (same helper).

- [ ] **Step 4: Run to verify pass.** Run: `go test ./internal/testutil/ -run 'Ambient|RealCommand' -v`. Expected: PASS. Then `make lint` (the test package compiles under the lint tag set).

- [ ] **Step 5: Commit.**

```bash
git add internal/testutil/
git commit -m "test(testutil): scan non-test .go files under test/ for ambient escapes"
```

### Task 6: Production-closure contract test

**Files:**
- Create: `internal/testutil/production_closure_contract_test.go`

**Interfaces:**
- Consumes: `internal/testutil`'s existing `TestMain`; the module root resolver used by the module-wide contract scans.
- Produces: a test asserting `go list -deps ./cmd/agents-sandbox` (no `-test`) links no `testing` and no `*/testutil*` test-support package.

- [ ] **Step 1: Write the failing test** that runs `go list -deps ./cmd/agents-sandbox`, splits the output, and asserts none matches `^testing$` or a test-support package path.

- [ ] **Step 2: Run to verify it passes** (production closure is clean today). Run: `go test ./internal/testutil/ -run ProductionClosure -v`. Expected: PASS. If it fails, the failure is the real finding — stop and report.

- [ ] **Step 3: Self-check** the test fails when the rule is violated: temporarily add `_ "testing"` to `cmd/agents-sandbox/main.go`, confirm FAIL, revert.

- [ ] **Step 4: Commit.**

```bash
git add internal/testutil/production_closure_contract_test.go
git commit -m "test(testutil): assert the production closure links no testing"
```

### Task 7: Extract `test/msb/driver` — harness table, spec constants, output helpers

**Files:**
- Create: `test/msb/driver/spec.go`, `test/msb/driver/output.go`, `test/msb/driver/main_test.go` (TestMain mocks)
- Move tests: the untagged cases of `test/harness/harness_test.go` → `test/msb/driver/spec_test.go`
- Source: `test/harness/harness_helpers_test.go` (lines ~20–710), `test/harness/harness_test.go`

**Interfaces:**
- Produces (package `driver`): `type Spec struct { QuitKey []byte; Network string; KeepsNormalBuffer bool; ReadyMarker string; ReadyTimeout time.Duration; SeedHomeFiles, SeedHomeManifest, SeedAgentSnippets map[string]string }` (unchanged fields, comment block kept); `var Specs map[string]Spec`; `func (s Spec) ReadyTimeout() time.Duration` (was `readyTimeout()`); `const CtrlC, CtrlD byte`, `const PublicNetwork`; the ready-marker/config-path/smoke-config constants (exported as needed); `func StripANSI([]byte) string`, `func TUIStarted([]byte) bool`, `func SawDaemonTimeout([]byte) bool`, `func UsableTerm(string) string`; `type TB interface { Helper(); Logf(string, ...any); Fatalf(string, ...any); TempDir() string; Cleanup(func()) }` (add the other exported `testing` methods a moved function uses; never import `testing` in a non-test file).
- Driver non-test files import no `testing`.

- [ ] **Step 1: Create the package** and move the identifiers above, exporting as listed (mechanical rename `harnessSpec`→`Spec`, `harnessSpecs`→`Specs`). Keep comments.
- [ ] **Step 2: Move the untagged unit tests** (`TestHarnessSpecsCoverEveryRegisteredAgent`, `TestHarnessSpecsQuitKeyNonEmpty`, the seed-config tests, `TestReadyTimeout*`, `TestSawDaemonTimeout`, `TestStripANSI`, `TestTUIStarted`, plus the harness-specific seed tests) into `test/msb/driver/spec_test.go`, package `driver`. Add `main_test.go` with `mocks.InitFailFastMocks`.
- [ ] **Step 3: Run to verify.** Run: `make test`. Expected: PASS; the completeness test now runs in the default suite.
- [ ] **Step 4: Commit.**

### Task 8: Extract the pty session into `driver`

**Files:**
- Create: `test/msb/driver/pty.go`
- Move tests: `test/harness/pty_test.go` → `test/msb/driver/pty_test.go`
- Source: `test/harness/pty_helpers_test.go`

**Interfaces:**
- Produces (package `driver`): `type Session struct { … }` (was `ptySession`); `func StartUnderPTY(dir string, env, argv []string) (*Session, error)`; methods `Output() []byte`, `Write([]byte) error`, `WaitForTUI(time.Duration) error`, `ExitedWithoutTUIError() error`, `WaitSettled(time.Duration) error`, `WaitForMarker(string, time.Duration) error`, `WaitExit(time.Duration) (int, error)`, `HasExited() bool`, `Close()`, `DrainOutput(time.Duration)`, `ExitStatus() string`, `OutputTail() string`; plus `MarkerText`, `TerminalSize`, `TerminalDimension`, `KillProcessGroup`, `KillProcessGroupWith`, `ExitErrorCode`, `ExitCode`, `AltScreenEnter`, `EscapeByte`.

- [ ] **Step 1: Move and export** the pty session and helpers. Keep the `TCSANOW`/buffer comments.
- [ ] **Step 2: Move `pty_test.go`** into `driver`, package `driver`.
- [ ] **Step 3: Run to verify.** Run: `make test`. Expected: PASS.
- [ ] **Step 4: Commit.**

### Task 9: Extract env isolation and home seeding into `driver`

**Files:**
- Create: `test/msb/driver/env.go`
- Move tests: the corresponding untagged cases from `test/harness/harness_test.go`
- Source: `test/harness/harness_helpers_test.go` (`isolatedEnv`, `prepareHome`, `seedHomeFiles`, `seedHomeManifest`, `seedAgentSnippets`)

**Interfaces:**
- Produces (package `driver`): `func IsolatedEnv(root, homeBase string, inherited []string) []string`; `func PrepareHome(homeBase string) error`; `func SeedHomeFiles(homeBase string, files map[string]string) error`; `func SeedHomeManifest(homeBase string, files map[string]string) error`; `func SeedAgentSnippets(homeBase, agentConfigDirName string, snippets map[string]string) error`. Keep the private `isolatedEnvKeep` allowlist (its unit test is internal to package `driver`).

- [ ] **Step 1: Move and export.** Keep the allowlist comments.
- [ ] **Step 2: Move the tests** (`TestIsolatedEnv*`, `TestPrepareHome*`, `TestSeedHome*`, `TestSeedAgentSnippets`) into `test/msb/driver/env_test.go`, package `driver`.
- [ ] **Step 3: Run to verify.** Run: `make test`. Expected: PASS.
- [ ] **Step 4: Commit.**

### Task 10: Extract root/reuse-lock, launcher build, preflight, prebuild, cleanup into `driver`

**Files:**
- Create: `test/msb/driver/root.go`, `test/msb/driver/launcher.go`, `test/msb/driver/images.go`
- Move tests: `TestResolveSuiteRoot*`, `TestRootFitsSandboxSocket*`, `TestSmokeRootStaysShortEnough*`, `TestHarnessConcurrencyForFreeMemory`, `TestHarnessRunArgsCapTheSmokeMemory` from `test/harness/harness_test.go` → `test/msb/driver/root_test.go`
- Source: `test/harness/harness_helpers_test.go` (root/lock/concurrency/run-args), `test/harness/runner_images_helpers_test.go`, `test/harness/smoke_msb_test.go` (`preflight`, `preflightDocker`, `prepare`, `createRootAndBuildBinary`, `prebuildImages`, `buildHarnessImages`, `cleanupSandboxes`, `boundedLauncher`, `harnessConcurrency`, `removeTempRoot`, `quit`, `waitReady`)

**Interfaces:**
- Produces (package `driver`): `const SmokeRootReuseEnv`; `func ResolveSuiteRoot() (string, bool, func(), error)`; `func MsbHomeUnderRoot(string) string`; `func RootFitsSandboxSocket(string) bool`; `func ReuseLockPath(string) string`; `func AcquireReuseLock(string) (func(), error)`; `const SmokeMemoryGiB, SmokeWorkingSetMiB, HarnessHostReserveMiB`; `const HarnessConcurrencyOverrideEnv`; `func SmokeMemorySpec() string`; `func HarnessConcurrencyFor(int) int`; `func HarnessRunArgs(name, network string) []string`; `func HarnessConcurrency() int`; `func ProjectDir(root, name string) string`; `type ProjectAgent struct { … }`; `func SuiteProjectAgents() []ProjectAgent`; `func HarnessRunnerImageTags(root string) ([]string, error)`; `func CleanupRunnerImages(tb TB, root string)`; `func Preflight(tb TB)`; `func PreflightDocker(tb TB, root string)`; `func Prepare(tb TB) (root, binary string)`; `func CreateRootAndBuildBinary() `; `func PrebuildImages(tb TB, root string)`; `func BuildHarnessImages(tb TB, root string) error`; `func CleanupSandboxes(tb TB, root, binary string)`; `func BoundedLauncher(root, binary string, args ...string) (*exec.Cmd, context.CancelFunc)`; `func Quit(ses *Session, spec Spec) (int, error)`; `func WaitReady(ses *Session, spec Spec) error`.
- Host-touching calls carry their existing `//testenv:allow` markers verbatim (`reuseLock`'s env read, `removeDockerImage`'s real `docker rmi`, `cleanupRunnerImages`'s env build).

- [ ] **Step 1: Move and export** the three files' content, replacing `testing.TB`/`*testing.T` with `TB`.
- [ ] **Step 2: Move the root/lock unit tests** into `test/msb/driver/root_test.go`.
- [ ] **Step 3: Run to verify.** Run: `make test && make lint`. Expected: PASS (driver compiles untagged; no `testing` import in non-test files).
- [ ] **Step 4: Commit.**

### Task 11: Rename `test/harness` → `test/msb/startup` and rewire

**Files:**
- Move: `test/harness/{main_test.go,smoke_msb_test.go,docker_storage_msb_test.go}` → `test/msb/startup/` (package `startup`)
- Modify: `Makefile` (`test-msb`, `test-msb-cold` package lists → `./test/msb/startup/... ./internal/sandbox/msb/...`; comments referencing `test/harness`)
- Modify: `.operator-shared/index/index.md`, `.operator-shared/specs/testing/harness-smoke-tests.md`, `.operator-shared/specs/testing/msb-journeys.md`, `.operator-shared/specs/testing/release-surface-testing.md`, `.operator-shared/specs/testing/shell-script-harnesses.md`, `.operator-shared/specs/testing/test-isolation.md`, `.operator-shared/specs/agents/hermes-agent.md`, `.operator-shared/guides/test-runtime-cost.md`, `.operator-shared/plans/2026-11-07-hermetic-test-suite-design.md` (`test/harness`→`test/msb/startup`, `test/msb/driver` where the helpers moved)
- Modify: `AGENTS.md` (the `make test-msb` paragraph path)
- Create: `changelog.d/<PR>.changed.md`

**Interfaces:**
- Consumes: everything `driver` produces (Task 7–10).
- Produces: startup package `startup`; `TestHarnessSmoke` and `docker_storage_msb_test.go` import `driver` for helpers.

- [ ] **Step 1: Move the files** and rename the package clause to `startup`.
- [ ] **Step 2: Rewire** each helper call to `driver.<Exported>`, and the `createRootAndBuildBinary`/`preflight`/`prepare`/`cleanup` calls in `TestHarnessSmoke` to the driver exports. `TestMain` stays in `main_test.go` with `mocks.InitFailFastMocks`.
- [ ] **Step 3: Update the Makefile** package lists and the comment references.
- [ ] **Step 4: Sweep the docs/brain references** listed above; run `git grep -n "test/harness"` and resolve every hit except the design spec's own rename prose (which names the old path deliberately). `make docs-linkcheck` must stay green.
- [ ] **Step 5: Add the changelog fragment.**
- [ ] **Step 6: Verify.** Run: `make test && make compile-all && make lint && make deadcode && make docs-linkcheck`. Expected: PASS. (The `msb` suites themselves are host-only; this proves they compile.)
- [ ] **Step 7: Commit.**

### Task 12: `make test-msb-journeys`, the journey/step model, and the skeleton

**Design call (the BDD alternative, folded in):** journeys are composed from small **named steps** drawn from one shared vocabulary, so a maintainer reviews a scenario and an agent extends it by adding or reordering one closure. This gets BDD's readability and composability without a parser, a `godog` dependency, or a third artifact between the spec and the test. `go test -v` prints the scenario; the surface sweep stays the prose, agent-authored, extensible twin.

**Files:**
- Create: `test/msb/journeys/main_test.go` (TestMain mocks), `test/msb/journeys/model.go` (`Journey`/`Step`/`Context`), `test/msb/journeys/steps.go` (the shared vocabulary), `test/msb/journeys/table.go` (the journey list + exemptions), `test/msb/journeys/journeys_msb_test.go` (the host-only runner)
- Modify: `Makefile` (new `test-msb-journeys` target), `.PHONY`
- Modify: `.operator-shared/index/index.md` (journeys entry)

**Interfaces:**
- Consumes: `driver`'s `Preflight`, `Prepare`, `PrebuildImages`, `ResolveSuiteRoot`, `Specs`, `BoundedLauncher`, `Session`.
- Produces:
  - target `test-msb-journeys` (`CGO_ENABLED=1 go test -tags msb -timeout 60m -count=1 ./test/msb/journeys/...`) with the same `AGENTS_SANDBOX_SMOKE_ROOT=$(MSB_REUSE_ROOT)/smoke`, `AGENTS_SANDBOX_MSB_TEST_HOME=$(MSB_REUSE_ROOT)/msb`, `AGENTS_SANDBOX_MSB_PARALLEL=$(MSB_PARALLEL)` env as `test-msb`;
  - the model (untagged, so the completeness test runs in the default suite):

    ```go
    type Context struct {
        TB      driver.TB        // the *testing.T the runner passed in
        Root    string           // the suite root from driver.Prepare
        Binary  string           // the built launcher
        Spec    driver.Spec      // driver.Specs[agent]
        Home    string           // this journey's isolated HOME base
        Session *driver.Session  // set by StepLaunch
        Stub    StubProvider     // set by StepSeedStub (Task 13)
    }
    type Step struct {
        Name string
        Run  func(*Context) error
    }
    type Journey struct {
        Name  string
        Agent string
        Steps []Step
    }
    var Journeys []Journey
    ```

  - `steps.go` — one exported `func Step<Verb>(...) Step` constructor per action (the shared vocabulary): `StepFreshHome`, `StepSeedHostConfig(files map[string]string)`, `StepSeedStub`, `StepLaunch(args ...string)`, `StepChooseFirstRun(choice firstrun.Choice)`, `StepWaitReady`, `StepSendPrompt(prompt string)`, `StepQuit`, `StepChooseExit(key string)`, `StepAssertFirstRunOffered(choice firstrun.Choice)`, `StepAssertScopeWritten(want ScopeWant)`, `StepAssertNoProjectScaffolding`, `StepAssertSessionCleared`, `StepAssertNetworkNotWidened(after network.Policy)`. `ScopeWant struct { EnvVar string; Hosts []string; ConfigKeys []string; Placeholder bool }`. A step that needs a facility the vocabulary lacks is added to `steps.go`, never inlined in a journey file.
  - the opencode2 exemptions recorded (every `agent.Names()` entry not covered by a journey, with a reason).
- Contract: a step returns an error; the runner aborts the journey there and reports the failing step's `Name`, so a scenario reads top-to-bottom and a failure names the step — no parser, no framework.

- [ ] **Step 1: Write the failing test.** A table-completeness test in `table_test.go` asserting every `agent.Names()` entry is either covered by a journey or carries a recorded exemption (mirroring the startup completeness test) — this runs **untagged** in the default suite (put `model.go`/`steps.go`/`table.go` in non-`msb` files so the default suite keeps them, like `driver`). Add a model test that every step in every journey has a non-empty `Name` and a non-nil `Run`.
- [ ] **Step 2: Verify it fails**, then implement `model.go`, the vocabulary in `steps.go` (wired to `driver`), and `table.go` with the journey list + the opencode2 exemption list.
- [ ] **Step 3: Add the target** and the tag-side runner in `journeys_msb_test.go`: `driver.Preflight` (skip, never fail, without `/dev/kvm`/docker), `driver.Prepare`, `driver.PrebuildImages`, then a `t.Run(journey.Name)` per journey that builds a `Context` and runs the steps in order.
- [ ] **Step 4: Verify compile.** Run: `make compile-all && make test`. Expected: PASS (no VM run in CI).
- [ ] **Step 5: Commit.**

---

## Phase 2 — Learn-mode journeys

### Task 13: Prerequisite spike — a real turn through the host-owned proxy

**Files:**
- Create: `test/msb/journeys/stub_provider.go` (the host-side stub destination + `StubProvider` type), `test/msb/journeys/stub_provider_msb_test.go`
- Modify: `test/msb/journeys/steps.go` (add `StepSeedStub`, filled in here)
- Create: `changelog.d/<PR>.added.md` (fold into Task 3's or add here)

**Interfaces:**
- Consumes: `channel.StartObservation` / `network.Policy` overlay semantics (the proxy is host-side; the VM reaches it via `network.proxy`).
- Produces: `type StubProvider interface { Addr() string; ProviderConfig() map[string]string; Stop() }` and `func StartStubProvider(tb driver.TB) (StubProvider, error)` — a host destination the VM can reach through the terminating proxy plus the opencode2 provider config that points the agent at it; `StepSeedStub` starts it and stores it on `Context.Stub`. A recorded decision (in `msb-journeys.md`) names the stub's protocol, the CA/`skip-upstream-verify` setting, and the provider config.

- [ ] **Step 1: Record the spike as a host-gated test.** The test builds the launcher, writes an isolated env + an opencode2 config whose provider `baseURL` points at the stub, runs `run --learn`, sends one prompt, exits, and asserts the saved observation (`channel.Save`'s `<state>/learn/<checkoutSlug>/<unix>.json`) contains a destination for the stub host. This is a **human accept** on a KVM host; the test is `msb`-tagged and compiles in CI.
- [ ] **Step 2: Decide the stub shape** from the spike outcome (plain HTTP through `CONNECT` vs HTTPS with `network.observe.skip-upstream-verify`). Write the decision into `.operator-shared/specs/testing/msb-journeys.md` §"Provider for a journey" (replace the "left to the builder" note for the stub wiring). **Do not proceed to Task 17 until this is recorded and the human has accepted the host run.**
- [ ] **Step 3: Commit.**

### Task 14: UC1 journey — fresh install → menu → save

**Files:**
- Create: `test/msb/journeys/uc1.go` (`var uc1Journey`)
- Modify: `test/msb/journeys/table.go` (add `uc1Journey` to `Journeys`)

**Interfaces:**
- Consumes: the `steps.go` vocabulary; `StartStubProvider` via `StepSeedStub`.
- Produces: `var uc1Journey Journey`, the scenario below.

- [ ] **Step 1: Write the journey as a step list** (the first failing assertion is in `StepAssertScopeWritten`'s `ScopeWant`):

  ```go
  var uc1Journey = Journey{
      Name: "uc1-learn-by-using", Agent: "opencode2",
      Steps: []Step{
          StepFreshHome,                                   // no host config, no managed layer
          StepSeedStub,                                    // Task 13
          StepLaunch("run", "--learn"),
          StepChooseFirstRun(firstrun.ChoiceLearnByUsing), // label "Learn by using it"
          StepWaitReady,
          StepSendPrompt(uc1Prompt),
          StepQuit,
          StepChooseExit(learn.ExitSaveAllKey),            // "Save all"
          StepAssertScopeWritten(ScopeWant{
              EnvVar: "OPENCODE_API_KEY", Hosts: []string{stubHost}, ConfigKeys: []string{...}, Placeholder: true,
          }),
          StepAssertNoProjectScaffolding,                  // #252
      },
  }
  ```
  Add any step the scenario needs to `steps.go` (never inline it).
- [ ] **Step 2: Assert the semantic effects** in `StepAssertScopeWritten` against the per-slug scope (`configpaths.UserSlugConfigDir(slug)`, slug = repo slug): `env.secret.yaml` carries the stub credential's env-var name with its host scoping; the managed config carries the `${ENVVAR}` placeholder; the expected config keys are present. Never a full-file golden.
- [ ] **Step 3: Assert no first-run scaffolding** (the #252 assertion, in `StepAssertNoProjectScaffolding`): no `.agents-sandbox` created in the project; the decision recorded once under `<repo slug>/<agent>` in `RootState`.
- [ ] **Step 4: Human accept on a KVM host** (`make kvm-run KVM_RUN='make test-msb-journeys'`). Record the accept in the PR.
- [ ] **Step 5: Commit.**

### Task 15: UC4 journey — host config seeds the scope

**Files:**
- Create: `test/msb/journeys/uc4.go` (`var uc4Journey`)
- Modify: `test/msb/journeys/table.go` (add `uc4Journey`)

**Interfaces:**
- Consumes: `firstrun.SeedFromHost` behavior via the production seam (the CLI tests already drive it: `cli_run_options_test.go:TestRunFirstRunMenuSeedsFromHostAndStartsLearning`).
- Produces: `var uc4Journey Journey`.

- [ ] **Step 1: Write the journey** as a step list: `StepSeedHostConfig(hostConfigWithStaticKey)`, `StepSeedStub`, `StepLaunch("run", "--learn")`, `StepAssertFirstRunOffered(firstrun.ChoiceLearnFromHost)` (the default when host config exists; label "Learn from my host setup"), `StepChooseFirstRun(firstrun.ChoiceLearnFromHost)`, `StepWaitReady`, `StepSendPrompt`, `StepQuit`, `StepChooseExit(learn.ExitSaveAllKey)`, `StepAssertScopeWritten(...)`. Register it in `table.go`.
- [ ] **Step 2: Assert** `firstrun.SeedFromHost` pre-populated the per-slug scope **before the session**: placeholdered config + a static secret in `env.secret.yaml`; a login store is **not** copied.
- [ ] **Step 3: Human accept; commit.**

### Task 16: `config learn` journey — replay a recorded session

**Files:**
- Create: `test/msb/journeys/config_learn.go` (`var configLearnJourney`)
- Modify: `test/msb/journeys/table.go` (add `configLearnJourney`)

**Interfaces:**
- Consumes: `state.RecordLearnSession`, `state.WithHomeState` (seed `HomeVolume`), `readback.HomeVolumePath` / the `cmd` seams the CLI tests use (`seedLearnState`, `stubLearnHomeVolume` in `cli_config_test.go`).
- Produces: `var configLearnJourney Journey`.

- [ ] **Step 1: Write the journey** as a step list: seed a `state.LearnSession` record **and a real home volume** (never abnormally kill a launcher) via a `StepSeedLearnSession` step, `StepLaunch("config", "learn")`, drive the review path (`StepChooseExit`), then `StepAssertScopeWritten` + `StepAssertSessionCleared`.
- [ ] **Step 2: Assert the owning-process-free replay** clears the record and deletes the artefact after analysis (`StepAssertSessionCleared`).
- [ ] **Step 3: Human accept; commit.**

### Task 17: Attribution journey — learned proposal from an observed pair

**Files:**
- Create: `test/msb/journeys/attribution.go` (`var attributionJourney`)
- Modify: `test/msb/journeys/table.go` (add `attributionJourney`)

**Interfaces:**
- Consumes: `StepSeedStub`, `analyser.ObservationFindings`/`Apply` behavior, `learn.Analysis`.
- Produces: `var attributionJourney Journey`.

- [ ] **Step 1: Write the journey** as a step list: `StepFreshHome`, `StepSeedHostConfig(providerConfigPointingAtStub)` (so the credential has a config-entry source but no hosts), `StepSeedStub`, run `--learn` against the stub, send the prompt, exit, accept, then `StepAssertScopeWritten` with the stub host and `StepAssertNetworkNotWidened(currentPolicy)`.
- [ ] **Step 2: Assert the positive route** (Review Focus 4, in `StepAssertScopeWritten` for this journey): the observation is non-empty for the stub host — a guest-loopback destination would leave it empty and fail. Attribution supersedes the config-entry fallback.
- [ ] **Step 3: Human accept; commit.**

---

## Phase 3 — Surface sweep and finalize

### Task 18: Surface sweep learns the journeys block + spine step

**Files:**
- Modify: `.operator-shared/specs/testing/release-surface-testing.md` (a learn-mode journey block + a spine step)
- Modify: `.operator-shared/specs/testing/msb-journeys.md` §"Exploratory tier and the promotion flow" if the interface text needs settling

**Interfaces:**
- Consumes: `make test-msb-journeys`; the shared-root interface (`AGENTS_SANDBOX_SMOKE_ROOT=<sweep>/smoke`, `AGENTS_SANDBOX_MSB_TEST_HOME=<sweep>/msb`).

- [ ] **Step 1: Add the spine step** that runs `make test-msb-journeys` once (warm the shared root), passing the sweep root through the two env vars the driver already resolves.
- [ ] **Step 2: Add the learn-mode journey block** in the existing `## Task N [I|M]:` format, with the `tmux` interaction, expected outcome, and evidence command per step; findings go to `<run>/findings.md`.
- [ ] **Step 3: Verify** `make docs-linkcheck && make brain-check`. Expected: PASS.
- [ ] **Step 4: Commit.**

### Task 19: Finalize — docs, brain, changelog, whole-branch review

**Files:**
- Modify: `.operator-shared/specs/testing/msb-journeys.md` (status/prose), `.operator-shared/catalog.md` (if a new plan/guide is listed), `.operator-shared/index/index.md`
- Create/Modify: `changelog.d/<PR>.*.md`

- [ ] **Step 1: Update the brain** to state what is now true: the suites, the target, the driver, the KVM tooling, and the residual exemptions (other harnesses).
- [ ] **Step 2: Run the full gate.** Run: `make check && make verify`. Expected: PASS.
- [ ] **Step 3: Whole-branch review** with `code-review`/`reviewer-large`.
- [ ] **Step 4: Open the PR** into the merge queue (`scripts/enqueue-pr.sh`); attach the host-accept evidence.

---

## Self-Review

**Spec coverage:** layout + rename (T11); shared driver (T7–10); gate scope + production closure (T4–6); `make test-msb-journeys` + skeleton (T12); prerequisite proxy spike (T13); read-back journeys UC1/UC4/`config learn` (T14–16); attribution journey (T17); surface sweep (T18); #252 absorbed (T14 Step 3). New scope: KVM dev tooling (T1–3).

**Type consistency:** `driver.Spec`/`Specs`, `driver.Session`, `driver.TB`, `driver.Prepare`/`PrebuildImages`/`Preflight`, `StartStubProvider`/`StubProvider`, and the journey model `Journey`/`Step`/`Context`/`ScopeWant` are used consistently across tasks. Every journey is a `Journey` of `Step`s drawn from `steps.go`; no journey inlines a step.

**Known risk:** T13 is genuinely unknown until run on a KVM host; T17 is gated on it. The read-back journeys (T14–16) are independent and can land in parallel.
