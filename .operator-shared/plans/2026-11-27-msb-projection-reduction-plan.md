# msb projection reduction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce the `internal/sandbox/msb` value-projection layer and its reflection drift register by aliasing the full-mirror pure-data observation types to the SDK and scoping the register to launcher-owned mirrors.

**Architecture:** Every aliased launcher type becomes `type LauncherName = msbSdk.SDKName`, so consumers keep the same names and no call site changes; the launcher struct body, its `XFrom` projector, and its register pair are deleted. SDK structs the adapter only reads internally lose their register pair too. Behavior-bearing types (inputs, enum-renaming, narrowed, method-carrying, normalized read-back) stay launcher-owned.

**Tech Stack:** Go 1.27, module `github.com/inoio/agents-sandbox`, microsandbox SDK v0.8.3, golangci-lint (incl. `exhaustruct_v5`, `deadcode`).

**Spec:** `.operator-shared/plans/2026-11-27-msb-projection-reduction-design.md`

## Global Constraints

- Base branch is `chore/906-msb-sdk-v0.8` (SDK v0.8.3); this work sits on top of the v0.8 migration.
- `phase4_boundary_test.go` must stay green: **no** production (non-`_test.go`) package outside `internal/sandbox/msb` may import `msbSdk`. Aliases satisfy this because they are declared inside package `msb`.
- `roles_boundary_test.go` must stay green; do not touch the roles/composites.
- Only full-mirror, pure-data SDK structs with **no enum fields and no methods** may be aliased. Do not alias `JobInfo`, `JobLogEntry`, `LogEntry`, `SandboxModificationPlan`, `PruneReport`, `DiskCompactionResult`, or any input type.
- No consumer call-site or signature churn: aliased type names, field names, and field types are unchanged.
- Format with `make fmt` (never `go fmt`); lint with `make lint`; run `make check` before finalizing.
- Every alias needs a `changelog.d/<PR-number>.<type>.md` entry only at the end (Task 7).

## Review Focus

Each line names where it is pinned; a green run of the named test is the check.

- **Nil SDK observation pointer** reaching a seam method (`Metrics`, `ImageInspect`): a caller expects `(nil, nil)`. Pinned by the existing `TestRealMsbClientImageInspectDetailNilConfig` and the unchanged passthrough call sites in Tasks 1/3 — if a task makes a returned SDK pointer nilable, its adapter test must still pass.
- **Partial production literal of an aliased type**: `exhaustruct_v5` now evaluates the SDK-typed literal. Pinned by `make lint` in every alias task; there is no production partial literal today, so a clean lint is the evidence.
- **`ImageDetail.Config` value-vs-pointer**: the new code must guard `detail.Config != nil` before `*detail.Config`. Pinned by `TestRealMsbClientImageInspectDetailInjected` and `TestRealMsbClientImageInspectDetailNilConfig` (Task 1).
- **Nested aliased fields** (`JobInfo.Failure`, `SandboxModificationPlan.Conflicts`, `DiskCompactionResult.Disks`): constructing or reading them must compile with no conversion. Pinned by `alias_contract_test.go` (Tasks 1-5) and the retained `TestModificationPlanFrom*` / `TestDiskCompactionResultFrom*`.
- **Enum-bearing seam values stay launcher-owned** (`LogEntry.Source`, `JobInfo.State`, `JobLogEntry.Source`): the contract test must not list them. Pinned by the contract test listing exactly the 10 observation names plus the retained enum tests (`TestSandboxStatusFromTranslatesValues`, `TestBackendKindFromTranslatesValues`, `TestVolumeKindFromTranslatesValues`).

---

### Task 1: Alias the image observation triple

**Files:**
- Modify: `internal/sandbox/msb/image_values.go` (all three types become aliases)
- Modify: `internal/sandbox/msb/client.go:294-321` (`ImageInspect`, `ImageInspectDetail`)
- Modify: `internal/sandbox/msb/translate.go` (delete `imageConfigFrom`, `imageLayersFrom`, `imagePruneReportFrom`; delete register maps at lines 230-270)
- Modify: `internal/sandbox/msb/translate_test.go` (table entries `ImageConfig`, `ImageLayer`, `ImagePruneReport`; delete the five projector tests)
- Create: `internal/sandbox/msb/alias_contract_test.go`

