# CLI Functional Test Spec

## Goal

Comprehensive `RunE`-level integration tests that execute CLI commands with mocked sandbox/Docker dependencies, verify behavior through `stdio.Mock` output and mock interaction assertions, and cover all command/flag permutations.

## Scope

**In scope**: All `RunE` command handlers — their flag parsing, dependency calls, and UI output.

**Out of scope**:
- `os.Exit` paths (tested via integration tests in `main_test.go`)
- Launcher config file loading from disk (`PersistentPreRunE` — tested in existing `cli_test.go`)
- `doctor` full preflight (requires `exec.LookPath`, `/dev/kvm` checks — too flaky for unit tests)
- `config show` (filesystem-dependent, covered by integration tests)

## Architecture

### Test file layout

```
cmd/opencode-msb/
├── cli_test.go              # Existing: tree, flag structure, launcher config
├── cli_fixture_test.go      # Mock helpers, fixture runner, shared types
├── cli_list_test.go         # list, image list, volume list
├── cli_lifecycle_test.go    # stop, kill (shared fixture with flag/fixture iteration)
├── cli_run_shell_test.go    # run, shell (shared fixture parameterized by command)
├── cli_build_test.go        # build, image build
├── cli_prune_test.go        # prune
└── cli_tree_test.go         # tree, version
```

### Mock injection points

| Dependency | Override variable | Reset |
|---|---|---|
| MSB SDK client | `sandbox.NewMsbClient` | `t.Cleanup(func() { sandbox.NewMsbClient = old })` |
| Docker client (prune only) | `newDockerClient` (in `commands.go:23`) | `t.Cleanup(func() { newDockerClient = old })` |

Both are package-level function variables. Tests save the original, overwrite, and restore via `t.Cleanup`.

### Test helpers (cli_fixture_test.go)

```go
package main

func overrideMsbClient(t *testing.T, mock msbClient) { ... }
func overrideDockerClient(t *testing.T, mock dockerClient) { ... }
func mockSandboxHandle(name string, status msb.SandboxStatus) mockSandboxHandle { ... }
func mockVolumeHandle(name, path string) mockVolumeHandle { ... }
func mockImageHandle(ref, digest string) mockImageHandle { ... }
```

### Fixture iteration pattern

Flag permutations (long vs short) are driven by a slice of `[]FlagSet`:

```go
type FlagSet []string  // e.g. []string{"--force", "--dry-run"}

var stopKillFlags = []FlagSet{
    {"--force", "--dry-run"},
    {"-f", "-n"},
}

var pruneAgeFlags = []FlagSet{
    {"--age", "7d"},
    {"-a", "7d"},
    {"-a", "2w"},
    {"--age", "14d"},
}
```

Each test case is a table entry. The runner iterates `FlagSet` for each test case. This avoids duplicating scenarios for `--force` vs `-f`.

### Test runner pattern

```go
func TestCommandName(t *testing.T) {
    type testCase struct {
        name               string
        args               []string          // fixed flags for this sub-test
        mockSetup          func(*mockMsbClient)
        wantInfo           []string
        wantOut            []string
        wantWarn           []string
        wantErr            bool
        wantErrContains    string
        wantVerboseMsg     string
        verifyMockCalls    func(*mockMsbClient)
    }

    tests := []testCase{
        // ...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 1. Setup UI + command
            // 2. Setup mock
            // 3. Iterate flag permutations
            // 4. Assert output + mock interactions
        })
    }
}
```

### Mock types (reused from sandbox/prune_client_test.go)

| Type | Purpose | Key fields |
|------|---------|------------|
| `mockMsbClient` | Implements `msbClient` | `sandboxes`, `volumes`, `images` return lists; `listSandboxesErr`, `createSandboxErr` inject failures; `createdSandboxes`, `removedSandboxes` track calls |
| `mockSandboxHandle` | Implements `msbSandboxHandle` | `name`, `status`, `stopErr`, `killErr`, `removeErr` |
| `mockSandbox` | Implements `msbSandbox` | `attachErr`, `name`, `fsValue` |
| `mockVolumeHandle` | Implements `msbVolumeHandle` | `name`, `path` |
| `mockImageHandle` | Implements `msbImageHandle` | `ref`, `digest` |
| `mockDockerClient` | Implements `dockerClient` | `removedImages`, `removeErr`, `perCallErrs` |
| `stdio.Mock` | Implements `stdio.UI` | Captures all `InfoCalls`, `OutCalls`, `WarnCalls`, `ErrorCalls`, `SpinnerCalls`, `VerboseCalls`, `IsInteractiveResult` |

