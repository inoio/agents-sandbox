---
description: Design for issue #919 - stop dropping `PruneStorage`'s per-object errors and shared-memory entries from the launcher's `PruneReport`, and record `DiagnoseStorage` and `RecoverStorage` as deferred.
read_if: Implementing #919 (the richer `PruneReport`/`PruneEntry`, the prune render, the `storagePruneReportProjected` register); changing `pruning.PruneStorage` or the `prune` report output; or asking why storage diagnosis/recovery is not wired.
---

# msb storage: a richer prune report; diagnosis and recovery deferred

Status: proposed. Issue #919. Base: branch `chore/923-msb-adapter` (PR #928); lands after #923
merges. Supersedes the #919 half of `guides/msb-sdk-integration.md` §1.5.

## Intent

`prune` calls the runtime's `PruneStorage` for RAM backings, but the launcher's `PruneReport` keeps
only the aggregates (`FilesRemoved`, `LogicalBytesRemoved`, `PhysicalBytesReclaimed`) and drops the
SDK's per-object `Entries` and `Truncated` flag. A prune in which some files failed therefore reads
as a clean, complete prune. This design surfaces that detail; the SDK's storage **diagnosis** and
**recovery** are recorded as deferred, not built.

## Goals

- Carry `PruneStorage`'s per-object outcomes (path, kind, bytes, state, error) and the `Truncated`
  flag through the launcher's report, so a partial prune is visible in the human render and
  `--format json`.
- Join any per-entry error into the aggregate prune error so the summary is not silently optimistic.
- Keep the launcher's one prune/reclamation policy (`specs/vm/lifecycle.md` liveness); this change
  only reports more of what the runtime already returns.

## Non-goals

- `DiagnoseStorage` - deferred (recorded below): it is a new observation path with no current
  surface (`doctor` is not a storage-diagnosis command; `df`/`sandbox inspect` are the observation
  surfaces).
- `RecoverStorage` - deferred: it selects a disk view and mutates storage, a second recovery policy
  beside the launcher's prune.
- A `storage diagnose`/`recover` command, a `doctor` storage check, or a VM-start failure hint.
- Changing the prune's reclamation rules or the `--older-than` semantics.

## Anti-goals

- Rendering an unknown byte count (a nil pointer) as `0`.
- Reporting a per-entry error as a hard failure of the whole prune: the prune is partial, and the
  report says so; the aggregate error carries the detail.
- Adopting `DiagnoseStorage`/`RecoverStorage` without a consumer, only because the SDK exposes them.

## Constraints

- `msb-seam.md`: `PruneReport` is already launcher-owned (it narrows `PhysicalBytesReclaimed`), so
  growing it is the seam's stated pattern; the reflection register
  (`translate.go#storagePruneReportProjected`) shrinks as fields are projected.
