# CLI Functional Tests — Implementation Plan

## Pre-conditions

- Spec: `docs/superpowers/specs/2026-08-03-cli-functional-tests-design.md` — reviewed and approved
- All existing tests pass: `go test ./...`
- Linter passes: `golangci-lint run`

## Test Architecture (Option B)

**CLI test layer asserts UI output only.** Sandbox mock construction, interface implementation, and method-level assertions live in the sandbox package (sandbox package tests). The CLI tests use `cmd.RunE(cmd, args)` and verify:
- `InfoCalls`, `OutCalls`, `WarnCalls`, `ErrorCalls`, `SpinnerCalls`, `VerboseCalls` on mockUI

This means CLI tests don't need to replicate mock MSB client types — they rely on the sandbox layer to execute correctly and assert via the UI contract.

---

## Task 1: Implementation changes

Adds verbose markers and error path needed by test scenarios.

**Scope**: 4 changes across 2 files

| # | File | Change | Scenarios |
|---|------|--------|-----------|
| 1.1 | `cmd/opencode-msb/commands.go` — `runFunc` (line ~345) | After `opts.DryRunVM = true`, add `ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")` | R3, R4 |
| 1.2 | `cmd/opencode-msb/commands.go` — `buildShellCmd` (line ~143) | Same pattern as 1.1 | R4 |
| 1.3 | `internal/sandbox/projectvm.go` — `stopOrKillProjectVM` (line ~350) | After `handle.Remove(ctx)`, add `else` branch with `ui.Verbosef("persisted state removed: %s", name)` | S6, S7 |
| 1.4 | `cmd/opencode-msb/commands.go` — `buildPruneCmd` (line ~374) | Replace silent fallback with `fmt.Errorf("invalid age %q: ...")` when `ParseHumanDuration` returns `ok=false` | P6 |

**Deliverable**:
- All 4 changes applied
- `go build ./...` succeeds
- `go test ./...` passes (no regressions)
- `golangci-lint run` passes

**Dependencies**: None

---

## Task 2a: Fixture helpers

Build the reusable test infrastructure. **No test cases** — just helper functions and types.

**File**: `cmd/opencode-msb/cli_fixture_test.go`

```go
package main

// overrideMsbClient saves/restores sandbox.NewMsbClient.
func overrideMsbClient(t *testing.T, mock sandbox.MsbClient)

// overrideDockerClient saves/restores newDockerClient factory.
func overrideDockerClient(t *testing.T, mock sandbox.DockerClient)

// FlagSet is a permutation of CLI flag arguments.
type FlagSet []string
```

