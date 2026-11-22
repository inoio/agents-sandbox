# msb smoke-remedies Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the in-guest `make kvm-run` smoke suite reliable on a slow nested guest: clear a reused root's leftover sandboxes before the run, give each VM a longer readiness budget, and let the harness's own ceilings scale.

**Architecture:** Four independent changes. (1) A start-of-run sandbox sweep in `TestHarnessSmoke`. (2) Two launcher config keys (`docker-ready-timeout`, `daemon-ready-timeout`) threaded through `RunOptions` into `internal/sandbox/vm`'s detached-service readiness. (3) A harness-side `AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR` that scales the smoke suite's readiness and quit ceilings. (4) A one-line fix of a stale daemon-timeout marker. The KVM guest target (`make test-msb-in-vm`) sets all three knobs; the host targets stay byte-for-byte unchanged.

**Tech Stack:** Go 1.27; Viper/Cobra config; the `msb`-tagged harness suite; GNU make.

**Spec:** `.operator-shared/specs/testing/harness-smoke-tests.md`, `.operator-shared/specs/configuration/config-keys.md`, `.operator-shared/guides/kvm-guest.md`, `.operator-shared/specs/vm/lifecycle.md`

## Global Constraints

- Host targets (`make test-msb`, `make test-msb-cold`) must behave exactly as today: every new launcher/harness knob defaults to the current value, and an unset env var must append no flag and change no ceiling.
- The KVM guest is the only consumer of the new knobs; the Makefile's `test-msb-in-vm` target is the only place that sets them.
- Config keys follow the derived registry: a `mapstructure`-tagged `Config` field gets an `AGENTS_SANDBOX_<KEY>` env var, a flag only if a flag with the exact key name is registered, and its flag-default type comes from the field's Go kind. `internal/viperconfig/configkeys_test.go`'s golden list must be updated in the same commit.
- A zero value for either readiness timeout means "use the package default" (`dockerdReadyTimeout` / `daemonReadyTimeout`, the test seam). A negative value is rejected at config load.
- Test-only changes (issues 1, 3, the marker) need no changelog fragment; the new config keys do (`changelog.d/<n>.added.md`).
- Run `make fmt` then `make check` before finalizing; `make check` is the gate.

## Review Focus