- `phase4_boundary_test.go` stays green; `roles_boundary_test.go` is untouched (no interface method
  changes - `PruneStorage`'s signature is unchanged).
- Format with `make fmt`; lint with `make lint`; run `make check` before finalizing.

## Current state (measured on the #923 base)

- `msb.PruneReport` (`storage_values.go`) has `FilesRemoved`, `LogicalBytesRemoved`,
  `PhysicalBytesReclaimed *uint64`; the register names `DryRun`, `Entries`, `Truncated` as
  deliberately not modeled.
- The SDK's `StoragePruneReport` (`ffi.StoragePruneReport`) is `DryRun`, `Entries
  []MemoryCacheEntry`, `FilesRemoved`, `LogicalBytesRemoved`, `PhysicalBytesReclaimed *uint64`,
  `Truncated`; `MemoryCacheEntry` is `Path`, `Kind`, `LogicalBytes *uint64`, `AllocatedBytes
  *uint64`, `State string`, `Error *string`.
- `pruning.PruneStorage` maps the report to the launcher `pruning.StorageReport` (totals only) and
  warns on the call error; per-file errors inside a successful report are invisible.
- The `prune` report render (`internal/cliout/prune.go`, `RenderPruneReport`) prints counts and a
  `details` array for the artifact groups; the runtime-RAM phase's detail is totals only.

## Approaches considered

- **A (chosen): extend the launcher `PruneReport` with `Entries []PruneEntry` and `Truncated`.**
  Surface them through `pruning.StorageReport`, add errored entries as warnings in the human render
  and to `--format json`, and join them into the aggregate error.
- **B: keep totals only.** Rejected: it is the silent-partial-prune the issue names.
- **C: alias the SDK report.** It carries no enum, but `PruneReport` is launcher-owned and already
  narrows a field; extending it keeps the launcher name and the seam pattern.

## The design

### A richer `PruneReport`

```go
// PruneEntry is one runtime-memory backing's prune outcome. Error is set when the
// backing could not be pruned; State explains eligibility or retention.
type PruneEntry struct {
    Path           string
    Kind           string
    LogicalBytes   *uint64
    AllocatedBytes *uint64
    State          string
    Error          *string
}

type PruneReport struct {
    FilesRemoved           uint64
    LogicalBytesRemoved    uint64
    PhysicalBytesReclaimed *uint64
    Entries                []PruneEntry
    Truncated              bool
}
```

- `translate.go` projects `Entries`/`Truncated`; the register drops those two from the
  not-modeled set (`DryRun` stays unmodeled - the caller already knows it from the request and
  echoes it in the report's `DryRun` field).
- `pruning.StorageReport` gains `Entries []PruneEntry` and `Truncated bool`; the runtime-RAM phase
  copies them through.
- Render: the human output lists entries whose `Error != nil` as warnings and notes `Truncated`;
  `--format json` includes the entries. Unknown bytes stay `null`/`-`, never `0`.
- The aggregate prune error (the "summary, then error" rule in
  `specs/volumes/runtime-management-commands.md`) joins the per-entry errors, so an exit code and a
  message reflect the partial failure.

**Return contract.** `pruning.PruneStorage` returns the populated `StorageReport` (with its entries)
whenever the SDK `PruneStorage` call itself succeeds; a per-entry error lives in the report, not in
the returned `error`. It keeps today's zero-report-on-**call**-error behaviour (a call error returns
the zero report and the error). So the errored-entry case must not mirror the call-error branch and
zero the report - the entries would be discarded exactly when they matter. The phase aggregator
(`pruning.Prune`, which builds `report.Storage`) extends its joined error from the entries, so an
entry error reaches both the report and the aggregate error.

**Renderer ownership.** `pruning.RenderPruneSummary` is shared by the explicit `prune` command
(`prune_cli.go`) and `AutoPrune` on every launch (`cleanup.go`). Per-entry warnings and the
`Truncated` note therefore belong to the **explicit command's** human render (the human func passed
to `cliout.RenderPruneReport`), not the shared summary: `prune` and `--format json` show the detail,
while an auto-prune launch stays terse.

**Type location.** `msb.PruneEntry` is the seam type; `pruning.StorageReport.Entries` is
`[]msb.PruneEntry` (pruning already imports `msb`), so no second domain type or mapping is added.
`view`'s JSON DTO declares its own entry struct and reads `msb.PruneEntry`'s fields, so `view` stays
`msb`-free.

## Recorded decisions

- **`DiagnoseStorage`** - deferred. The SDK call is read-only but adds an observation path with no
  surface to host it: the management surface is bounded (`specs/volumes/runtime-management-commands.md`)
  and `doctor` checks host prerequisites, not per-sandbox storage. Deferred until a consumer exists
  (an explicit `storage diagnose` command or a documented VM-start diagnosis hint).
- **`RecoverStorage`** - deferred. It chooses a disk view (`StorageRecoveryOptions.AcceptSelectedView`)
  and mutates storage; wired automatically it would be a second recovery policy and a silent data
  choice. Revive only as an explicit, acknowledgement-gated command.

## Spec and guide updates

- `specs/volumes/runtime-management-commands.md`: the `prune` report contract's JSON/human shape
  gains the per-entry outcomes and `truncated`.
- `guides/msb-sdk-integration.md` §1.5: fold in the adopted richer prune and the deferred
  diagnosis/recovery.
- `changelog.d/<PR-number>.fixed.md`: the per-object prune outcomes and partial-failure reporting
  (user-visible).

## Testing

- Unit (`msb`): `PruneReport` projection carries `Entries`/`Truncated` and keeps nil bytes nil; the
  register no longer lists them.
- Unit (`pruning`): `PruneStorage` returns the populated report (not zero) when the SDK call
  succeeds with an errored entry, and the aggregate joins the entry error; a report with an errored
  entry is surfaced as a warning; `Truncated` is noted; a clean report is unchanged.
- CLI (`cmd/agents-sandbox`): `prune --format json` carries the entries; the explicit command shows
  an errored entry as a warning and notes `truncated`; an auto-prune launch stays terse.
- Real VM (`msb` tag / KVM host): `PruneStorage` on a home with eligible branch memory returns
  entries; the launcher surfaces them.

## Estimates

- `msb`: the `PruneEntry` type, the `PruneReport` growth, the projector, the register edit, the mock
  unchanged (it returns a `PruneReport`). Small.
- `pruning`: carry entries/truncated and join errors. Small.
- Docs/render. Small.

## What is left to the builder

- Whether the entries join the existing `details` array in `--format json` or a new
  `prune.storage.entries` key (the spec owns the shape).
- Whether an errored entry fails the prune command or only warns; the design proposes warn-and-join.

## References

- `.operator-shared/guides/msb-sdk-integration.md` §1.5
- microsandbox v0.8.3 `docs/sdk/storage.mdx`, `sdk/go/core/internal/ffi/storage.go`
  (`StoragePruneReport`, `MemoryCacheEntry`)
- `.operator-shared/specs/volumes/runtime-management-commands.md`, `specs/vm/lifecycle.md`
- `internal/sandbox/msb/storage_values.go`, `translate.go#storagePruneReportProjected`,
  `internal/sandbox/pruning/storage.go`, `internal/cliout/prune.go`
