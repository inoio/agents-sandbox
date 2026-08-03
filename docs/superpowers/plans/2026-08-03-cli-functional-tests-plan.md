# CLI Functional Tests — Implementation Plan

## Pre-conditions

- Spec: `docs/superpowers/specs/2026-08-03-cli-functional-tests-design.md` — reviewed and approved
- All existing tests pass: `go test ./...`
- Linter passes: `golangci-lint run`

---

## Task 1: Implementation changes (prerequisite)

Adds verbose markers and error path needed by test scenarios.

**Scope**: 3 small changes across 2 files

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

## Task 2: Fixtures + List tests

Build the reusable test infrastructure and the straightforward list commands.

**Files**: `cmd/opencode-msb/cli_fixture_test.go`, `cmd/opencode-msb/cli_list_test.go`

**Scope**: Fixture infrastructure + 15 list scenarios (L1–L15)

### cli_fixture_test.go

- `overrideMsbClient(t, msbClient)` — saves/restores `sandbox.NewMsbClient`
- `overrideDockerClient(t, dockerClient)` — saves/restores `newDockerClient` from commands.go
- `FlagSet` type and fixtures: `stopKillFlags`, `pruneAgeFlags`
- `mockSandboxHandle`, `mockVolumeHandle`, `mockImageHandle` helper constructors (convenience wrappers around `prune_client_test.go` mock types)
- Table-driven `testCase` struct definition for test runners

### cli_list_test.go — Scenarios L1–L15

Uses `mockMsbClient` from `sandbox/prune_client_test.go` via `overrideMsbClient`.

| Group | Scenarios | Mock method | Command |
|-------|-----------|-------------|---------|
| Sandboxes | L1–L5 | `ListSandboxes()` | `list` |
| Images | L6–L10 | `ImageList()` | `image list` |
| Volumes | L11–L15 | `ListVolumes()` | `volume list` |

**Deliverable**:
- 15 passing tests (one per scenario number)
- `go test ./cmd/opencode-msb/ -run TestList -v` passes
- All test names map directly to scenario IDs in spec

**Dependencies**: Task 1 (implementation changes), sandbox mocks (existing in `prune_client_test.go`)

---

## Task 3: Lifecycle tests (stop/kill)

Tests `stopOrKillProjectVM` via the CLI layer, using flag iteration fixtures.

**File**: `cmd/opencode-msb/cli_lifecycle_test.go`

**Scope**: 10 scenarios (S1–S10) with `stopKillFlags` iteration

| Group | Scenarios | Mock method | Command group |
|-------|-----------|-------------|---------------|
| Not found | S1 | `GetSandbox: ErrSandboxNotFound` | stop, kill |
| Dry run | S2, S3 | `GetSandbox: handle; stopErr=killErr=nil` | stop, kill |
| Normal | S4, S5 | handle `stopErr/killErr = nil` | stop, kill |
| Force+remove | S6, S7 | add `removeErr=nil` | stop, kill |
| Remove fails | S8 | `removeErr=fmt.Errorf(...)` | stop, kill |
| Command fails | S9, S10 | `stopErr/killErr = fmt.Errorf(...)` | stop, kill |

Shared fixture iterates `stopKillFlags` (`{"--force","--dry-run"}` and `{"-f","-n"}`).

Each stopKillFlags permutation runs independently inside a `t.Run`. Assertions use `assert.Contains` on UI output and mock method call tracking.

**Mock assertions by scenario**:
- S1: `InfoCalls` contains "no project VM found"
- S2: `InfoCalls` contains "dry-run: Would stop/kill" + Stop/Kill not called (verify via mock call count)
- S3: `InfoCalls` contains "(also would remove persisted state)" + Stop/Kill/Remove not called
- S4/S5: `SpinnerCalls` contains "Stopping/Force-killing" + `InfoCalls` contains "Stopped/Killed"
- S6/S7: Same as S4/S5 + `VerboseCalls` contains "persisted state removed:" + `handle.Remove` was called
- S8: Same as S4/S5 + `WarnCalls` contains "failed to remove sandbox state"
- S9/S10: `ErrorCalls` contains "stop/kill sandbox"

**Deliverable**:
- 10 passing tests
- `go test ./cmd/opencode-msb/ -run TestStopKill -v` passes

**Dependencies**: Task 1 (state remove verbose marker), Task 2 (fixtures + override helpers)

---

## Task 4: Build + Prune tests (dry-run focused)

Tests `BuildImage` dry-run path and `Prune` pipeline with mock MSB client + mock Docker client.

**Files**: `cmd/opencode-msb/cli_build_test.go`, `cmd/opencode-msb/cli_prune_test.go`

**Scope**: 13 scenarios (B1–B3, P1–P10)