- **A reused root whose leftovers cannot be listed.** `sandbox list -q` can itself fail (observed: `cleanup: list sandboxes: exit status 1`) when a sandbox is mid create/cleanup or the runtime is wedged. The sweep must log and continue, never fail the suite, and the run must still proceed to the subtests.
- **An unset factor or timeout scaling a ceiling to zero.** `timeoutFactorFromValue` must return 1 for unset/empty/zero/negative/non-numeric, and `scaleTimeout` must be the identity at factor 1; a 0 ceiling would make every harness fail instantly.
- **A negative ready timeout reaching the launcher.** `--docker-ready-timeout=-1s` must be rejected at config load, not silently produce an immediately-expiring readiness deadline.
- **Env-vs-flag precedence for the new keys.** With a flag default bound to viper, an unset flag must not shadow an env/config value (the registry's default-mirroring).
- **The docker-storage sibling's ceilings.** It uses its own `tuiReadyTimeout`/`quitTimeout` reads; if only `TestHarnessSmoke` scales its ceilings, the sibling stays fixed and still fails on a slow guest.

---

### Task 1: Fix the stale daemon-timeout marker

The suite asserts on `"did not become healthy"`, but the launcher prints `"<label> did not become ready within <timeout>"` (`internal/sandbox/vm/detached.go:194`), so `sawDaemonTimeout` never fires on real output.

**Files:**
- Modify: `test/harness/harness_helpers_test.go:21-23`
- Test: `test/harness/harness_test.go` (`TestSawDaemonTimeout`, around line 470)
- Modify: `.operator-shared/specs/testing/harness-smoke-tests.md:15,47`

**Interfaces:**
- Produces: `daemonTimeoutMarker = "did not become ready"` (const, still consumed by `sawDaemonTimeout`).

- [ ] **Step 1: Update the test to the launcher's real phrasing**

In `TestSawDaemonTimeout`, replace the `launcherPhrase` literal with the launcher's actual wording and assert the marker is a substring of it:

```go
launcherPhrase := "opencode2 daemon did not become ready within 1m0s: 401 unauthorized"
if !strings.Contains(launcherPhrase, daemonTimeoutMarker) {
    t.Fatalf("test phrase %q does not carry the launcher's marker %q", launcherPhrase, daemonTimeoutMarker)
}
if !sawDaemonTimeout([]byte("launcher output\n" + launcherPhrase)) {
    t.Error("sawDaemonTimeout = false, want true for the launcher's daemon-timeout phrase")
}
if sawDaemonTimeout([]byte("launcher output\nno daemon timeout here")) {
    t.Error("sawDaemonTimeout = true, want false for unrelated output")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -run TestSawDaemonTimeout ./test/harness/ -count=1`
Expected: FAIL — the phrase does not contain `daemonTimeoutMarker`.

- [ ] **Step 3: Fix the constant and its comment**

In `harness_helpers_test.go` change the const to `"did not become ready"` and point the comment at the production source (the detached-service readiness timeout, `internal/sandbox/vm/detached.go`).

- [ ] **Step 4: Run it to verify it passes**

Same command as step 2. Expected: PASS.

- [ ] **Step 5: Update the spec**

In `harness-smoke-tests.md`, change both `did not become healthy` references (line 15 prose, line 47 marker) to `did not become ready`.

- [ ] **Step 6: Commit**

```bash
git add test/harness/harness_helpers_test.go test/harness/harness_test.go .operator-shared/specs/testing/harness-smoke-tests.md
git commit -m "fix(test): match the smoke suite's daemon-timeout marker to the launcher"
```

---

### Task 2: Scale the smoke suite's readiness and quit ceilings

**Files:**
- Modify: `test/harness/harness_helpers_test.go`
- Modify: `test/harness/smoke_msb_test.go`
- Modify: `test/harness/docker_storage_msb_test.go`
- Test: `test/harness/harness_test.go`
- Modify: `Makefile`
- Modify: `.operator-shared/specs/testing/harness-smoke-tests.md`, `.operator-shared/guides/kvm-guest.md`

**Interfaces:**
- Produces: `smokeTimeoutFactorEnv = "AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR"`; `timeoutFactorFromValue(value string) float64`; `scaleTimeout(d time.Duration, factor float64) time.Duration`; `smokeTimeoutFactor() float64` (msb-tagged).

- [ ] **Step 1: Write the failing tests**

In `harness_test.go`:

```go
func TestTimeoutFactorFromValue(t *testing.T) {
    for _, tc := range []struct{ name, value string; want float64 }{
        {"unset", "", 1}, {"zero", "0", 1}, {"negative", "-2", 1},
        {"non-numeric", "fast", 1}, {"double", "2", 2}, {"fractional", "1.5", 1.5},
        {"padded", "  3  ", 3},
    } {
        t.Run(tc.name, func(t *testing.T) {
            if got := timeoutFactorFromValue(tc.value); got != tc.want {
                t.Errorf("timeoutFactorFromValue(%q) = %v, want %v", tc.value, got, tc.want)
            }
        })
    }
}

func TestScaleTimeout(t *testing.T) {
    if got := scaleTimeout(15*time.Second, 1); got != 15*time.Second {
        t.Errorf("scaleTimeout(f=1) = %s, want 15s", got)
    }
    if got := scaleTimeout(15*time.Second, 2); got != 30*time.Second {
        t.Errorf("scaleTimeout(f=2) = %s, want 30s", got)
    }
    if got := scaleTimeout(15*time.Second, 0.5); got != 7500*time.Millisecond {
        t.Errorf("scaleTimeout(f=0.5) = %s, want 7.5s", got)
    }
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -run 'TestTimeoutFactorFromValue|TestScaleTimeout' ./test/harness/ -count=1`
Expected: FAIL — undefined: `timeoutFactorFromValue`, `scaleTimeout`.

- [ ] **Step 3: Implement the pure helpers**

In `harness_helpers_test.go` add `smokeTimeoutFactorEnv`, `timeoutFactorFromValue` (positive `strconv.ParseFloat` else 1), and `scaleTimeout` (identity when `factor == 1`, else `time.Duration(float64(d) * factor)`).

- [ ] **Step 4: Run to verify they pass**

Same command as step 2. Expected: PASS.

- [ ] **Step 5: Read the factor once and apply it**

In `smoke_msb_test.go` add `smokeTimeoutFactor()` reading the env via `timeoutFactorFromValue(os.Getenv(smokeTimeoutFactorEnv))`. In `waitReady`, multiply `spec.readyTimeout()` and `tuiReadyTimeout` by the factor. In `quit`, compute the deadline from `scaleTimeout(quitTimeout, smokeTimeoutFactor())`. In `docker_storage_msb_test.go`, scale its `tuiReadyTimeout` and `quitTimeout` reads by the same factor.

- [ ] **Step 6: Wire the guest target**

In the `Makefile`: add `MSB_GUEST_TIMEOUT_FACTOR ?= 2` beside `MSB_GUEST_VM_CPUS`, and add `AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR=$(MSB_GUEST_TIMEOUT_FACTOR) \` to the `test-msb-in-vm` recipe.

- [ ] **Step 7: Run the harness package and the Makefile dry-run**

Run: `CGO_ENABLED=1 go test -tags microsandbox_ffi_path ./test/harness/ -count=1` and `make -n test-msb-in-vm | grep TIMEOUT_FACTOR`
Expected: PASS; the recipe shows `AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR=2`.

- [ ] **Step 8: Update the spec and the KVM guide**

In `harness-smoke-tests.md` (Isolation/tag section) and `guides/kvm-guest.md` (the `test-msb-in-vm` paragraph) document the factor, the ceilings it scales, and that the press/retry cadence is deliberately unscaled.

- [ ] **Step 9: Commit**

```bash
git add test/harness Makefile .operator-shared/specs/testing/harness-smoke-tests.md .operator-shared/guides/kvm-guest.md
git commit -m "test(harness): scale the smoke suite's readiness and quit ceilings in-guest"
```

---

### Task 3: Launcher readiness-timeout config keys

Add `docker-ready-timeout` and `daemon-ready-timeout`: config keys with env vars, CLI flags, validation, and plumbing into the two detached-service readiness waits.

**Files:**
- Modify: `internal/viperconfig/config.go` (fields + key consts)
- Modify: `internal/viperconfig/validate.go`, `internal/viperconfig/validate_test.go`
- Modify: `internal/viperconfig/configkeys_test.go`
- Modify: `internal/sandbox/options/options.go`
- Modify: `internal/runoptions/runoptions.go`
- Modify: `internal/sandbox/vm/dockerd.go`, `daemon.go`, `setup_sandbox.go`
- Modify: `internal/sandbox/vm/dockerd_test.go`, `daemon_test.go`, `setup_sandbox_test.go` (call sites)
- Modify: `cmd/agents-sandbox/constants.go`, `commands_cli.go`
- Test: `cmd/agents-sandbox/cli_run_options_test.go`
- Modify: `docs/configuration/launcher.md`
- Add: `changelog.d/<PR-number>.added.md`

**Interfaces:**
- Produces: `Config.DockerReadyTimeout`, `Config.DaemonReadyTimeout time.Duration`; `RunOptions.DockerReadyTimeout`, `RunOptions.DaemonReadyTimeout time.Duration`; `resolvedReadyTimeout(override, fallback time.Duration) time.Duration` in `internal/sandbox/vm`; `startDockerdIfPresent(ctx, sb, ui, readyTimeout time.Duration) error`; `ensureDaemon(ctx, a, serveOnly, force, sb, ui, seams, readyTimeout time.Duration) error`; `restartDaemons(ctx, a, sb, serveOnly, ui, seams, readyTimeout time.Duration)`; flag/key consts `keyDockerReadyTimeout`/`keyDaemonReadyTimeout` = `"docker-ready-timeout"`/`"daemon-ready-timeout"`.

- [ ] **Step 1: Write failing unit tests**

- `internal/sandbox/vm/dockerd_test.go`: `TestResolvedReadyTimeoutUsesOverride` — `resolvedReadyTimeout(0, 30*time.Second) == 30*time.Second`; `resolvedReadyTimeout(90*time.Second, 30*time.Second) == 90*time.Second`; `resolvedReadyTimeout(-1, 30*time.Second) == 30*time.Second`.
- `internal/viperconfig/validate_test.go`: `TestValidateReadyTimeouts` — a resolver with `docker-ready-timeout=-1s` returns an error naming the key; `0`/unset and `90s` are accepted. Mirror `TestValidateAutoStopTimeout`'s resolver setup.
- `cmd/agents-sandbox/cli_run_options_test.go`: `TestRunAndShellHaveReadyTimeoutFlags` — both `run` and `shell` register `--docker-ready-timeout` and `--daemon-ready-timeout` (mirror `TestRunAndShellHaveDockerFlags`).

- [ ] **Step 2: Run to verify they fail**

Run: `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -run 'TestResolvedReadyTimeoutUsesOverride|TestValidateReadyTimeouts|TestRunAndShellHaveReadyTimeoutFlags' ./internal/sandbox/vm/ ./internal/viperconfig/ ./cmd/agents-sandbox/ -count=1`
Expected: FAIL — undefined symbols, missing keys, missing flags.

- [ ] **Step 3: Add the config keys and validation**

In `config.go`, add the two `time.Duration` fields right after `AutoStopMaxSessionRetries`, and the two key consts beside `keyAutoStopTimeout`. In `validate.go`, add `validateReadyTimeouts(v *viper.Viper) error` rejecting a negative value for either key (zero is allowed) and call it from `validate()`. Update the golden `want` list in `configkeys_test.go` to include both keys after `keyAutoStopMaxSessionRetries`.

- [ ] **Step 4: Thread the values to the VM**

Add the two `RunOptions` fields; set them in `runoptions.Build` from `cfg`. Add `resolvedReadyTimeout` to `internal/sandbox/vm` and switch `dockerd.go`'s `timeout:` and `daemon.go`'s `timeout:` to use it with the new parameter. Change `startDockerdIfPresent`, `ensureDaemon`, and `restartDaemons` signatures, pass `opts.DockerReadyTimeout`/`opts.DaemonReadyTimeout` from `setup_sandbox.go`, and add a trailing `, 0` at every existing test call site.

- [ ] **Step 5: Register the flags**

In `constants.go` add `flagDockerReadyTimeout`/`flagDaemonReadyTimeout` with values equal to the key names. In `registerSharedRunShellFlags`, register `cmd.Flags().Duration(...)` for each with a `0` default and a help string naming the built-in default (30s / 60s).

- [ ] **Step 6: Run the tests**

Run: `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -run 'TestResolvedReadyTimeoutUsesOverride|TestValidateReadyTimeouts|TestRunAndShellHaveReadyTimeoutFlags|TestConfigEnvKeysGoldenSet' ./internal/sandbox/vm/ ./internal/viperconfig/ ./cmd/agents-sandbox/ -count=1`
Expected: PASS.

- [ ] **Step 7: Document and changelog**

Add the two rows to the `docs/configuration/launcher.md` config table (key / flag / `AGENTS_SANDBOX_*` env / description, marked as VM-setup readiness ceilings). Add `changelog.d/<PR-number>.added.md` (slug = the new PR number; see `guides/github-pr-workflow.md`).

- [ ] **Step 8: Commit**

```bash
git add internal/viperconfig internal/sandbox/options internal/runoptions internal/sandbox/vm cmd/agents-sandbox docs/configuration/launcher.md changelog.d
git commit -m "feat(config): make the VM readiness timeouts configurable"
```

---

### Task 4: Let the guest raise the launcher readiness timeouts

**Files:**
- Modify: `test/harness/harness_helpers_test.go` (`isolatedEnvKeep`)
- Test: `test/harness/harness_test.go` (`TestIsolatedEnvAllowlist` neighbourhood)
- Modify: `Makefile`

**Interfaces:**
- Consumes: the `AGENTS_SANDBOX_DOCKER_READY_TIMEOUT` / `AGENTS_SANDBOX_DAEMON_READY_TIMEOUT` env vars from Task 3.
- Produces: both names present in `isolatedEnvKeep`.

- [ ] **Step 1: Write the failing test**

Add `TestIsolatedEnvKeepsLauncherReadyTimeouts`: call `isolatedEnv(root, home, []string{"AGENTS_SANDBOX_DOCKER_READY_TIMEOUT=90s", "AGENTS_SANDBOX_DAEMON_READY_TIMEOUT=180s", "HOME=/real"})` and assert the result contains both `AGENTS_SANDBOX_*_READY_TIMEOUT` entries and not `HOME=/real`.

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -run TestIsolatedEnvKeepsLauncherReadyTimeouts ./test/harness/ -count=1`
Expected: FAIL — the two entries are dropped by the allowlist.

- [ ] **Step 3: Extend the allowlist and the guest target**

Add the two variable names to `isolatedEnvKeep`. In the `Makefile`, add `MSB_GUEST_DOCKER_READY_TIMEOUT ?= 90s` and `MSB_GUEST_DAEMON_READY_TIMEOUT ?= 180s` beside the other guest defaults, and export `AGENTS_SANDBOX_DOCKER_READY_TIMEOUT=$(MSB_GUEST_DOCKER_READY_TIMEOUT)` and `AGENTS_SANDBOX_DAEMON_READY_TIMEOUT=$(MSB_GUEST_DAEMON_READY_TIMEOUT)` in the `test-msb-in-vm` recipe.

- [ ] **Step 4: Run to verify it passes**

Same command as step 2. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add test/harness/harness_helpers_test.go test/harness/harness_test.go Makefile
git commit -m "test(harness): pass the launcher readiness timeouts into the guest run"
```

---

### Task 5: Sweep leftover sandboxes at suite start

**Files:**
- Modify: `test/harness/harness_helpers_test.go` (`sandboxNames`)
- Test: `test/harness/harness_test.go`
- Modify: `test/harness/smoke_msb_test.go`
- Modify: `.operator-shared/specs/testing/harness-smoke-tests.md`

**Interfaces:**
- Produces: `sandboxNames(output string) []string`; `sweepSandboxes(t *testing.T, root, binary, when string)`.

- [ ] **Step 1: Write the failing test**

`TestSandboxNames` feeds `sandbox list -q` output and asserts trimmed, non-empty names: `"a\n\n  b  \n"` → `[]string{"a", "b"}`; `""` → `nil`.

- [ ] **Step 2: Run to verify it fails**

Run: `CGO_ENABLED=1 go test -tags microsandbox_ffi_path -run TestSandboxNames ./test/harness/ -count=1`
Expected: FAIL — undefined: `sandboxNames`.

- [ ] **Step 3: Implement `sandboxNames`**

Add it to `harness_helpers_test.go` using `strings.SplitSeq` + `strings.TrimSpace`, skipping empty entries.

- [ ] **Step 4: Run to verify it passes**

Same command as step 2. Expected: PASS.

- [ ] **Step 5: Refactor the sweep and call it at start**

In `smoke_msb_test.go`, rename `cleanupSandboxes` to `sweepSandboxes(t, root, binary, when string)` and have it use `sandboxNames` for the list output, logging `"%s: list sandboxes: %v"`/`"%s: remove sandbox %s: %v\n%s"` with `when`. In `TestHarnessSmoke`, call `sweepSandboxes(t, root, binary, "startup")` right after `preflightDocker` and before `prebuildImages`, and register the teardown via `t.Cleanup(func() { sweepSandboxes(t, root, binary, "cleanup") })` in the same position as today.

- [ ] **Step 6: Compile the tagged package and run the suite package**

Run: `CGO_ENABLED=1 go test -tags msb -run '^$' ./test/harness/` and `CGO_ENABLED=1 go test -tags microsandbox_ffi_path ./test/harness/ -count=1`
Expected: compiles; unit tests PASS. (`TestHarnessSmoke` itself still needs a KVM host.)

- [ ] **Step 7: Update the spec**

In `harness-smoke-tests.md`'s Isolation section, document that the suite sweeps the root's sandboxes at start (before the prebuild and the parallel subtests) as well as at teardown, so a reused root never carries a previous run's stopped-but-persisted VM into a run.

- [ ] **Step 8: Commit**

```bash
git add test/harness .operator-shared/specs/testing/harness-smoke-tests.md
git commit -m "test(harness): sweep a reused root's sandboxes before the suite runs"
```

---

### Task 6: Consolidate the brain

**Files:**
- Modify: `.operator-shared/specs/vm/lifecycle.md`
- Modify: `.operator-shared/guides/kvm-guest.md`
- Modify: `.operator-shared/specs/testing/harness-smoke-tests.md`

- [ ] **Step 1: Record the new contract where it belongs**

Add the two config keys and the `resolvedReadyTimeout` fallback rule to `specs/vm/lifecycle.md`. Ensure `guides/kvm-guest.md` names all three guest knobs (`MSB_GUEST_VM_CPUS`, `MSB_GUEST_PARALLEL`, `MSB_GUEST_TIMEOUT_FACTOR`, and the two readiness-timeout overrides) and their defaults. Ensure `harness-smoke-tests.md` covers the factor and the start sweep.

- [ ] **Step 2: Run the brain and repo gates**

Run: `make brain-check && make fmt && make check`
Expected: `brain-check: ok`; all `make check` targets PASS.

- [ ] **Step 3: Commit**

```bash
git add .operator-shared
git commit -m "docs(brain): record the msb smoke-remedy contracts"
```

---

## Final verification

- [ ] `make fmt && make check` green.
- [ ] `make -n test-msb-in-vm` shows `AGENTS_SANDBOX_MSB_TIMEOUT_FACTOR`, `AGENTS_SANDBOX_DOCKER_READY_TIMEOUT`, `AGENTS_SANDBOX_DAEMON_READY_TIMEOUT`, `AGENTS_SANDBOX_MSB_VM_CPUS`, `AGENTS_SANDBOX_MSB_PARALLEL`, and `-p 1`.
- [ ] `make -n test-msb` and `make -n test-msb-cold` are unchanged from `feat/793-msb-journeys` (no new env vars).
- [ ] Hand-off: the user runs `make kvm-sync && make kvm-run` on the KVM host; success is a green suite with no `started existing project VM` restart lines from a previous run.