**FlagSet fixtures**:
```go
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

**Deliverable**:
- `go test ./cmd/opencode-msb/ -run TestNone -v` compiles and passes
- No test assertions, just compile-check

**Dependencies**: Task 1

---

## Task 2b: List tests

Tests `ListSandboxes`, `ListImages`, `ListVolumes` CLI output.

**File**: `cmd/opencode-msb/cli_list_test.go`

**Scope**: 15 scenarios (L1–L15)

| Group | Scenarios | Command | Output assertions |
|-------|-----------|---------|-------------------|
| Sandboxes | L1–L5 | `list` | Empty → "No sandboxes found."; populated → formatted name+status; error → `ErrorCalls` |
| Images | L6–L10 | `image list` | Empty → "No images found."; populated → reference+digest |
| Volumes | L11–L15 | `volume list` | Empty → "No volumes found."; populated → name+path |

**Key behavior tested**:
- Filtering: `opencode-msb-vm-*` only for sandboxes, `opencode-msb/runner-*` for images, `opencode-msb-home-*` for volumes
- Error propagation: CLI forwards sandbox error to `ErrorCalls` and non-nil return

**Deliverable**:
- 15 passing tests
- `go test ./cmd/opencode-msb/ -run TestList -v` passes

**Dependencies**: Task 2a

---

## Task 3: Lifecycle tests (stop/kill)

Tests CLI output for `stopOrKillProjectVM` via both `stop` and `kill` commands.

**File**: `cmd/opencode-msb/cli_lifecycle_test.go`

**Scope**: 10 scenarios (S1–S10) with `stopKillFlags` iteration

Uses `stopKillFlags` fixture (`{"--force","--dry-run"}` and `{"-f","-n"}`). Each permutation runs inside a nested `t.Run` to isolate flag effects.

| Group | Scenarios | Command | Output assertions |
|-------|-----------|---------|-------------------|
| Not found | S1 | stop, kill | `InfoCalls` → "no project VM found: opencode-msb-vm-xxx" |
| Dry run | S2 | stop, kill | `InfoCalls` → "dry-run: Would stop/kill project VM: ..." |
| Dry run + force | S3 | stop, kill | `InfoCalls` → "... (also would remove persisted state)" |
| Normal stop | S4 | stop | `SpinnerCalls` → "Stopping project VM"; `InfoCalls` → "Stopped project VM: ..." |
| Normal kill | S5 | kill | `SpinnerCalls` → "Force-killing project VM"; `InfoCalls` → "Killed project VM: ..." |
| Force+remove | S6 | stop | S4 + `VerboseCalls` → "persisted state removed: ..." (from Task 1.3) |
| Force+remove | S7 | kill | S5 + same verbose message |
| Remove fails | S8 | stop, kill | S4/S5 + `WarnCalls` → "failed to remove sandbox state: ..." |
| Command fails | S9 | stop | `ErrorCalls` → "stop sandbox ..." |
| Command fails | S10 | kill | `ErrorCalls` → "kill sandbox ..." |

**Deliverable**:
- 10 passing tests
- `go test ./cmd/opencode-msb/ -run TestLifecycle -v` passes

**Dependencies**: Task 1, Task 2a

---

## Task 4a: Build tests

Tests dry-run path for `buildBuildCmd`.

**File**: `cmd/opencode-msb/cli_build_test.go`

**Scope**: 3 scenarios (B1–B3)

| # | Args | Assertions |
|---|------|------------|
| B1 | `--dry-run` | `InfoCalls` → "dry-run: Would build runner image" |
| B2 | `--dry-run --rebuild` | Same (force ignored in dry-run path) |
| B3 | (no flags, mock returns build error) | `SpinnerCalls` → "Building runner image"; non-nil error |

`image build` reuses `buildBuildCmd(ui)` so B1–B3 apply identically — covered by commenting the shared path.

**Deliverable**:
- 3 passing tests
- `go test ./cmd/opencode-msb/ -run TestBuild -v` passes

**Dependencies**: Task 1, Task 2a

---

## Task 4b: Prune tests

Tests `buildPruneCmd` output with mock MSB client + mock Docker client.

**File**: `cmd/opencode-msb/cli_prune_test.go`

**Scope**: 10 scenarios (P1–P10) with `pruneAgeFlags` iteration

**Key mock setup pattern**: Full `mockMsbClient` with `sandboxes`, `volumes`, `images` lists containing items with varying `updatedAt` times. `mockDockerClient` returns nil for `ImageRemove` (success) or returns an error.

| # | Mock setup | Assertions |
|---|-----------|------------|
| P1 | Empty lists, docker remove ok | `InfoCalls` → "Pruned 0 VMs, 0 home volumes, ..." |
| P2 | `mockDockerClient` ok + stale items | `InfoCalls` → "Pruned X VMs, Y home volumes, ..." |
| P3 | `mockDockerClient` ok + `removeSandboxErr` set on some | `WarnCalls` → "failed to remove stale VM ..."; counts reflect successes |
| P4–P5 | `pruneAgeFlags[2]` (`-a 2w`) / `[3]` (`--age 14d`) | Summary counts with longer threshold |
| P6 | `{"--age", "invalid"}` | `ErrorCalls` → "invalid age ..." (tests Task 1.4) |
| P7 | `overrideDockerClient` returns error | `ErrorCalls` → "cannot connect to Docker daemon" |
| P8 | Mock with stale clone volumes | Count includes clone volumes |
| P9 | Mock with task sandboxes | Count includes task sandboxes |
| P10 | `pruneAgeFlags[0]` (`-a 7d`) with stale items | Summary counts |

**Deliverable**:
- 10 passing tests
- `go test ./cmd/opencode-msb/ -run TestPrune -v` passes

**Dependencies**: Task 1, Task 2a

---

## Task 5: Run + Shell tests

Most complex task. Requires full mock MSB client chain supporting `prepareSandbox()` → `setUpSandbox()` → `GetSandbox()`/`CreateSandbox()` → `Attach()`.

**File**: `cmd/opencode-msb/cli_run_shell_test.go`

**Scope**: 12 scenarios (R1–R12)

### Mock chain requirements

The mock must support all methods called by `prepareSandbox`:

```
run/shell → prepareSandbox →
  CheckAll      → CheckMsb (skip with --dry-run flag)
  EnsureImage   → ImageList, ImageGet → return mock image handle
  ListVolumes   → return empty list
  GetSandbox    → return handle (triggers Connect/Start) or error
  CreateSandbox → return {name, fsValue, attachResult}