### cli_build_test.go (B1–B3)
- Dry-run build → `InfoCalls` == "dry-run: Would build runner image"
- Dry-run + rebuild → same message (force ignored in dry-run)
- Build error → `SpinnerCalls` contains "Building runner image" + non-nil error

`image build` reuses `buildBuildCmd(ui)` so B1–B3 apply identically — verified in comments, no separate assertions needed.

Mock setup: `overrideMsbClient` with mock that returns image list or errors from `ImageGet`/`ImageList`. The `EnsureImage` function uses `newMsbClient()` internally.

### cli_prune_test.go (P1–P10)
Uses `pruneAgeFlags` iteration + `overrideDockerClient`/`overrideMsbClient`.

Key mock setup pattern: full `mockMsbClient` with `sandboxes`, `volumes`, `images` lists containing items of varying ages (mock `updatedAt` times). `mockDockerClient` returns nil for `ImageRemove` (success) or returns an error.

| Test | Mock setup | Assertions |
|------|-----------|------------|
| P1 | Empty lists, docker remove ok | Info: "Pruned 0 ..." |
| P2 | `mockDockerClient` returns ok + stale items in mock lists | Info: dry-run summary with counts |
| P3 | Full mock, `removeSandboxErr` set on some | Counts reflect successes + Warn for failures |
| P4–P5 | `pruneAgeFlags[2]` / `[3]` with stale items | Summary counts |
| P6 | `{"--age", "invalid"}` | Error contains "invalid age" (tests the impl fix from Task 1.4) |
| P7 | `overrideDockerClient` returns error | Error: "cannot connect to Docker daemon" |
| P8 | Mock with stale clone volumes | Count includes clone volumes |
| P9 | Mock with task sandboxes | Count includes task sandboxes |
| P10 | `pruneAgeFlags[0]` with stale items | Summary counts |

**Deliverable**:
- 13 passing tests
- `go test ./cmd/opencode-msb/ -run "TestBuild|TestPrune" -v` passes

**Dependencies**: Task 1 (invalid age error path), Task 2 (fixtures + override helpers)

---

## Task 5: Run + Shell tests (full mock chain)

Most complex task. Requires building a full mock MSB client chain that supports `prepareSandbox()` → `setUpSandbox()` → `GetSandbox()` / `CreateSandbox()` → `Attach()`.

**File**: `cmd/opencode-msb/cli_run_shell_test.go`

**Scope**: 12 scenarios (R1–R12)

### Full mock chain setup

The mock must support all methods called by `prepareSandbox`:

```
run/shell → prepareSandbox →
  CheckAll      → CheckMsb (use ensureInstalled mock via sandbox override)
                    └─ or skip: use --dry-run flag which short-circuits
  EnsureImage   → ImageList, ImageGet → return mock image handle
  ListVolumes   → return [] (empty)
  GetSandbox    → return handle (for dry-run-vm: return nil/sandboxes with errors)
  CreateSandbox → return {name: "opencode-msb-vm-test", fsValue: mockFs, shellResult: mock}
```

For dry-run scenarios (R1, R2, R3, R4, R7): `--dry-run` flag is used, which exits before `prepareSandbox`, so no mock chain needed.

For non-dry-run scenarios (R5–R12): full mock chain constructed. The mock `CreateSandbox` returns a `mockSandbox{attachErr: fmt.Errorf("mock"), name: "opencode-msb-vm-test"}` so the test fails at `Attach()` rather than blocking interactively.

### Scenario mapping

| Test | Command | Args | Key assertion | Key mock check |
|------|---------|------|---------------|----------------|
| R1 | run | `--dry-run` | Info: "dry-run: Would run opencode" | `CreateSandbox` not called |
| R2 | shell | `--dry-run` | Info: "dry-run: Would start interactive shell session" | `CreateSandbox` not called |
| R3 | run | `--dry-run --dry-run-vm` | Info + `VerboseCalls` contains "auto-enabled" | `CreateSandbox` not called |
| R4 | shell | `--dry-run --dry-run-vm` | Info + `VerboseCalls` contains "auto-enabled" | `CreateSandbox` not called |
| R5 | run | (default) | Error: "opencode session failed" | `CreateSandbox` opts `Auto=true`; `Attach` called |
| R6 | shell | (default) | Error: "opencode session failed" | `CreateSandbox` opts `Auto=false`; `Attach` called with `["/bin/bash", "-l"]` |
| R7 | run | `--dry-run --no-auto` | Info: "dry-run: Would run opencode" | No mock calls |
| R9 | run | `--branch x --cpus 2 --memory 8G --user alice` | Error from Attach | `CreateSandbox` opts contain all 4 |
| R10 | run | `--branch main -c 4 -m 16G -u root` | Error from Attach | Same as R9 via fixture |
| R11 | run | (no --no-auto) | `ExitError{Code: 0}` (mock attach returns ok) | Attach arg string contains "--auto" |
| R12 | shell | `--branch foo --cpus 2` | Error from Attach | `CreateSandbox` opts `Branch="foo", CPUs=2, Auto=false` |

