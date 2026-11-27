---
description: Design for issue #924 - reduce the msb adapter's value-projection layer and its reflection drift register by making the seam's currency a stated rule (behavior-bearing vs observation), using type aliases for full-mirror pure-data observations and scoping the register to launcher-owned mirrors.
read_if: Implementing #924 (make the msb port thinner); touching internal/sandbox/msb/translate.go, its projection registers, the launcher value types, or the drift test; revising specs/architecture/msb-seam.md's "Launcher-owned types" or "Upstream sync guards" sections.
---

# msb adapter: reduce the value-projection layer and the drift register

Status: proposed, revised after builder review. Issue #924. Base: branch `chore/906-msb-sdk-v0.8`
(SDK v0.8.3), on top of the v0.8 migration; the migration must land first. Supersedes the
"value-projection" half of `guides/msb-sdk-integration.md` §3.

## Intent

The `internal/sandbox/msb` port is the launcher's abstract boundary over the microsandbox SDK. It
exists because there are two adapters behind it (the real SDK client and the hand-written mock) and
because the SDK has identity/no-op edge cases the port normalizes. It is **not** meant to mirror the
SDK, yet ~20 of `translate.go`'s 51 functions are field-for-field `XFrom` projectors, backed by
launcher struct declarations and a ~490-line reflection drift register whose sole job is to force a
decision on every field the SDK adds.

That tax is recurring. The v0.8 migration **grew** the layer: it added five register pairs
(`sandboxConfigInspection`, `sandboxInspectionSpec`, `sandboxResourceInspection`, `volumeInfo`,
`sandboxInfo`) plus raw-JSON decode helpers. Three of those five are pure observations the launcher
never branches on. This change restates the boundary so only fields the port *uses* require a
decision, and removes the mirroring that carries no policy.

## Goals

- Cut the projection layer to the types the port adds behavior to, and make the register shrink to
  that set and stop growing with SDK observation churn.
- Keep the port's purpose: consumers still speak launcher type names, the SDK stays an implementation
  detail of `internal/sandbox/msb` alone, normalized behaviors survive, and enum distinctness
  (`SandboxStatus`/`BackendKind`/`VolumeKind`, `LogSource`) is preserved.
- **No consumer call-site churn.** This is a hard constraint of the chosen mechanism, not a hope.
- Keep the two upstream-sync guards that still earn their keep (the compiler for all types; the
  register for the launcher-owned mirrors).

## Non-goals

- Any SDK capability work (warm start, pause/resume, metrics streaming, storage diagnosis, filesystem
  widening, create-time knobs, SSH/agent). Those are issues #915-#922.
- The explicit-context/collection adoption and the captured-observation contract (#923).
- Changing the SDK pin or crossing an SDK line.

## Anti-goals

- A "compiler-only" end state where SDK types are the seam's currency everywhere (Option 3 below):
  it deletes the port's reason to exist and makes every SDK rename a repo-wide break.
- A cosmetic trim that leaves the register growing at the same rate (Option 1).
- A whole-struct alias that drags an SDK enum or a method onto the seam, or widens a deliberately
  narrowed projection. That trades the invariant away for ~60 lines and breaks "no consumer churn".

## Current state (measured on the migration branch)

- `internal/sandbox/msb` production code: **~3,760 lines**. `translate.go`: **1,417 lines**, 51
  functions, 20 `XFrom`/`*From` projectors.
- Projection register: `translate.go` lines ~20-509, **~490 lines**, **29 register pairs** over 29 SDK
  structs (each a `projected` set and a `notModeled` reason map).
- Boundary: `phase4_boundary_test.go` fails if **any** non-`msb` production package imports `msbSdk`;
  **66** packages import `msb`.
- Drift test: `TestSDKStructFieldsAreClassified` in `translate_test.go` (~138 lines of harness).
- The SDK expresses its own surface with aliases (`type JobInfo = core.JobInfo`), and the SDK is
  itself the consumer of those aliases.

## Approaches considered

All three keep launcher type **names** as the seam's currency; they differ in how the launcher type
relates to the SDK type.

### Option 1 — Shrink-only (keep every launcher struct)

Trim each projector and launcher struct to the fields consumers read, and prune the register to
match. Removes ~60-120 production lines; ~450 register lines remain and **resume growing** on the next
bump (the v0.8 migration is the evidence). Lowest risk, unchanged trajectory. Rejected.

### Option 2 — A stated currency rule: alias the pure observations, scope the register (chosen)

Restate the boundary as a rule:

> The seam's currency is a launcher-owned type wherever the port **adds behavior** — inputs, any
> returned type the launcher branches on or whose edge cases the port normalizes, and anything
> deliberately narrowed or enum-bearing. A **full-mirror, pure-data observation** with no enum fields
> and no methods may be an **alias** to the SDK type.

Two halves:

1. **Alias the qualifying observation return types** (`type Metrics = msbSdk.Metrics`). Consumers keep
   writing `msb.Metrics`; the seam alias-declares against the SDK, so `phase4_boundary_test.go` stays
   green and **no consumer changes**. The launcher struct body and its projector disappear; the
   compiler guards field renames at the consumer's read site; a new SDK field is silently ignorable,
   which is correct for an observation.
2. **Scope the register to launcher-owned mirrors.** A register pair is meaningful only when a launcher
   struct re-declares an SDK struct's fields. For an SDK struct the adapter merely *reads* (captured
   `VolumeInfo`/`SandboxInfo`, the inspection envelope), a new upstream field needs no decision, so the
   pair is pure tax: drop it and let the compiler catch renames in the read code.