## Implementation changes required

The implementation needs small additions so test cases can differentiate cleanly (same Info output, different behavior).

### 1. Run/Shell: verbose after dry-run auto-enable

**File**: `cmd/opencode-msb/commands.go`

In `runFunc` (around line 345), after `if opts.DryRun { opts.DryRunVM = true }`:

```go
if opts.DryRun {
    opts.DryRunVM = true
    ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")
}
```

In `buildShellCmd` (around line 143), same pattern:

```go
if opts.DryRun {
    opts.DryRunVM = true
    ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")
}
```

**Why**: Scenarios where both `--dry-run` and `--dry-run-vm` are set produce the same Info message. Without this verbose marker, tests cannot verify that `--dry-run` triggered the auto-enable behavior.

### 2. Stop/Kill: verbose when state removed

**File**: `internal/sandbox/projectvm.go`

In `stopOrKillProjectVM`, after the `handle.Remove(ctx)` call at line 347 (before the error check):

```go
if remove {
    if err := handle.Remove(ctx); err != nil {
        ui.Warnf("failed to remove sandbox state: %v", err)
    } else {
        ui.Verbosef("persisted state removed: %s", name)
    }
}
```

**Why**: Test scenario 22 and 26 produce the same Info message as 21 and 25 respectively (both say "Stopped/Killed project VM: ..."). The verbose message after `Remove` provides a clean assertion that state was actually removed.

### 3. Prune: invalid age should error

**File**: `cmd/opencode-msb/commands.go`

In `buildPruneCmd` RunE (around line 365-374), replace the silent fallback with an error path:

```go
ageStr, _ := cmd.Flags().GetString("age")
var age time.Duration
if ageStr != "" {
    var ok bool
    age, ok = launcherconfig.ParseHumanDuration(ageStr)
    if !ok {
        return fmt.Errorf("invalid age %q: use a Go duration or suffix d/w (e.g. 7d, 2w)", ageStr)
    }
}
if age == 0 {
    age = 7 * 24 * time.Hour
}
```

**Why**: Scenario 55 requires that invalid `--age` values produce a hard error, not a silent fallback to 7 days.

---

## Test Scenarios

### A. List commands (`cli_list_test.go`)

**Dependencies**: `sandbox.NewMsbClient` → `ListSandboxes()` / `ImageList()` / `ListVolumes()`

Drop scenarios that test flags with no behavioral effect (verbose/quiet on list commands).

| # | Scenario | Mock Setup | Assertions |
|---|----------|------------|------------|
| L1 | Empty sandbox list | `ListSandboxes: []` (empty) | `InfoCalls` contains `"No sandboxes found."` |
| L2 | One sandbox running | `{name: "opencode-msb-vm-abc", status: "running"}` | `OutCalls` contains `"opencode-msb-vm-abc          running"` |
| L3 | Multiple sandboxes | 3 handles: running, stopped, draining | 3 `OutCalls` lines |
| L4 | Non-project VMs filtered | Handles with `myvm-` prefix + `opencode-msb-vm-abc` | Only `opencode-msb-vm-*` in output |
| L5 | List error | `listSandboxesErr` set | Non-nil error returned |
| L6 | Empty image list | `ImageList: []` | `InfoCalls` contains `"No images found."` |
| L7 | One image | `{ref: "opencode-msb/runner:latest", digest: "sha256-abc123"}` | `OutCalls` with reference + digest |
| L8 | Multiple images | 3 handles | 3 `OutCalls` |
| L9 | Non-opencode images filtered | References: `opencode-msb/runner:latest` + `docker.io/some/img` | Only `opencode-msb/runner-*` in output |
| L10 | Image list error | `listImagesErr` set | Non-nil error returned |
| L11 | Empty volume list | `ListVolumes: []` | `InfoCalls` contains `"No volumes found."` |
| L12 | One volume | `{name: "opencode-msb-home-proj-abc", path: "/mnt/vol"}` | `OutCalls` with name + path |
| L13 | Multiple volumes | 2 handles | 2 `OutCalls` |
| L14 | Non-home volumes filtered | Handles with `opencode-msb-clone-` + `opencode-msb-home-*` | Only `opencode-msb-home-*` in output |
| L15 | Volume list error | `listVolumesErr` set | Non-nil error returned |

