---
description: TDD implementation plan for #919 - carry `PruneStorage`'s per-object `Entries` and `Truncated` through the msb seam and `pruning.StorageReport`, surface them in the explicit `prune` render and JSON, and join per-entry errors into the aggregate prune error.
read_if: Executing #919; touching `msb.PruneReport`/`PruneEntry`, `pruneReportFrom` or the `storagePruneReportProjected` register, `pruning.PruneStorage`/`StorageReport`, the `prune` aggregate error, or the explicit `prune` human/JSON render; a `prune` that reads as clean despite per-object failures.
---

# msb storage prune report Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop dropping the microsandbox `PruneStorage` report's per-object `Entries` (path, kind, bytes, state, error) and `Truncated` flag at the launcher seam, surface them in the explicit `prune` render and `--format json`, and join per-entry errors into the aggregate prune error so a partial prune is never reported as clean.

**Architecture:** The `internal/sandbox/msb` seam grows `PruneEntry` and two `PruneReport` fields; `pruneReportFrom` projects them and the drift register reclassifies `Entries`/`Truncated` as projected (`DryRun` stays dropped). `pruning.StorageReport` carries `[]msb.PruneEntry` and `Truncated`; `PruneStorage` keeps today's zero-report-on-call-error branch but returns the populated report on call success even when an entry is errored. The aggregate `Prune` folds entry errors into its joined error. `view.PruneReportJSON` gains its own entry DTO (no `msb` import), and the explicit command's human render — not the shared `RenderPruneSummary` that `AutoPrune` uses — warns on errored entries and notes truncation.

**Tech Stack:** Go 1.27, module `github.com/inoio/agents-sandbox`, microsandbox SDK v0.8.3, golangci-lint.

**Spec:** `.operator-shared/plans/2026-11-27-msb-storage-diagnosis-design.md`

## Global Constraints

- Base is `main` after #923 (PR #928) merges; #923's explicit `LocalContext` destination and the observation aliases are present. Reference the seam as it is post-#923.
- `phase4_boundary_test.go` must stay green: no production (non-`_test.go`) package outside `internal/sandbox/msb` may import `msbSdk`.
- `roles_boundary_test.go` is untouched: `PruneStorage`'s interface signature does not change; no role/composite method moves.
- `PruneReport` is launcher-owned (`msb-seam.md`); growing it is the seam's stated pattern. The reflection register (`translate.go#storagePruneReportProjected`) drops `Entries`/`Truncated` from the not-modeled set; `DryRun` stays unmodeled (the caller issued the prune and echoes it).
- `msb.PruneEntry` is the seam type; `pruning.StorageReport.Entries` is `[]msb.PruneEntry` (pruning already imports `msb`), so no second domain type or mapping is added. `internal/view` declares its own entry DTO and reads `msb.PruneEntry`'s fields by inference, so the `view` **production** package needs no `msb` import (its test builds `[]msb.PruneEntry` and imports `msb` as usual).
- Nil-not-zero: an unknown per-entry byte count (`LogicalBytes`/`AllocatedBytes` nil) renders `-` in the human output and `null` in JSON, never `0`.
- Return contract: `pruning.PruneStorage` returns the populated `StorageReport` whenever the SDK `PruneStorage` call itself succeeds, even with an errored entry; a per-entry error lives in the report, not in the returned `error`. A **call** error still returns the zero report and the error.
- Warn-and-join: an entry error is surfaced as a warning in the explicit command's human render **and** joined into the aggregate prune error, so `prune` exits non-zero on a partial prune while still rendering the report first ("summary, then error").
- Renderer ownership: the per-entry warnings and the `Truncated` note belong to the explicit `prune` command's human render, **not** `pruning.RenderPruneSummary` (which `AutoPrune` calls on every launch).
- Unit tests stay in the unit's file: `msb` projection/register in `translate_test.go`, the adapter in `msb_test.go`, `pruning.StorageReport`/`PruneStorage` in `storage_test.go`, the aggregate and summary in `prune_test.go`, the JSON DTO in `view/prune_test.go`, the CLI in `cmd/agents-sandbox/cli_prune_test.go`.
- Unit tests stay FFI-free: swap the package-level `storagePrune` seam or use `msbtest` mocks; never link the native library.
- Format with `make fmt` (never `go fmt`); lint with `make lint` (never `go vet`); run `make check` before finalizing.
- A user-visible change needs `changelog.d/<PR-number>.fixed.md` (the literal `<PR-number>` placeholder).

## Review Focus