Removes ~500-560 production lines; the register drops from 29 pairs to **15** and stops growing on the
pure-data observation set, which is the churning set. No boundary change, no consumer churn, no
invariant broken — it is sharpened.

### Option 3 — Compiler migration (SDK types as currency everywhere)

Delete every launcher value type and translator, drop the register, let SDK types flow through all 66
consumers. Removes ~2,200-2,500 production lines, but makes every SDK rename a repo-wide break and
prevents the port from normalizing an upstream defect (the untyped mount/port decoder must still exist
somewhere). Rejected.

**Codegen** (generate the projections and drop the reflection register via an `x/tools/go/packages`
scan) was also considered: it preserves launcher types without the register, but `msb-seam.md` already
rejected the `packages` dependency as against `AGENTS.md`, and a build-time generator is
disproportionate for one module. Dropped.

**Pick: Option 2 (strict variant).**

## The design

### The rule

Classify every SDK struct the port touches, once:

- **Behavior-bearing** → launcher-owned type, hand-written projector, register-guarded. This covers:
  inputs/options; any type the launcher branches on; any type whose field types the launcher
  **renames** (e.g. an SDK enum to a launcher string); any type the launcher **deliberately narrows**
  (drops exported SDK fields); any type carrying methods.
- **Observation** → `type LauncherName = msbSdk.SDKName`. Full-mirror, pure-data, no enum fields, no
  methods. Bare alias, no projector, no register.
- **Internal read** → an SDK struct captured and read only inside the adapter, never returned to a
  consumer. No launcher type, no register; the compiler guards the read.

A type moves classes only by an explicit decision recorded in `msb-seam.md`.

### Alias set (10 types)

Delete the launcher struct body and its projector; replace with a one-line alias. Each is a verified
full mirror (`notModeled` empty) with pure-data fields and no methods.

| Launcher type (deleted) | Alias target | Projector deleted |
|---|---|---|
| `ExecFailure` | `msbSdk.ExecFailure` | `execFailureFrom` |
| `Metrics` | `msbSdk.Metrics` | `metricsFrom` |
| `ImageConfig` | `msbSdk.ImageConfig` | `imageConfigFrom` |
| `ImageLayer` | `msbSdk.ImageLayer` | `imageLayersFrom` |
| `ImagePruneReport` | `msbSdk.ImagePruneReport` | `imagePruneReportFrom` |
| `StorageReport` | `msbSdk.StorageUsageReport` | `storageReportFrom` |
| `StorageCategory` | `msbSdk.StorageCategoryUsage` | `storageCategoryFrom` |
| `StorageItem` | `msbSdk.StorageItemUsage` | `storageItemsFrom`, `storageItemFrom` |
| `DiskCompactionDiskResult` | `msbSdk.DiskCompactionDiskResult` | `diskCompactionDisksFrom` |
| `ModificationConflict` | `msbSdk.ModificationConflict` | `modificationConflictsFrom` |

Slice projectors over these collapse to the SDK slice; the seam signatures become
`[]msb.Metrics` = `[]msbSdk.Metrics`.

### Register pairs dropped (5, internal reads)

`msbSdk.VolumeInfo` and `msbSdk.SandboxInfo` (captured on the handles, read by identity/config
methods), `msbSdk.SandboxConfigInspection`, `msbSdk.SandboxInspectionSpec`,
`msbSdk.SandboxResourceInspection` (read by `sandboxConfigFrom` and the rootfs decode). The read code
stays; the classification maps go. `sandboxConfigFrom` remains launcher-owned because it normalizes
the persisted-config shape (mounts/ports decode, `RootfsSource` → image + root-disk size).

### Kept launcher-owned (with their register pairs)

- **Input mirrors:** `ExecConfig`, `AttachConfig`, `LogOptions`, `DiskCompactionOptions`,
  `StoragePruneOptions`, `JobLogOptions`, `ModifyOptions`, `SandboxConfig`, `VolumeConfig`.