### B. Stop/Kill lifecycle (`cli_lifecycle_test.go`)

**Dependencies**: `sandbox.NewMsbClient` → `GetSandbox()` → `Stop()` / `Kill()`

Shared fixture iterates over `stopKillFlags` (`{"--force","--dry-run"}` and `{"-f","-n"}`).

| # | Scenario | Group | Mock Setup | Assertions |
|---|----------|-------|------------|------------|
| S1 | No VM found | stop/kill | `GetSandbox: nil, ErrSandboxNotFound` | `InfoCalls` contain `"no project VM found: opencode-msb-vm-xxx"` |
| S2 | Dry run, stop/kill | stop/kill | `GetSandbox: handle; handle.stopErr/killErr = nil` | Info: `"dry-run: Would stop/kill project VM: opencode-msb-vm-xxx"`; `stopFn/killFn` **not** called |
| S3 | Dry run + force | stop/kill | Same as S2 | Info: `"dry-run: Would stop/kill project VM: opencode-msb-vm-xxx (also would remove persisted state)"`; neither `stopFn/killFn` nor `Remove` called |
| S4 | Normal stop/kill | stop | `handle.stopErr = nil` | `SpinnerCalls` contains `"Stopping project VM"`; Info: `"Stopped project VM: opencode-msb-vm-xxx"` |
| S5 | Normal kill | kill | `handle.killErr = nil` | `SpinnerCalls` contains `"Force-killing project VM"`; Info: `"Killed project VM: opencode-msb-vm-xxx"` |
| S6 | Stop + force, state removed | stop | Same as S4 + `handle.removeErr = nil` | Same as S4 + `VerboseCalls` contains `"persisted state removed: opencode-msb-vm-xxx"` + `handle.Remove` called |
| S7 | Kill + force, state removed | kill | Same as S5 + `handle.removeErr = nil` | Same as S5 + `VerboseCalls` contains `"persisted state removed: opencode-msb-vm-xxx"` + `handle.Remove` called |
| S8 | Stop/kill + force, removal fails | stop/kill | `removeErr` set | Same stop/kill output + `WarnCalls` contains `"failed to remove sandbox state: ..."` |
| S9 | Stop fails | stop | `handle.stopErr = fmt.Errorf("...")` | `ErrorCalls` contains `"stop sandbox ..."`; `Spinner.StopError(err)` called |
| S10 | Kill fails | kill | `handle.killErr = fmt.Errorf("...")` | `ErrorCalls` contains `"kill sandbox ..."`; `Spinner.StopError(err)` called |

### C. Run + Shell (`cli_run_shell_test.go`)

**Dependencies**: `sandbox.NewMsbClient` → `CheckAll()`, `EnsureImage()`, `EnsureProjectVM()`, sandbox `Attach()`.

Requires a full mock MSB client: `SetCreatedSandbox(sb)` on `CreateSandbox`, `SetGetSandbox` on `GetSandbox`.

**No `--dry-run-vm` crutch needed**: The mock chain is already set up for these tests. `prepareSandbox` is called through the real path, which calls `setUpSandbox` → `GetSandbox` → returns the mock. The resulting session has a non-nil `sb`. Tests exercise either the dry-run path or mock the final `Attach()` call directly.

```
Mock chain for non-dry-run:
  sandbox.NewMsbClient → mockMsbClient
    → ListSandboxes: ok
    → EnsureImage (→ ImageList, ImageGet): ok (mock returns existing image)
    → ListVolumes: ok (empty)
    → GetSandbox: return { name: "opencode-msb-vm-x", status: stopped/crashed }
    → CreateSandbox: return { mockSandbox{attachErr: ..., name: "opencode-msb-vm-x"} }
    → session.sandbox = created mock → non-nil
```