**Interfaces:**
- Produces: `type ImageConfig = msbSdk.ImageConfig`, `type ImageLayer = msbSdk.ImageLayer`, `type ImagePruneReport = msbSdk.ImagePruneReport`.
- `ImageStore.ImageInspect(ctx) (*ImageConfig, error)`, `ImageInspectDetail(ctx) (*ImageDetail, error)`, `ImagePrune(ctx) (*ImagePruneReport, error)` keep their signatures.

- [ ] **Step 1: Write the failing contract test**

Create `internal/sandbox/msb/alias_contract_test.go`:

```go
package msb

import msbSdk "github.com/superradcompany/microsandbox/sdk/go"

// Two-way direct assignment (no conversion) compiles only while the launcher
// name is an alias of the SDK type. A defined type fails to build here.
var (
	_ ImageConfig        = msbSdk.ImageConfig{}
	_ msbSdk.ImageConfig = ImageConfig{}
	_ ImageLayer         = msbSdk.ImageLayer{}
	_ msbSdk.ImageLayer  = ImageLayer{}
	_ ImagePruneReport        = msbSdk.ImagePruneReport{}
	_ msbSdk.ImagePruneReport = ImagePruneReport{}
)
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/sandbox/msb/`
Expected: build failure, `cannot use msbSdk.ImageConfig{} ... as ImageConfig value` (the launcher type is still a defined struct).

- [ ] **Step 3: Replace the structs with aliases**

In `internal/sandbox/msb/image_values.go`, replace the three struct declarations with:

```go
type ImageConfig = msbSdk.ImageConfig

type ImageLayer = msbSdk.ImageLayer

type ImagePruneReport = msbSdk.ImagePruneReport
```

Add the import `msbSdk "github.com/superradcompany/microsandbox/sdk/go"`. Keep one concise contract comment on the file, e.g. "These are SDK observations the launcher surfaces directly; they add no behavior. See specs/architecture/msb-seam.md."

- [ ] **Step 4: Update the adapter call sites**

In `client.go`:
- `ImageInspect`: `return detail.Config, nil` (drop `imageConfigFrom`).
- `ImageInspectDetail`: `out := &ImageDetail{Layers: detail.Layers}`; replace `if config := imageConfigFrom(detail.Config); config != nil { out.Config = *config }` with `if detail.Config != nil { out.Config = *detail.Config }`.
- `ImagePrune`: `return report, nil`.

- [ ] **Step 5: Delete the projectors and register maps**

Delete `imageConfigFrom`, `imageLayersFrom`, `imagePruneReportFrom` from `translate.go`. Delete the `imageConfigProjected`/`imageConfigNotModeled`, `imagePruneReportProjected`/`imagePruneReportNotModeled`, `imageLayerProjected`/`imageLayerNotModeled` map pairs (lines ~230-270). Remove the table entries `"ImageConfig"`, `"ImageLayer"`, `"ImagePruneReport"` in `translate_test.go`.

- [ ] **Step 6: Delete the obsolete projector tests**

Delete from `translate_test.go`: `TestImageConfigFromProjectsFields`, `TestImageConfigFromNilIsNil`, `TestImageLayersFromPreservesNilAndFields`, `TestImagePruneReportFromProjectsFields`, `TestImagePruneReportFromNilIsNil`.

- [ ] **Step 7: Run the tests and the lint**