Note: R8 (Run with branch) is covered by R9's mock check. The dry-run path (R1–R4, R7) uses no mock chain; the non-dry-run path (R5–R6, R9–R12) uses the full mock chain.

**Mock construction helper**: A builder-like function in the test file:
```go
func makeFullMock() *fullMock {
    sb := &sandboxMock{attachErr: fmt.Errorf("mock"), name: "oc-vm-test"}
    return &fullMock{
        listSandboxesResult: nil,
        ensureImageResult:   []mockImageHandle{{ref: "opencode-msb/runner:test", digest: "sha256-abc"}},
        listVolumesResult:   nil,
        getSandboxResult:    nil, // will trigger CreateSandbox
        createSandboxResult: sb,
        createSandboxOpts:   make([]msb.SandboxOption, 0),
    }
}
```

The `fullMock` tracks all calls (createSandboxOpts is a slice of the actual options passed, verified via reflection or custom assertion).

**Deliverable**:
- 12 passing tests
- `go test ./cmd/opencode-msb/ -run TestRunShell -v` passes

**Dependencies**: Task 1 (dry-run verbose marker), Task 2 (fixtures)

---

## Task 6: Tree + Version tests

Simplest tests. Verify static output without dependencies on mocks.

**File**: `cmd/opencode-msb/cli_tree_test.go`

**Scope**: 5 scenarios (T1–T5)

| Test | Fixture | Assertions |
|------|---------|------------|
| T1 | `buildRootCmd(mockUI).RunE(nil, nil)` | `InfoCalls[0] == "opencode-msb"` |
| T2 | Same | Tree contains: run, doctor, build, list, shell, config, image, volume, stop, kill, prune |
| T3 | Same | Tree contains flag descriptions: "Assume yes to all prompts", "Memory limit" |
| T4 | `version = "dev"` (default) | `OutCalls[0]` contains `"opencode-msb dev"` |
| T5 | Pre-`version = "1.2.3"` | `OutCalls[0]` contains `"opencode-msb 1.2.3"` |

No mock injection needed (tree and version don't call sandbox or Docker).

**Deliverable**:
- 5 passing tests
- `go test ./cmd/opencode-msb/ -run TestTree -v` passes

**Dependencies**: Task 2 (fixtures for mockUI setup — or use inline `stdio.Mock{}`)

---

## Task 7: Verification

Final integration verification after all tasks are complete.

- `go test ./cmd/opencode-msb/ -v` runs all 55+ scenarios
- `go test ./...` runs full test suite (no regressions)
- `golangci-lint run` passes
- `go vet ./...` passes
- Verify no dead code (unused imports in test files)
- Verify all test names contain scenario IDs for traceability to spec

**Deliverable**: Clean CI in local environment

---

## Dependency Graph

```
┌───────────────┐
│  Task 1       │  ← no dependencies
│  Impl changes │
└───────┬───────┘
        │
        ┌───────────────┐
        │  Task 2        │  ← needed by tasks 3, 4, 5
        │  Fixtures +    │
        │  List tests    │
        └───────┬────────┘
                │
   ┌────────────┼────────────┐
   │               │              │
┌──┴───┐    ┌─────┴─────┐  ┌─────┴─────┐
│Task 4│    │  Task 3    │  │  Task 6   │
│Build │    │Lifecycle   │  │Tree/Version│
│+Prune│    │(stop/kill) │  │            │
└──────┘    └─────┬─────┘  └────────────┘
                  │
           ┌──────┴──────┐
           │  Task 5      │
           │  Run+Shell   │
           └─────────────┘
```

**Parallelization opportunities**:
- Task 3, 4, 6 are independent of each other (all depend only on Task 1 + Task 2)
- They could be dispatched in parallel on 3 subagents
- Task 5 (run/shell) should run last due to highest complexity and indirect coupling (full mock chain interacts with sandbox package changes)

---

## Success Criteria (across all tasks)

1. All 55 scenarios from spec have corresponding passing tests
2. Test names and scenario IDs in comments match spec exactly
3. `go test ./cmd/opencode-msb/ -v` runs in under 10 seconds
4. No test depends on Docker, KVM, msb, or network access
5. `golangci-lint run` and `go vet ./...` clean
6. New test files follow existing Go test conventions (table-driven, `t.Run`, `t.Cleanup`)