- **A per-entry byte count the runtime could not measure renders as `0`** instead of `null`/`-`. Pinned by Task 1's `TestPruneReportFromKeepsUnknownBytesNil` and Task 4's `TestNewPruneReportJSONIncludesRuntimeMemoryEntries` (`"allocated_bytes":null`).
- **An errored entry zeroes the report** by mirroring the SDK-call-error branch, discarding the very entries that matter. Pinned by Task 2's `TestPruneStorageErroredEntryReturnsPopulatedReport`.
- **A per-entry error never reaches the aggregate error**, so a partial prune still reports success. Pinned by Task 3's `TestPruneJoinsStorageEntryError` and Task 4's `TestPruneHumanOutputShowsRuntimeMemoryFailures`.
- **The shared `RenderPruneSummary` gains the per-entry warnings/truncated note**, making every auto-prune launch noisy. Pinned by Task 4's `TestRenderPruneSummaryIgnoresStorageEntries`.
- **A clean runtime-memory phase changes its output** — an empty entries listing marshals as `null`, or a clean phase joins a spurious error. Pinned by Task 4's `TestNewPruneReportJSONEmptyRuntimeEntriesMarshalArray` and Task 3's `TestPruneCleanStorageReportKeepsNilError`.

---

### Task 1: Carry per-object entries and truncated through the msb seam

**Files:**
- Modify: `internal/sandbox/msb/storage_values.go`
- Modify: `internal/sandbox/msb/translate.go`
- Modify: `internal/sandbox/msb/translate_test.go`
- Modify: `internal/sandbox/msb/msb_test.go`

**Interfaces:**
- Consumes: `msbSdk.MemoryCacheEntry` (`Path`, `Kind`, `LogicalBytes *uint64`, `AllocatedBytes *uint64`, `State`, `Error *string`) and `msbSdk.StoragePruneReport.Entries`/`Truncated` (SDK v0.8.3).
- Produces: `type PruneEntry struct { Path string; Kind string; LogicalBytes *uint64; AllocatedBytes *uint64; State string; Error *string }` in package `msb`.
- Produces: `PruneReport.Entries []PruneEntry` and `PruneReport.Truncated bool`.
- Produces (unchanged signature): `func pruneReportFrom(*msbSdk.StoragePruneReport) *PruneReport`, now projecting `Entries`/`Truncated`.

- [ ] **Step 1: Write the failing projection and register tests**

Append to `internal/sandbox/msb/translate_test.go` (package `msb`; `msbSdk` is already imported):

```go
func TestPruneReportFromProjectsEntries(t *testing.T) {
	logical := uint64(2048)
	allocated := uint64(4096)
	entryErr := "locked by a running sandbox"
	report := pruneReportFrom(&msbSdk.StoragePruneReport{
		Truncated: true,
		Entries: []msbSdk.MemoryCacheEntry{
			{
				Path:           "/home/.msb/branch/one",
				Kind:           "branch_memory",
				LogicalBytes:   &logical,
				AllocatedBytes: &allocated,
				State:          "retained",
			},
			{Path: "/home/.msb/branch/two", Kind: "snapshot_memory", State: "error", Error: &entryErr},
		},
	})
	if report == nil {
		t.Fatal("pruneReportFrom() = nil, want a report")
	}
	if !report.Truncated {
		t.Error("Truncated = false, want true")
	}
	if len(report.Entries) != 2 {
		t.Fatalf("Entries = %+v, want two entries", report.Entries)
	}
	first := report.Entries[0]
	if first.Path != "/home/.msb/branch/one" || first.Kind != "branch_memory" ||
		first.LogicalBytes == nil || *first.LogicalBytes != logical ||
		first.AllocatedBytes == nil || *first.AllocatedBytes != allocated ||
		first.State != "retained" || first.Error != nil {
		t.Errorf("first entry = %+v, want the published backing copied field for field", first)
	}
	second := report.Entries[1]
	if second.Path != "/home/.msb/branch/two" || second.Error == nil || *second.Error != entryErr {
		t.Errorf("second entry = %+v, want the errored backing", second)
	}
}

// TestPruneReportFromKeepsUnknownBytesNil pins the nil-not-zero convention for a
// per-entry byte count the runtime could not measure.
func TestPruneReportFromKeepsUnknownBytesNil(t *testing.T) {
	report := pruneReportFrom(&msbSdk.StoragePruneReport{
		Entries: []msbSdk.MemoryCacheEntry{
			{Path: "/home/.msb/branch/one", Kind: "branch_memory", State: "eligible"},
		},
	})
	if report == nil || len(report.Entries) != 1 {
		t.Fatalf("pruneReportFrom() = %+v, want one entry", report)
	}
	if report.Entries[0].LogicalBytes != nil || report.Entries[0].AllocatedBytes != nil {
		t.Errorf("entry = %+v, want nil byte pointers", report.Entries[0])
	}
}

// TestStoragePruneReportRegisterProjectsEntries pins that the drift register
// classifies Entries and Truncated as projected, not dropped, while DryRun stays
// dropped.
func TestStoragePruneReportRegisterProjectsEntries(t *testing.T) {
	for _, field := range []string{"Entries", "Truncated"} {
		if _, ok := storagePruneReportProjected[field]; !ok {
			t.Errorf("storagePruneReportProjected missing %q", field)
		}
		if _, ok := storagePruneReportNotModeled[field]; ok {
			t.Errorf("storagePruneReportNotModeled still lists %q", field)
		}
	}
	if _, ok := storagePruneReportNotModeled["DryRun"]; !ok {
		t.Error("storagePruneReportNotModeled missing DryRun")
	}
}
```