Run: `go test ./internal/sandbox/msb/` → PASS.
Run: `make lint` → clean (watch for `exhaustruct_v5` and `deadcode`).

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "refactor(msb): alias the image observation types to the SDK"
```

---

### Task 2: Alias the storage observation triple

**Files:**
- Modify: `internal/sandbox/msb/storage_values.go` (`StorageReport`, `StorageCategory`, `StorageItem` become aliases; keep `PruneRequest`, `PruneReport`)
- Modify: `internal/sandbox/msb/client.go:321-330` (`StorageUsage`)
- Modify: `internal/sandbox/msb/translate.go` (delete `storageReportFrom`, `storageCategoryFrom`, `storageItemsFrom`, `storageItemFrom`; register maps at lines 96-138)
- Modify: `internal/sandbox/msb/translate_test.go`
- Modify: `internal/sandbox/msb/alias_contract_test.go`

**Interfaces:**
- Consumes: Task 1's alias pattern.
- Produces: `type StorageReport = msbSdk.StorageUsageReport`, `type StorageCategory = msbSdk.StorageCategoryUsage`, `type StorageItem = msbSdk.StorageItemUsage`.
- `StorageStore.Usage(ctx) (*StorageReport, error)` unchanged. `PruneReport` stays launcher-owned.

- [ ] **Step 1: Extend the failing contract test**

Append to `alias_contract_test.go`:

```go
var (
	_ StorageReport         = msbSdk.StorageUsageReport{}
	_ msbSdk.StorageUsageReport = StorageReport{}
	_ StorageCategory         = msbSdk.StorageCategoryUsage{}
	_ msbSdk.StorageCategoryUsage = StorageCategory{}
	_ StorageItem         = msbSdk.StorageItemUsage{}
	_ msbSdk.StorageItemUsage = StorageItem{}
)
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/sandbox/msb/`
Expected: build failure on the new `StorageReport`/`StorageCategory`/`StorageItem` assignments.

- [ ] **Step 3: Replace the structs with aliases**

In `storage_values.go`: replace the three struct bodies with `type StorageReport = msbSdk.StorageUsageReport`, `type StorageCategory = msbSdk.StorageCategoryUsage`, `type StorageItem = msbSdk.StorageItemUsage`; add the `msbSdk` import. Keep `PruneRequest` and `PruneReport`.

- [ ] **Step 4: Update the adapter call site**

In `client.go` `StorageUsage`: `return report, nil`.

- [ ] **Step 5: Delete the projectors and register maps**

Delete `storageReportFrom`, `storageCategoryFrom`, `storageItemsFrom`, `storageItemFrom` from `translate.go` (they chained through each other). Delete the `storageUsageReport*`, `storageCategory*`, `storageItem*` map pairs (lines ~96-138). Remove the table entries `"StorageUsageReport"`, `"StorageCategoryUsage"`, `"StorageItemUsage"` in `translate_test.go`.

- [ ] **Step 6: Delete the obsolete projector tests**

Delete `TestStorageReportFromProjectsFields`, `TestStorageReportFromNilIsNil`, `TestStorageCategoryFromPreservesNilCollections`. Keep `TestPruneReportFromProjectsTotals` and `TestPruneReportFromNilIsNil`.

- [ ] **Step 7: Run the tests and the lint**

Run: `go test ./internal/sandbox/msb/` → PASS. Run: `make lint` → clean.

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "refactor(msb): alias the storage observation types to the SDK"
```

---

### Task 3: Alias Metrics

**Files:**
- Modify: `internal/sandbox/msb/observation_values.go` (keep `JobInfo`, `JobLogEntry`, `JobLogQuery`, `LogEntry`; alias `Metrics`)
- Modify: `internal/sandbox/msb/sandbox.go:319-325` and `:498-503` (both `Metrics` methods)
- Modify: `internal/sandbox/msb/translate.go` (delete `metricsFrom`; register maps at lines 209-229)
- Modify: `internal/sandbox/msb/translate_test.go`, `internal/sandbox/msb/alias_contract_test.go`

**Interfaces:**
- Produces: `type Metrics = msbSdk.Metrics`; `SandboxObserver.Metrics(ctx) (*Metrics, error)` unchanged.

- [ ] **Step 1: Extend the failing contract test**

Append to `alias_contract_test.go`:

```go
var (
	_ Metrics         = msbSdk.Metrics{}
	_ msbSdk.Metrics  = Metrics{}
)
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/sandbox/msb/` → build failure on the `Metrics` assignment.

- [ ] **Step 3: Replace the struct with an alias**

In `observation_values.go`: replace the `Metrics` struct with `type Metrics = msbSdk.Metrics`; add the `msbSdk` import.

- [ ] **Step 4: Update the adapter call sites**

In `sandbox.go`, both `Metrics` methods: `return metrics, nil`.

- [ ] **Step 5: Delete the projector, register maps, test**

Delete `metricsFrom` and the `metricsProjected`/`metricsNotModeled` pair. Remove the `"Metrics"` table entry and `TestMetricsFromProjectsFieldsAndNil`.