```

For dry-run scenarios (R1, R2, R3, R4, R7): `--dry-run` flag short-circuits before `prepareSandbox`, so no mock chain needed.

For non-dry-run scenarios (R5–R12): mock must return a sandbox handle or create a sandbox with controlled `Attach()` behavior.

### Scenario mapping

| Test | Command | Args | Assertion | Mock check |
|------|---------|------|-----------|------------|
| R1 | run | `--dry-run` | `InfoCalls` → "dry-run: Would run opencode" | `CreateSandbox` not called |
| R2 | shell | `--dry-run` | `InfoCalls` → "dry-run: Would start interactive shell session" | `CreateSandbox` not called |
| R3 | run | `--dry-run --dry-run-vm` | `InfoCalls` + `VerboseCalls` → "auto-enabled" | `CreateSandbox` not called |
| R4 | shell | `--dry-run --dry-run-vm` | Same verbose marker | `CreateSandbox` not called |
| R5 | run | (default) | `ErrorCalls` → "opencode session failed" | `CreateSandbox` opts `Auto=true`; `Attach` called |
| R6 | shell | (default) | Same error | `CreateSandbox` opts `Auto=false`; `Attach` called with `["/bin/bash", "-l"]` |
| R7 | run | `--dry-run --no-auto` | `InfoCalls` → "dry-run: Would run opencode" | No mock calls, no error |
| R9 | run | `--branch x --cpus 2 --memory 8G --user alice` | Error from Attach | `CreateSandbox` opts contain `Branch`, `CPUs`, `Memory`, `User` |
| R10 | run | `--branch main -c 4 -m 16G -u root` | Same error | All 4 flags in single `CreateSandbox` call (fixture) |
| R11 | run | (no --no-auto) | `ExitError{Code: 0}` (mock attach returns ok) | Attach arg string contains "--auto" |
| R12 | shell | `--branch foo --cpus 2` | Error from Attach | `CreateSandbox` opts `Branch="foo", CPUs=2, Auto=false` |

**Key mock tracking mechanism**: The mock must record every `CreateSandbox` call's arguments (the `...msb.SandboxOption` slice). CLI tests assert on tracked call arguments rather than re-implementing sandbox methods.

**Deliverable**:
- 12 passing tests
- `go test ./cmd/opencode-msb/ -run TestRunShell -v` passes

**Dependencies**: Task 1, Task 2a, Task 4a (mock patterns from build help)

---

## Task 6: Tree + Version tests

Static output tests. No mock injection needed.

**File**: `cmd/opencode-msb/cli_tree_test.go`

**Scope**: 5 scenarios (T1–T5)

| Test | Fixture | Assertions |
|------|---------|------------|
| T1 | `mockUI := stdio.Mock{}` → `buildRootCmd(&mockUI)` → `rootCmd.RunE(nil, nil)` | `InfoCalls[0] == "opencode-msb"` |
| T2 | Same tree output | Tree contains: run, doctor, build, list, shell, config, image, volume, stop, kill, prune |
| T3 | Same | Flag descriptions: "Assume yes to all prompts", "Memory limit" |
| T4 | Default `version = "dev"` | `OutCalls[0]` → "opencode-msb dev" |
| T5 | Pre-set: `version = "1.2.3"` | `OutCalls[0]` → "opencode-msb 1.2.3" |

**Deliverable**:
- 5 passing tests
- `go test ./cmd/opencode-msb/ -run TestTree -v` passes

**Dependencies**: Task 2a (mockUI setup)

---

## Task 7: Verification

Final integration verification after all tasks complete.

- `go test ./cmd/opencode-msb/ -v` runs all 55+ scenarios
- `go test ./...` runs full test suite (no regressions)
- `golangci-lint run` passes
- `go vet ./...` passes
- All test names contain scenario IDs for traceability

**Deliverable**: Clean CI in local environment

---

## Sequential Dispatch Order

```
  Task 1
    │
    ▼
  Task 2a (helpers only, no tests)
    │
    ├──► Task 2b (list tests) ──► Task 5 ──► Task 7
    │     (15 scenarios)
    │
    ├──► Task 3 (lifecycle) ──────► Task 5
    │     (10 scenarios)
    │
    └──► Task 4a (build) ──────► Task 5
          (3 scenarios)
              │
          Task 4b (prune) ──────► Task 5
          (10 scenarios)
              │
          Task 6 (tree/version) ──► Task 5
          (5 scenarios)
              │
          Task 5 (run/shell) ────► Task 7
          (12 scenarios)
              │
          Task 7 (verification)
```

Total: 7 sequential passes. Each subagent handles one task and leaves the codebase ready for the next.

---

## Success Criteria

1. All 55 scenarios from spec have corresponding passing tests (scenario IDs in comments and test names)
2. `go test ./cmd/opencode-msb/ -v` runs in under 10 seconds
3. No test depends on Docker, KVM, msb, or network access
4. `golangci-lint run` and `go vet ./...` clean
5. New test files follow existing Go test conventions (table-driven, `t.Run`, `t.Cleanup`)
6. No unused imports or dead code in test files