| # | Scenario | Args | Assertions | Mock checks |
|---|----------|------|------------|-------------|
| R1 | Dry-run run | `--dry-run` | Info: `"dry-run: Would run opencode"` | `CreateSandbox` not called |
| R2 | Dry-run shell | `--dry-run` | Info: `"dry-run: Would start interactive shell session"` | `CreateSandbox` not called |
| R3 | Dry-run-vm + dry-run run | `--dry-run --dry-run-vm` | Info: `"dry-run: Would run opencode"` | `VerboseCalls` has `"auto-enabled"` (R3a vs R1) |
| R4 | Dry-run-vm + dry-run shell | `--dry-run --dry-run-vm` | Info: Dry-run message | `VerboseCalls` has `"auto-enabled"` |
| R5 | Run error | `--no-auto` | Error: `"opencode session failed: ..."` | `CreateSandbox` called with `Auto=false`; `Attach` called, returns error |
| R6 | Shell error | `--no-auto` | Error: `"opencode session failed: ..."` | `Attach` called with `["/bin/bash", "-l"]`; `CreateSandbox` opts `Auto=false` |
| R7 | Run + no-auto | `--dry-run --no-auto` | Info: `"dry-run: Would run opencode"` | No mock calls (dry-run path) |
| R8 | Run with branch | `--dry-run` passes; verify via `--branch` mock path | N/A (verify via `CreateSandbox` opts below) | Covered by R9 |
| R9 | Run: branch + cpus + memory + user | Full mock, Attach err set | Error from Attach | `CreateSandbox` opts `Branch="feature-x"`, `CPUs=2`, `Memory="8G"`, `User="alice"` |
| R10 | Run: all flag combos | Same | Same error | All 4 flags verified in single `CreateSandbox` call |
| R11 | Run auto vs no-auto: mock Attach succeeded | `none` vs `--no-auto` | Run returns `ExitError{Code: 0}`; no-auto also returns `ExitError{Code: 0}` but with different attach command | Verify Attach arg string: with auto contains `--auto`, without auto does not |
| R12 | Shell with flags | `--branch foo --cpus 2` | Shell error (mock Attach) | `CreateSandbox` opts `Branch="foo"`, `CPUs=2`, `Auto=false` |

R5–R11 iterate with the same full mock, only changing `--dry-run` vs no-`--dry-run`, `--no-auto` vs none, and flag values.

**Why no dry-run-vm-only scenarios (R3/R4 from original)**: `DryRunVM && sb == nil` only happens if `setUpSandbox` succeeds but somehow returns `sb == nil` — which is a very narrow edge case within `setUpSandbox` itself (e.g., `EnsureProjectVM` returns nil session on an unhandled error path). Testing requires either mocking the internal function or depending on an actual implementation bug. The `--dry-run` flag alone (R1/R2) already verifies the dry-run path. The `--dry-run-vm` flag's interaction with `--dry-run` (auto-enable) is verified via verbose output in R3/R4.

### D. Build (`cli_build_test.go`)

**Dependencies**: `sandbox.NewMsbClient` → `EnsureImage()` (which uses MSB SDK)

| # | Scenario | Assert |
|---|----------|--------|
| B1 | Dry-run build | Info: `"dry-run: Would build runner image"` |
| B2 | Dry-run + --rebuild | Same message (force ignored in dry-run) |
| B3 | Build error | `SpinnerCalls` contains `"Building runner image"`; non-nil error returned |

`image build` reuses `buildBuildCmd(ui)` so B1–B3 apply identically (no separate tests).

### E. Prune (`cli_prune_test.go`)

**Dependencies**: `sandbox.NewMsbClient` (`ListSandboxes`, `ListVolumes`, `ImageList`, `RemoveSandbox`) + `newDockerClient`.

Force flag skipped — no behavioral effect. Fixture iterates over `pruneAgeFlags`.