- [ ] **Step 6: Run the tests and the lint**

Run: `go test ./internal/sandbox/msb/` → PASS. Run: `make lint` → clean.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "refactor(msb): alias the metrics observation to the SDK"
```

---

### Task 4: Alias ExecFailure

**Files:**
- Modify: `internal/sandbox/msb/observation_values.go` (alias `ExecFailure`; `JobInfo` stays)
- Modify: `internal/sandbox/msb/translate.go` (delete `execFailureFrom`; `jobInfoFrom` field; register maps at lines 185-196)
- Modify: `internal/sandbox/msb/translate_test.go`, `internal/sandbox/msb/alias_contract_test.go`

**Interfaces:**
- Produces: `type ExecFailure = msbSdk.ExecFailure`; `JobInfo.Failure` becomes field type `*ExecFailure` structurally identical to `*msbSdk.ExecFailure`.
- Consumes: nothing from other tasks beyond the pattern.

- [ ] **Step 1: Extend the failing contract test**

Append to `alias_contract_test.go`:

```go
var (
	_ ExecFailure        = msbSdk.ExecFailure{}
	_ msbSdk.ExecFailure = ExecFailure{}
)
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/sandbox/msb/` → build failure on the `ExecFailure` assignment.

- [ ] **Step 3: Replace the struct with an alias and drop the projector**

In `observation_values.go`: replace the `ExecFailure` struct with `type ExecFailure = msbSdk.ExecFailure`. In `translate.go`, change `jobInfoFrom`'s `Failure: execFailureFrom(info.Failure)` to `Failure: info.Failure`, and delete `execFailureFrom`.

- [ ] **Step 4: Delete the register maps and table entry**

Delete `execFailureProjected`/`execFailureNotModeled` and the `"ExecFailure"` table entry.

- [ ] **Step 5: Run the tests and the lint**

Run: `go test ./internal/sandbox/msb/` → PASS (there is no dedicated `execFailure` test to delete; it is covered through `jobInfoFrom`). Run: `make lint` → clean.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "refactor(msb): alias the exec-failure observation to the SDK"
```

---

### Task 5: Alias the compaction-disk and modification-conflict observations

**Files:**
- Modify: `internal/sandbox/msb/config_values.go` (alias `DiskCompactionDiskResult` and `ModificationConflict`; keep `SandboxModificationPlan`, `DiskCompactionResult`)
- Modify: `internal/sandbox/msb/translate.go` (delete `diskCompactionDisksFrom` and `modificationConflictsFrom`; fix `diskCompactionResultFrom`, `modificationPlanFrom`; register maps at lines 407-418)
- Modify: `internal/sandbox/msb/translate_test.go`, `internal/sandbox/msb/alias_contract_test.go`

**Interfaces:**
- Produces: `type DiskCompactionDiskResult = msbSdk.DiskCompactionDiskResult`, `type ModificationConflict = msbSdk.ModificationConflict`.
- `DiskCompactionResult.Disks` becomes `[]msbSdk.DiskCompactionDiskResult`; `SandboxModificationPlan.Conflicts` becomes `[]msbSdk.ModificationConflict`.

- [ ] **Step 1: Extend the failing contract test**

Append to `alias_contract_test.go`:

```go
var (
	_ DiskCompactionDiskResult        = msbSdk.DiskCompactionDiskResult{}
	_ msbSdk.DiskCompactionDiskResult = DiskCompactionDiskResult{}
	_ ModificationConflict        = msbSdk.ModificationConflict{}
	_ msbSdk.ModificationConflict = ModificationConflict{}
)
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/sandbox/msb/` → build failure on both assignments.

Before Step 3, confirm `msbSdk.ModificationConflict` is a full mirror with no enum field or method (`go doc github.com/superradcompany/microsandbox/sdk/go.ModificationConflict`). If it carries an enum or method, keep it launcher-owned and drop it from this task.

- [ ] **Step 3: Replace the structs with aliases and drop the projectors**

In `config_values.go`: replace the two structs with the aliases. In `translate.go`: `diskCompactionResultFrom` sets `Disks: result.Disks`; `modificationPlanFrom` sets `Conflicts: plan.Conflicts`; delete `diskCompactionDisksFrom` and `modificationConflictsFrom`. Delete the `diskCompactionDiskResultProjected`/`NotModeled` pair (lines 407-418) and the `"DiskCompactionDiskResult"` table entry. `ModificationConflict` has no register pair.