- **Enum-renaming observations:** `JobInfo` (`State` SDK `JobState` → `string`), `JobLogEntry`
  (`Source` SDK `JobOutputSource` → `string`), `LogEntry` (`Source` SDK `LogSource` → launcher
  `LogSource`; also carries the SDK's `Text()` method — keeping it launcher-owned avoids importing the
  method onto the seam).
- **Deliberately narrowed:** `SandboxModificationPlan` (surfaces only `Conflicts`), `PruneReport`
  (drops `DryRun`/`Entries`/`Truncated`), `DiskCompactionResult` (drops `DryRun`).
- **Normalized read-back:** `MountConfig`/`MountOwner`/`PortBinding` and the
  `configmounts.go`/`configports.go` decoders (hide an upstream `UnmarshalJSON` defect).
- **Lifecycle enums:** `SandboxStatus`/`BackendKind`/`VolumeKind` and the request `LogSource`.

### Spec revisions

`specs/architecture/msb-seam.md`:

- "Launcher-owned types → Values out": replace the blanket prohibition with the rule, and name the
  alias as the sanctioned observation representation, including the full-mirror/pure-data/enum-free
  qualification.
- "Upstream sync guards": restate guard 2 as "for every SDK struct a launcher type **mirrors**"; the
  compiler is the sole guard for aliases and internal reads.
- Status: add a `#924` entry.

## Items 2-5 of #924

- **Item 2 — duplicate factories: no-op.** `Mount.Bind/Named/Tmpfs` build the launcher `MountConfig`
  directly with no SDK round-trip (`mount_values.go:109-149`); the launcher→SDK translation lives in
  `mountConfig()` (`translate.go:1234`). The only `Secret.Env` is the launcher's, which
  `envsecret/secrets.go:61` already calls; `secretEntry()` is the launcher→SDK translation. The guide's
  §3.2 describes pre-migration code. Removing the launcher factories would push `msbSdk.MountConfig`
  into consumers and break the boundary. Do nothing; note the stale premise in the PR.
- **Item 3 — the role split.** The guide judges the `roles_boundary_test.go` invariant still outweighs
  the narrowing win. Leave the composites; narrow only clearly single-consumer roles if it stays
  cheap, with the boundary test as arbiter. 0 to ~120 lines. May be split out.
- **Item 4 — the test seam surface: stale.** The v0.8 branch has 3 package-level function-pointer vars
  and 0 func-typed struct fields, not "dozens". ~0 lines.
- **Item 5 — status classification.** `GetVMStatus`'s fail-closed unknown branch can collapse behind
  the launcher `SandboxStatus` enum. ~30-40 lines, only if no SDK type crosses the seam. Optional.

## Testing

- Delete one projector test per aliased type (10 types); the alias needs no field assertion.
- Shrink `TestSDKStructFieldsAreClassified` to the 15 surviving register pairs.
- Keep `roles_boundary_test.go` and `phase4_boundary_test.go` untouched — the latter is the proof that
  aliases do not leak the SDK to consumers.
- Add a rule test: pin the alias set as exactly the observation set (identity checks
  `msb.Metrics == msbSdk.Metrics`, or an AST check that each observation name is a bare alias).
- Run `make check` (the `deadcode` gate flags any projector left unreferenced) and the `msb`-tagged
  suite for the real-stack paths.

## Estimates (production lines, seam package)

| | Option 1 | **Option 2 strict (chosen)** | Option 3 |
|---|---|---|---|
| Removed | −60 to −120 | **−500 to −560** | −2,200 to −2,500 |
| Adapter after | ~3,650 | **~3,200** | ~1,300 |
| Register after | ~450, still growing | **15 pairs (~250 l), bounded** | 0 |
| Consumer churn | none | **none** | repo-wide over time |
| Items 2-5 delta | | **0 to −40** | |

## What is left to the builder

- Confirm `msbSdk.ModificationConflict` is a full mirror with no enum field or method (if not, keep it
  launcher-owned; it has no register pair today).
- Decide item 3 and item 5 in/out; the guide leans item 3 split out.
- `changelog.d/<PR-number>.<type>.md` fragment and the `README`/docs sync `AGENTS.md` requires.
- Rebase note: this branch will merge the migration branch before #924 lands.

## References

- `.operator-shared/guides/msb-sdk-integration.md` §3 (lives on `main`; not yet on this branch)
- `.operator-shared/specs/architecture/msb-seam.md`
- `.operator-shared/guides/dead-code-detection-go.md`
- `internal/sandbox/msb/translate.go`, `translate_test.go`, `phase4_boundary_test.go`,
  `roles_boundary_test.go`