Then extend `TestRealMsbClientPruneStorageSuccess` in `internal/sandbox/msb/msb_test.go` so the adapter's fake report carries an entry and truncation, and assert they reach the launcher report: set `Truncated: true` and `Entries: []msbSdk.MemoryCacheEntry{{Path: "/home/.msb/branch/one", Kind: "branch_memory", State: "removed"}}` on the returned `&msbSdk.StoragePruneReport{...}`, then after the existing total assertions add

```go
	if !report.Truncated || len(report.Entries) != 1 || report.Entries[0].Path != "/home/.msb/branch/one" {
		t.Errorf("PruneStorage() = %+v, want the entry and truncated flag carried", report)
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/msb/ -run 'TestPruneReportFromProjectsEntries|TestPruneReportFromKeepsUnknownBytesNil|TestStoragePruneReportRegisterProjectsEntries'`
Expected: build failure, `report.Truncated undefined (type *PruneReport has no field or method Truncated)`.

- [ ] **Step 3: Implement the type, the projection, and the register edit**

In `internal/sandbox/msb/storage_values.go`, add `PruneEntry` and grow `PruneReport`:

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

// PruneReport keeps the reclaimed totals and the per-backing outcomes. The SDK's
// DryRun echo is not surfaced: the caller issued the prune and knows it.
type PruneReport struct {
	FilesRemoved           uint64
	LogicalBytesRemoved    uint64
	PhysicalBytesReclaimed *uint64
	Entries                []PruneEntry
	Truncated              bool
}
```

In `internal/sandbox/msb/translate.go`, move `Entries`/`Truncated` from `storagePruneReportNotModeled` into `storagePruneReportProjected` (leave `DryRun` in the not-modeled map with its existing reason), update the register's doc comment, and project the fields. `pruneReportFrom` gains `Entries: pruneEntriesFrom(report.Entries)` and `Truncated: report.Truncated`, plus a translator:

```go
// pruneEntriesFrom translates the SDK's per-backing outcomes, preserving nil as
// nil and each optional byte count as nil when the runtime could not measure it.
func pruneEntriesFrom(entries []msbSdk.MemoryCacheEntry) []PruneEntry {
	if entries == nil {
		return nil
	}
	out := make([]PruneEntry, len(entries))
	for i, entry := range entries {
		out[i] = PruneEntry{
			Path:           entry.Path,
			Kind:           entry.Kind,
			LogicalBytes:   entry.LogicalBytes,
			AllocatedBytes: entry.AllocatedBytes,
			State:          entry.State,
			Error:          entry.Error,
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests and the lint**

Run: `go test ./internal/sandbox/msb/` → PASS (including the unchanged `TestSDKStructFieldsAreClassified`, which fails if a field is left in both maps).
Run: `make lint` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/storage_values.go internal/sandbox/msb/translate.go internal/sandbox/msb/translate_test.go internal/sandbox/msb/msb_test.go
git commit -m "feat(msb): carry PruneStorage's per-object entries and truncated flag"
```

---

### Task 2: PruneStorage carries entries/truncated with the stated return contract

**Files:**
- Modify: `internal/sandbox/pruning/report.go` (`StorageReport`; add the `msb` import)
- Modify: `internal/sandbox/pruning/storage.go` (`PruneStorage`)
- Modify: `internal/sandbox/pruning/storage_test.go`

**Interfaces:**
- Consumes: `msb.PruneEntry`, `msb.PruneReport.Entries`/`Truncated` from Task 1.
- Produces: `StorageReport.Entries []msb.PruneEntry` and `StorageReport.Truncated bool`; `PruneStorage` copies them through and returns the populated report on SDK **call** success.

- [ ] **Step 1: Write the failing carry and return-contract tests**

Append to `internal/sandbox/pruning/storage_test.go` (package `pruning`; `msb`, `msbtest`, `termiotest`, `time` are already imported):

```go
// TestPruneStorageCarriesEntriesAndTruncated pins that the runtime's per-object
// outcomes and listing cap reach the launcher report.
func TestPruneStorageCarriesEntriesAndTruncated(t *testing.T) {
	logical := uint64(2048)
	client := &msbtest.MockMsbClient{
		StoragePruneFn: func(context.Context, msb.PruneRequest) (*msb.PruneReport, error) {
			return &msb.PruneReport{
				FilesRemoved: 1,
				Entries: []msb.PruneEntry{
					{Path: "/home/.msb/branch/one", Kind: "branch_memory", LogicalBytes: &logical, State: "removed"},
				},
				Truncated: true,
			}, nil
		},
	}
	msbtest.WithMsbMock(t, client)

	report, err := PruneStorage(context.Background(), PruneState{Age: time.Hour}, false, &termiotest.Mock{})
	if err != nil {
		t.Fatalf("PruneStorage: %v", err)
	}
	if !report.Truncated {
		t.Error("Truncated = false, want true")
	}
	if len(report.Entries) != 1 || report.Entries[0].Path != "/home/.msb/branch/one" ||
		report.Entries[0].LogicalBytes == nil || *report.Entries[0].LogicalBytes != logical {
		t.Errorf("Entries = %+v, want the runtime entry", report.Entries)
	}
}

// TestPruneStorageErroredEntryReturnsPopulatedReport pins the return contract: a
// per-entry error is data on the report, not a call failure, so the report must
// not be zeroed the way the SDK-call-error branch does.
func TestPruneStorageErroredEntryReturnsPopulatedReport(t *testing.T) {
	entryErr := "locked by a running sandbox"
	client := &msbtest.MockMsbClient{
		StoragePruneFn: func(context.Context, msb.PruneRequest) (*msb.PruneReport, error) {
			return &msb.PruneReport{
				FilesRemoved: 1,
				Entries: []msb.PruneEntry{
					{Path: "/home/.msb/branch/locked", Kind: "branch_memory", State: "error", Error: &entryErr},
				},
			}, nil
		},
	}
	msbtest.WithMsbMock(t, client)

	report, err := PruneStorage(context.Background(), PruneState{Age: time.Hour}, false, &termiotest.Mock{})
	if err != nil {
		t.Fatalf("PruneStorage error = %v, want nil (a per-entry error is data, not a call failure)", err)
	}
	if len(report.Entries) != 1 || report.Entries[0].Error == nil {
		t.Fatalf("report = %+v, want the errored entry preserved", report)
	}
	if report.FilesRemoved != 1 {
		t.Errorf("FilesRemoved = %d, want the runtime total preserved", report.FilesRemoved)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/pruning/ -run 'TestPruneStorageCarriesEntriesAndTruncated|TestPruneStorageErroredEntryReturnsPopulatedReport'`
Expected: build failure, `report.Truncated undefined (type StorageReport has no field or method Truncated)`.

- [ ] **Step 3: Grow `StorageReport` and copy the fields through**

In `internal/sandbox/pruning/report.go`, add `"github.com/inoio/agents-sandbox/internal/sandbox/msb"` to the imports and extend `StorageReport`:

```go
// StorageReport summarizes a runtime-memory reclaim run. PhysicalBytesReclaimed
// is nil when the runtime cannot measure it, following the nil-not-zero
// convention; it is never rendered as zero. Entries carries the runtime's
// per-backing outcomes (an entry with Error set is a backing that could not be
// pruned); Truncated records that the runtime capped its entry listing.
type StorageReport struct {
	FilesRemoved           uint64
	LogicalBytesRemoved    uint64
	PhysicalBytesReclaimed *uint64
	Entries                []msb.PruneEntry
	Truncated              bool
}
```

In `internal/sandbox/pruning/storage.go`, add `Entries: report.Entries` and `Truncated: report.Truncated` to the returned `StorageReport{...}`. Leave the `if err != nil` branch returning `StorageReport{}` and the error unchanged.

- [ ] **Step 4: Run the tests and the lint**

Run: `go test ./internal/sandbox/pruning/` → PASS (the existing `TestPruneStorageFailureWarnsAndReturnsError` still pins the zero-report call-error branch).
Run: `make lint` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pruning/report.go internal/sandbox/pruning/storage.go internal/sandbox/pruning/storage_test.go
git commit -m "feat(pruning): carry runtime-memory prune entries through StorageReport"
```

---

### Task 3: Join per-entry errors into the aggregate prune error

**Files:**
- Modify: `internal/sandbox/pruning/prune.go` (the `Prune` aggregator; add `storageEntryErrors`)
- Modify: `internal/sandbox/pruning/prune_test.go`

**Interfaces:**
- Consumes: `StorageReport.Entries` from Task 2.
- Produces: `func Prune` returns a joined error that includes one error per entry with `Error != nil` (formatted `runtime memory: <path> (<kind>): <error>`); `report.Storage.Entries` stays populated.

- [ ] **Step 1: Write the failing aggregate tests**

Append to `internal/sandbox/pruning/prune_test.go` (package `pruning`; `msb`, `msbtest`, `dockertest`, `configpathstest`, `termiotest`, `strings`, `errors` are already imported):

```go
// TestPruneJoinsStorageEntryError proves a per-backing failure the runtime
// reported inside a successful call is folded into the aggregate error and kept
// on the report.
func TestPruneJoinsStorageEntryError(t *testing.T) {
	entryErr := "locked by a running sandbox"
	client := &msbtest.MockMsbClient{
		StoragePruneFn: func(context.Context, msb.PruneRequest) (*msb.PruneReport, error) {
			return &msb.PruneReport{
				Entries: []msb.PruneEntry{
					{Path: "/home/.msb/branch/locked", Kind: "branch_memory", State: "error", Error: &entryErr},
				},
			}, nil
		},
	}
	msbtest.WithMsbMock(t, client)
	dockertest.WithNoopDockerMock(t)
	configpathstest.WithMockConfigPaths(t)

	report, err := Prune(context.Background(), docker.Get(), PruneOptions{Age: 7 * 24 * time.Hour}, &termiotest.Mock{})
	if err == nil || !strings.Contains(err.Error(), entryErr) {
		t.Fatalf("Prune error = %v, want the entry error joined", err)
	}
	if len(report.Storage.Entries) != 1 || report.Storage.Entries[0].Error == nil {
		t.Errorf("Storage.Entries = %+v, want the errored entry on the report", report.Storage.Entries)
	}
}

// TestPruneCleanStorageReportKeepsNilError proves entries without an error add
// nothing to the aggregate error.
func TestPruneCleanStorageReportKeepsNilError(t *testing.T) {
	client := &msbtest.MockMsbClient{
		StoragePruneFn: func(context.Context, msb.PruneRequest) (*msb.PruneReport, error) {
			return &msb.PruneReport{
				FilesRemoved: 1,
				Entries: []msb.PruneEntry{
					{Path: "/home/.msb/branch/one", Kind: "branch_memory", State: "removed"},
				},
			}, nil
		},
	}
	msbtest.WithMsbMock(t, client)
	dockertest.WithNoopDockerMock(t)
	configpathstest.WithMockConfigPaths(t)

	if _, err := Prune(context.Background(), docker.Get(), PruneOptions{Age: 7 * 24 * time.Hour}, &termiotest.Mock{}); err != nil {
		t.Fatalf("Prune error = %v, want nil for a clean runtime-memory phase", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/pruning/ -run 'TestPruneJoinsStorageEntryError|TestPruneCleanStorageReportKeepsNilError'`
Expected: `TestPruneJoinsStorageEntryError` FAIL, `Prune error = <nil>, want the entry error joined`; the clean test passes (it is the guard).

- [ ] **Step 3: Fold the entry errors into the aggregate join**

In `internal/sandbox/pruning/prune.go`, replace the final `return report, errors.Join(vmErr, imgErr, storageErr, stateErr)` with a join that appends the entry errors (add `"fmt"` to the imports):

```go
	entryErrors := storageEntryErrors(storageReport)
	allErrors := append([]error{vmErr, imgErr, storageErr, stateErr}, entryErrors...)
	return report, errors.Join(allErrors...)
```

Add the helper:

```go
// storageEntryErrors collects the per-backing prune failures the runtime
// reported. They are data on the report, so the aggregate join folds them in
// explicitly.
func storageEntryErrors(report StorageReport) []error {
	var errs []error
	for _, entry := range report.Entries {
		if entry.Error != nil {
			errs = append(errs, fmt.Errorf("runtime memory: %s (%s): %s", entry.Path, entry.Kind, *entry.Error))
		}
	}
	return errs
}
```

- [ ] **Step 4: Run the tests and the lint**

Run: `go test ./internal/sandbox/pruning/` → PASS.
Run: `make lint` → clean.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pruning/prune.go internal/sandbox/pruning/prune_test.go
git commit -m "feat(pruning): join storage entry errors into the aggregate prune error"
```

---

### Task 4: Surface entries and truncated in the explicit prune render and JSON

**Files:**
- Modify: `internal/view/prune.go` (the entry DTO and `NewPruneReportJSON`)
- Modify: `internal/view/prune_test.go`
- Modify: `cmd/agents-sandbox/prune_cli.go` (`runPruneBatch`'s human closure; add `renderPruneStorageOutcomes`)
- Modify: `cmd/agents-sandbox/cli_prune_test.go`
- Modify: `internal/sandbox/pruning/prune_test.go` (the terse-summary guard)
- Create: `changelog.d/<PR-number>.fixed.md`

**Interfaces:**
- Consumes: `pruning.StorageReport.Entries`/`Truncated` from Task 2.
- Produces: `view.PruneReportJSON.RuntimeMemoryEntries []pruneStorageEntryJSON` (json `runtime_memory_entries`, non-nil so an empty list marshals `[]`) and `view.PruneReportJSON.RuntimeTruncated bool` (json `runtime_truncated`); the unexported `pruneStorageEntryJSON` struct with json keys `path`/`kind`/`logical_bytes`/`allocated_bytes`/`state`/`error`.
- Produces: `func renderPruneStorageOutcomes(ui termio.UI, report pruning.StorageReport)` in `package main`, called only from `runPruneBatch`'s human closure.

- [ ] **Step 1: Write the failing view tests**

Append to `internal/view/prune_test.go` (package `view`; add `"github.com/inoio/agents-sandbox/internal/sandbox/msb"` to its imports — only the test imports `msb`; the production `prune.go` stays `msb`-free; `pruning`, `strings`, `testing` are already imported):

```go
// TestNewPruneReportJSONIncludesRuntimeMemoryEntries pins that the
// runtime-memory phase's per-backing outcomes and truncated flag reach the JSON,
// with an unmeasured byte count as null.
func TestNewPruneReportJSONIncludesRuntimeMemoryEntries(t *testing.T) {
	logical := uint64(2048)
	entryErr := "locked by a running sandbox"
	got := NewPruneReportJSON(&pruning.StaleReport{
		Storage: pruning.StorageReport{
			FilesRemoved: 1,
			Truncated:    true,
			Entries: []msb.PruneEntry{
				{Path: "/home/.msb/branch/one", Kind: "branch_memory", LogicalBytes: &logical, State: "removed"},
				{Path: "/home/.msb/branch/locked", Kind: "branch_memory", State: "error", Error: &entryErr},
			},
		},
	}, false)
	if !got.RuntimeTruncated {
		t.Error("RuntimeTruncated = false, want true")
	}
	if len(got.RuntimeMemoryEntries) != 2 {
		t.Fatalf("RuntimeMemoryEntries = %+v, want two entries", got.RuntimeMemoryEntries)
	}
	if got.RuntimeMemoryEntries[0].LogicalBytes == nil || *got.RuntimeMemoryEntries[0].LogicalBytes != logical {
		t.Errorf("first entry = %+v, want the logical byte count", got.RuntimeMemoryEntries[0])
	}
	if got.RuntimeMemoryEntries[0].AllocatedBytes != nil {
		t.Errorf("first entry allocated = %v, want nil", got.RuntimeMemoryEntries[0].AllocatedBytes)
	}
	if got.RuntimeMemoryEntries[1].Error == nil || *got.RuntimeMemoryEntries[1].Error != entryErr {
		t.Errorf("second entry = %+v, want the entry error", got.RuntimeMemoryEntries[1])
	}
	encoded := marshal(t, got)
	for _, key := range []string{
		`"runtime_memory_entries":[`,
		`"path":"/home/.msb/branch/one"`,
		`"logical_bytes":2048`,
		`"allocated_bytes":null`,
		`"error":"locked by a running sandbox"`,
		`"runtime_truncated":true`,
	} {
		if !strings.Contains(encoded, key) {
			t.Errorf("JSON missing %s: %s", key, encoded)
		}
	}
}

// TestNewPruneReportJSONEmptyRuntimeEntriesMarshalArray pins that a clean
// runtime-memory phase marshals runtime_memory_entries as [] not null.
func TestNewPruneReportJSONEmptyRuntimeEntriesMarshalArray(t *testing.T) {
	encoded := marshal(t, NewPruneReportJSON(&pruning.StaleReport{}, false))
	if !strings.Contains(encoded, `"runtime_memory_entries":[]`) {
		t.Errorf("empty entries must marshal as []: %s", encoded)
	}
}
```

- [ ] **Step 2: Run the view tests to verify they fail**

Run: `go test ./internal/view/ -run 'TestNewPruneReportJSONIncludesRuntimeMemoryEntries|TestNewPruneReportJSONEmptyRuntimeEntriesMarshalArray'`
Expected: build failure, `got.RuntimeTruncated undefined (type PruneReportJSON has no field or method RuntimeTruncated)`.

- [ ] **Step 3: Write the failing CLI tests and the terse-summary guard**

Append to `cmd/agents-sandbox/cli_prune_test.go` (package `main`; `sandboxmsb`, `msbtest`, `termiotest`, `slices`, `strings` are already imported):

```go
// TestPruneFormatJSONIncludesRuntimeMemoryEntries pins that the explicit prune's
// JSON carries the runtime-memory per-backing outcomes and truncated flag. The
// errored entry also joins the aggregate error, so Execute reports it.
func TestPruneFormatJSONIncludesRuntimeMemoryEntries(t *testing.T) {
	entryErr := "locked by a running sandbox"
	m := &msbtest.MockMsbClient{
		StoragePruneFn: func(context.Context, sandboxmsb.PruneRequest) (*sandboxmsb.PruneReport, error) {
			return &sandboxmsb.PruneReport{
				FilesRemoved: 1,
				Truncated:    true,
				Entries: []sandboxmsb.PruneEntry{
					{Path: "/home/.msb/branch/one", Kind: "branch_memory", State: "removed"},
					{Path: "/home/.msb/branch/locked", Kind: "branch_memory", State: "error", Error: &entryErr},
				},
			}, nil
		},
	}
	cmd, ui := setupCommandFixtures(t, "prune", "--format", "json", "--yes")
	msbtest.WithMsbMock(t, m)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), entryErr) {
		t.Fatalf("Execute() error = %v, want the entry error joined", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	// cliout.RenderPruneReport marshals with json.MarshalIndent, so the output is
	// colon-spaced; assert the indented forms.
	for _, key := range []string{
		`"runtime_memory_entries": [`,
		`"path": "/home/.msb/branch/locked"`,
		`"error": "locked by a running sandbox"`,
		`"runtime_truncated": true`,
	} {
		if !strings.Contains(joined, key) {
			t.Errorf("JSON missing %s: %s", key, joined)
		}
	}
}

// TestPruneHumanOutputShowsRuntimeMemoryFailures pins that the explicit prune's
// human render warns about an errored backing and notes a truncated listing.
func TestPruneHumanOutputShowsRuntimeMemoryFailures(t *testing.T) {
	entryErr := "locked by a running sandbox"
	m := &msbtest.MockMsbClient{
		StoragePruneFn: func(context.Context, sandboxmsb.PruneRequest) (*sandboxmsb.PruneReport, error) {
			return &sandboxmsb.PruneReport{
				Truncated: true,
				Entries: []sandboxmsb.PruneEntry{
					{Path: "/home/.msb/branch/locked", Kind: "branch_memory", State: "error", Error: &entryErr},
				},
			}, nil
		},
	}
	cmd, ui := setupCommandFixtures(t, "prune", "--yes")
	msbtest.WithMsbMock(t, m)
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), entryErr) {
		t.Fatalf("Execute() error = %v, want the entry error joined", err)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool {
		return strings.Contains(s, "/home/.msb/branch/locked") && strings.Contains(s, entryErr)
	}) {
		t.Errorf("expected a warning naming the failed backing, got %v", ui.WarnCalls)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool {
		return strings.Contains(s, "truncated")
	}) {
		t.Errorf("expected a truncated-listing note, got %v", ui.WarnCalls)
	}
}
```

Append the terse-summary guard to `internal/sandbox/pruning/prune_test.go` (package `pruning`; `msb` and `termiotest` are already imported). It passes today and fails only if a later change folds the entries into the shared summary:

```go
// TestRenderPruneSummaryIgnoresStorageEntries pins that the summary AutoPrune
// prints on every launch stays terse: the per-backing failures and truncated
// note belong to the explicit prune command's human render, not this summary.
func TestRenderPruneSummaryIgnoresStorageEntries(t *testing.T) {
	entryErr := "locked by a running sandbox"
	ui := &termiotest.Mock{}
	RenderPruneSummary(ui, &StaleReport{
		Storage: StorageReport{
			FilesRemoved: 1,
			Truncated:    true,
			Entries: []msb.PruneEntry{
				{Path: "/home/.msb/branch/locked", Kind: "branch_memory", Error: &entryErr},
			},
		},
	}, false)
	if len(ui.WarnCalls) != 0 {
		t.Errorf("RenderPruneSummary emitted warnings, want none: %v", ui.WarnCalls)
	}
	// RenderPruneSummary writes through Outf/Verbosef, so a regression folding the
	// detail into the shared summary surfaces as an out/verbose line, not a warning.
	for _, line := range slices.Concat(ui.OutCalls, ui.VerboseCalls) {
		if strings.Contains(line, "truncated") || strings.Contains(line, "/home/.msb/branch/locked") {
			t.Errorf("RenderPruneSummary leaked the per-entry detail: %q", line)
		}
	}
}
```

- [ ] **Step 4: Run the CLI tests to verify they fail, and the guard to verify it passes**

Run: `go test ./cmd/agents-sandbox/ -run 'TestPruneFormatJSONIncludesRuntimeMemoryEntries|TestPruneHumanOutputShowsRuntimeMemoryFailures'`
Expected: FAIL — the JSON test fails the `runtime_memory_entries` assertion (the view DTO is not implemented yet) and the human test fails the warning assertion (`renderPruneStorageOutcomes` does not exist and `RenderPruneSummary` alone does not warn).
Run: `go test ./internal/sandbox/pruning/ -run TestRenderPruneSummaryIgnoresStorageEntries`
Expected: PASS (the guard).

- [ ] **Step 5: Implement the view DTO**

In `internal/view/prune.go`, add the entry DTO next to `pruneDetailJSON`:

```go
// pruneStorageEntryJSON is one runtime-memory backing's prune outcome. The byte
// counts are pointers so an unmeasured count marshals as null, never 0.
type pruneStorageEntryJSON struct {
	Path           string  `json:"path"`
	Kind           string  `json:"kind"`
	LogicalBytes   *uint64 `json:"logical_bytes"`
	AllocatedBytes *uint64 `json:"allocated_bytes"`
	State          string  `json:"state"`
	Error          *string `json:"error"`
}
```

Add to `PruneReportJSON` (after `RuntimePhysicalBytesReclaimed`):

```go
	RuntimeMemoryEntries          []pruneStorageEntryJSON `json:"runtime_memory_entries"`
	RuntimeTruncated              bool                    `json:"runtime_truncated"`
```

In `NewPruneReportJSON` set `RuntimeMemoryEntries: newPruneStorageEntriesJSON(report.Storage)` and `RuntimeTruncated: report.Storage.Truncated`, and add:

```go
// newPruneStorageEntriesJSON maps the runtime-memory phase's per-backing
// outcomes into a non-nil array so an empty listing marshals as [] not null. It
// ranges over the storage report (not msb.PruneEntry directly), keeping view
// free of the msb package.
func newPruneStorageEntriesJSON(storage pruning.StorageReport) []pruneStorageEntryJSON {
	entries := make([]pruneStorageEntryJSON, 0, len(storage.Entries))
	for _, entry := range storage.Entries {
		entries = append(entries, pruneStorageEntryJSON{
			Path:           entry.Path,
			Kind:           entry.Kind,
			LogicalBytes:   entry.LogicalBytes,
			AllocatedBytes: entry.AllocatedBytes,
			State:          entry.State,
			Error:          entry.Error,
		})
	}
	return entries
}
```

- [ ] **Step 6: Implement the explicit command's human render**

In `cmd/agents-sandbox/prune_cli.go`, add the call inside `runPruneBatch`'s human closure, after `pruning.RenderPruneSummary(ui, report, dryRun)` and before `view.RenderRetiredProjects(ui, retired)`:

```go
				renderPruneStorageOutcomes(ui, report.Storage)
```

Then add:

```go
// renderPruneStorageOutcomes reports the runtime-memory phase's per-backing
// failures and its truncated listing. It belongs to the explicit prune's human
// render, not the shared summary AutoPrune also prints, so an ordinary launch
// stays terse.
func renderPruneStorageOutcomes(ui termio.UI, report pruning.StorageReport) {
	for _, entry := range report.Entries {
		if entry.Error != nil {
			ui.Warnf("runtime memory: failed to prune %s (%s): %s", entry.Path, entry.Kind, *entry.Error)
		}
	}
	if report.Truncated {
		ui.Warnf("runtime memory: listing truncated; additional eligible backings were not reported")
	}
}
```

- [ ] **Step 7: Add the changelog fragment**

Create `changelog.d/<PR-number>.fixed.md` (use the implementation PR's number):

```markdown
Report the runtime-memory prune's per-backing outcomes, so a `prune` in which some
backings could not be reclaimed is no longer presented as a clean, complete prune.
The explicit `prune` command now names each failed backing and notes a truncated
listing, `--format json` carries the entries, and a per-entry failure joins the
command's error.
```

- [ ] **Step 8: Run the tests and the lint**

Run: `go test ./internal/view/ ./internal/sandbox/pruning/ ./cmd/agents-sandbox/` → PASS.
Run: `make lint` → clean.

- [ ] **Step 9: Commit**

```bash
git add internal/view/prune.go internal/view/prune_test.go cmd/agents-sandbox/prune_cli.go cmd/agents-sandbox/cli_prune_test.go internal/sandbox/pruning/prune_test.go changelog.d/
git commit -m "feat(prune): surface runtime-memory prune entries and partial failures"
```

---

### Task 5: Record the contract in the spec and guide, then run the full gate

**Files:**
- Modify: `.operator-shared/specs/volumes/runtime-management-commands.md`
- Modify: `.operator-shared/guides/msb-sdk-integration.md` (§1.5)

**Interfaces:**
- Consumes: the behavior recorded in Tasks 1-4 (the chosen JSON keys, the warn-and-join rule, the terse auto-prune).
- Produces: nothing; the durable contract.

- [ ] **Step 1: Update the `prune` report contract in the spec**

In `.operator-shared/specs/volumes/runtime-management-commands.md`, extend the **"Prune report"** bullet (the JSON-shape contract) with the runtime-memory phase's detail: `--format json` carries `runtime_memory_entries` (each with `path`/`kind`/`logical_bytes`/`allocated_bytes`/`state`/`error`, an unmeasured byte count as `null`) and `runtime_truncated`; the explicit `prune` human output warns for each backing whose `error` is set and notes a truncated listing; and a per-entry error joins the aggregate prune error (warn-and-join) while the report is still rendered first. Leave the "Runtime-RAM pruning enriches…" paragraph as the phase's existence/threshold statement, and state there (or in the bullet) that the shared summary `AutoPrune` prints stays totals-only.

- [ ] **Step 2: Fold the adopted prune and the deferred diagnosis/recovery into the guide**

In `.operator-shared/guides/msb-sdk-integration.md` §1.5, replace the "the launcher's `PruneReport` drops" clause with the adopted state: `PruneStorage`'s per-object outcomes and `Truncated` are now carried by `PruneReport` and surfaced by the explicit `prune` command. Record `DiagnoseStorage` and `RecoverStorage` as deferred non-adoptions with their reasons (no hosting surface; `RecoverStorage` selects a disk view and mutates storage, so it would be a second recovery policy) — matching the design's "Recorded decisions".

- [ ] **Step 3: Run the full gate**

Run: `make check`
Expected: every target reports PASS (`validate-fmt`, `lint`, `test`, the doc/script gates, `brain-check`). If `validate-fmt` reports unformatted files, run `make fmt` and re-run.

- [ ] **Step 4: Verify manually on a KVM host (no automated expectation)**

The `msb`-tagged suite does not exercise `PruneStorage`'s entries, so this is a manual, host-only check (the dev VM cannot start VMs). On a KVM host with a home holding eligible branch memory, run the `msb`-tagged suite (`make test-msb`, or `CGO_ENABLED=1 go test -tags msb ./internal/sandbox/msb/`) to confirm nothing regressed, then run an explicit `prune --yes` against that home and confirm the entry outcomes appear (a failed backing warns; the summary renders before any error surfaces). No test asserts this; record the observation for the PR.

- [ ] **Step 5: Commit**

```bash
git add .operator-shared/specs/volumes/runtime-management-commands.md .operator-shared/guides/msb-sdk-integration.md
git commit -m "docs(prune): record the richer runtime-memory prune report contract"
```

---

## Out of scope

- `DiagnoseStorage` and `RecoverStorage` — deferred (the guide's §1.5 records the reasons).
- A `storage diagnose`/`recover` command, a `doctor` storage check, or a VM-start failure hint.
- Changing the prune's reclamation rules, the `--older-than` semantics, or the `PruneStorage` interface signature.
- Any other SDK capability work (metrics streaming, snapshots, filesystem transfer, pause/resume) — issues #915-#922.
