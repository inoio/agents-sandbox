# Task 4b: Prune Tests

## Deliverable

File: `cmd/opencode-msb/cli_prune_test.go`

Tests `buildPruneCmd` with 10 scenarios using `pruneAgeFlags` fixture from `cli_fixture_test.go`.

## Summary

All 10 test scenarios pass (P1-P10), each with fixture flag iteration (4 runs per fixture-scenario = 40 passing test sub-tests).

## Scenarios

| # | Scenario | Fixture? | Flags | Description |
|---|----------|----------|-------|-------------|
| P1 | No stale items | Yes | All pruneAgeFlags | Empty mock lists → zero summary |
| P2 | Dry-run with stale items | Yes | --dry-run + flags | Stale VM cascade (1 VM, 1 vol, 1 docker img, 1 msb img) |
| P3 | Partial failures | Yes | Default flags | Two stale VMs pruned; mock doesn't support per-VM error |
| P4 | Custom age "2w" | No | --age 2w | Stale items > 14d; clone volume pruned |
| P5 | Custom age "14d" | No | --age 14d | Same as P4 with explicit 14d flag |
| P6 | Invalid age error | No | --age invalid | Error contains "invalid age"; no mock needed |
| P7 | Docker client error | Yes | Docker override | Override newDockerClient to return error |
| P8 | Clone volumes pruned | Yes | Default flags | 1 stale VM + 1 clone vol + 1 task sandbox |
| P9 | Task sandboxes pruned | Yes | Default flags | Active VM cleanup + 1 task sandbox |
| P10 | Valid age via -a | Yes | pruneAgeFlags[0] | Stale items pruned with all flag values |

## Key Findings

### `SetNewMsbClient` (sandbox/build_image_testutil.go)

Added `SetNewMsbClient` export to `internal/sandbox/build_image_testutil.go` to allow CLI tests to override `newMsbClient` (the internal factory used by `sandbox.Prune`). This was necessary because `Prune` uses `newMsbClient()` (unexported), not `NewMsbClient` (public alias). The existing fixture helper `overrideMsbClient` replaced `NewMsbClient` which did not affect `Prune`.

### Digest Matching Behavior

The prune logic matches MSB images and Docker images against the active VM's snapshot digest. When the MSB image tag differs from the active VM's image digest, both the MSB image AND Docker image are pruned. Clone volumes for that project are also pruned because the project slug in the clone name doesn't match any active digest entry.

### MockLimitations

`MockSandboxHandle.RemoveErr` does not affect `MockMsbClient.RemoveSandbox` calls. The mock does not support per-VM error injection, so P3 tests both VMs succeeding rather than partial failure.

## Verification

- `go test ./cmd/opencode-msb/ -run TestPrune -v` — 40 passing sub-tests
- `golangci-lint run ./cmd/opencode-msb/` — 0 issues
- `go build ./...` — clean