- [ ] **Step 4: Delete the obsolete projector test**

Delete `TestDiskCompactionDisksFromPreservesNil`. Keep `TestDiskCompactionResultFromProjectsFields`, `TestDiskCompactionResultFromNilIsNil`, `TestModificationPlanFromProjectsConflicts`, `TestModificationPlanFromNilIsNil` (they exercise the still-launcher-owned types).

- [ ] **Step 5: Run the tests and the lint**

Run: `go test ./internal/sandbox/msb/` → PASS. Run: `make lint` → clean.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "refactor(msb): alias the compaction-disk and conflict observations to the SDK"
```

---

### Task 6: Drop the internal-read register pairs

**Files:**
- Modify: `internal/sandbox/msb/translate.go` (delete register maps at lines 420-509)
- Modify: `internal/sandbox/msb/translate_test.go` (remove five table entries)

**Interfaces:**
- No type or signature changes. The read code (`sandboxConfigFrom`, `decodeInspectionRootfs`, handle `Info` reads) is untouched; only its classification maps go, because a new field on an internally-read SDK struct needs no decision.

- [ ] **Step 1: Remove the five table entries**

In `translate_test.go`, remove the entries `"SandboxConfigInspection"`, `"SandboxInspectionSpec"`, `"SandboxResourceInspection"`, `"VolumeInfo"`, `"SandboxInfo"`. The package still builds (package-level unused vars are legal), so the signal is the linter.

- [ ] **Step 2: Run the linter to verify it fails**

Run: `make lint`
Expected: FAIL — the ten now-unreferenced `*Projected`/`*NotModeled` map vars are reported unused/dead.

- [ ] **Step 3: Delete the register maps**

Delete `sandboxConfigInspectionProjected`/`NotModeled`, `sandboxInspectionSpecProjected`/`NotModeled`, `sandboxResourceInspectionProjected`/`NotModeled`, `volumeInfoProjected`/`NotModeled`, `sandboxInfoProjected`/`NotModeled` (translate.go lines ~420-509). If the enclosing `var (...)` block loses its `//nolint:goconst` reason, remove that directive.

- [ ] **Step 4: Run the tests and the lint to verify they pass**

Run: `go test ./internal/sandbox/msb/` → PASS (the 15 surviving table entries remain). Run: `make lint` → clean, no unused vars, `deadcode` clean.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "refactor(msb): drop the drift register for internally-read SDK structs"
```

---

### Task 7: Update the seam spec and the changelog

**Files:**
- Modify: `.operator-shared/specs/architecture/msb-seam.md`
- Create: `changelog.d/<PR-number>.changed.md`

**Interfaces:** Documentation only.

- [ ] **Step 1: Revise the spec's currency rule**

In `msb-seam.md` "Launcher-owned types → Values out", replace the blanket prohibition with the rule: a launcher-owned type wherever the port adds behavior (inputs; types the launcher branches on; enum-renaming, narrowed, or method-carrying types; normalized read-back), and an **alias** (`type X = msbSdk.X`) for a full-mirror, pure-data observation with no enum field and no method. Name the alias set. In "Upstream sync guards", restate guard 2 as applying to every SDK struct a launcher type **mirrors**; the compiler is the sole guard for aliases and internal reads.

- [ ] **Step 2: Add the changelog fragment**

Create `changelog.d/<PR-number>.changed.md` describing the internal reduction (no user-visible behavior change).

- [ ] **Step 3: Run the full gate**

Run: `make check` → all targets PASS (including `check-docs` and `brain-check`).

- [ ] **Step 4: Commit**

```bash
git add .operator-shared/specs/architecture/msb-seam.md changelog.d/
git commit -m "docs(msb): revise the seam currency rule for observation aliases"
```

---

## Out of scope (per the spec)

- Issue #924 item 2 (duplicate factories) is a **no-op** on this branch; item 4 (test seam) is stale. Do not act on them.
- Item 3 (role split) and item 5 (status classification) are optional and, if done, belong in a separate change.
- No SDK capability work (#915-#922), no context/collection adoption (#923).