| # | Scenario | Mock Setup | Assertions |
|---|----------|------------|------------|
| P1 | No stale items | All lists empty; docker remove succeeds | Info: `"Pruned 0 VMs, 0 home volumes, 0 docker images, 0 msb images, 0 task sandboxes, 0 clone volumes"` |
| P2 | Dry-run with stale items | Mock returns stale VMs + volumes + images | Info starts with `"dry-run: Would prune..."` plus non-zero counts |
| P3 | Normal prune, partial failure | Some `RemoveSandbox` calls return error | `WarnCalls` contains `"failed to remove stale VM ..."`; counts reflect successes only |
| P4 | Custom age `"2w"` | Stale items older than 14d | Summary with correct counts |
| P5 | Custom age `"14d"` | Same mock as P4 | Same summary |
| P6 **FIXME** | Invalid age errors | `{"--age", "notaduration"}` | Error contains age parse error message (skip until impl fix) |
| P7 | Docker client error | `newDockerClient` returns error | Error: `"cannot connect to Docker daemon"` |
| P8 | Clone volumes pruned | Mock returns stale clone volumes | Summary count includes clone volumes |
| P9 | Task sandboxes pruned | Mock returns task sandboxes | Summary includes task sandbox count |
| P10 | Valid age via `-a` | `pruneAgeFlags[0]` with stale items | Summary with counts |

### F. Tree + Version (`cli_tree_test.go`)

| # | Scenario | Assertions |
|---|----------|------------|
| T1 | Tree contains root name | `InfoCalls[0] == "opencode-msb"` |
| T2 | Tree lists all commands | InfoCalls contain: run, doctor, build, list, shell, config, image, volume, stop, kill, prune |
| T3 | Tree shows flag descriptions | InfoCalls contain: `"Assume yes to all prompts"`, `"Memory limit"` |
| T4 | Default version | `OutCalls[0]` contains `"opencode-msb dev"` |
| T5 | Custom version | `version = "1.2.3"` → `OutCalls[0]` contains `"opencode-msb 1.2.3"` |

---

## Coverage Matrix

| File | Scenarios | Count |
|------|-----------|-------|
| `cli_list_test.go` | L1–L15 | 15 |
| `cli_lifecycle_test.go` | S1–S10 | 10 |
| `cli_run_shell_test.go` | R1–R12 | 12 |
| `cli_build_test.go` | B1–B3 | 3 |
| `cli_prune_test.go` | P1–P10 | 10 |
| `cli_tree_test.go` | T1–T5 | 5 |
| **Total** | | **55** |

## Mock Interaction Assertions

Output assertions (InfoCalls, OutCalls, etc.) cover the visible behavior. Mock interaction assertions verify internal behavior:

| Scenario | Mock Check |
|----------|------------|
| R1, R2 | `CreateSandbox` not called (dry-run path) |
| R10–R13, R15 | `CreateSandbox` opts contain correct `Branch`, `CPUs`, `Memory`, `User` |
| S2 | `stopFn/killFn` not called (dry-run prevents VM manipulation) |
| S3 | Neither `stopFn/killFn` nor `removeFn` called |
| S6, S7 | `handle.Remove` called on mock |
| S8 | `handle.Remove` called but errored |
| S9, S10 | `stopFn/killFn` called and errored |
| P8 | Clone volume `Remove` called for each stale clone |
| P9 | `RemoveSandbox` called for task sandboxes |

## Notes

- **No `--dry-run-vm` crutch**: Run/shell scenarios use either the `--dry-run` flag (scenarios R1–R4, R7) which exits before `prepareSandbox`, or the full mock chain (R5–R6, R9–R12) where `Attach()` is mocked. No `--dry-run-vm` flag is needed in tests — the mock sandbox from `prepareSandbox` is always non-nil, so the real Attach path is exercised.
- **Doctor commands skipped**: `doctor` calls `exec.LookPath`, checks `/dev/kvm`, and reads `git` PATH — these are inherently flaky and better suited for integration tests.
- **Version var `= "dev"`**: The `version` package variable is set at build time. Tests that need a custom value set it directly: `version = "1.2.3"` before calling `Execute` or `Run`.