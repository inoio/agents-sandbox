# Design: Decoupled per-type pruning + `image prune` subcommand (Chunk F)

## Goal

Two goals, done together because they are the same change:

1. **Refactor** the monolithic prune pipeline in `internal/sandbox/pruning` into
   three independent, per-artifact-type pruners (`VMs`, `Volumes`, `Images`) that share a
   small "live state" snapshot. This eliminates the cascade/orphan/active-VM phase
   coupling that currently makes the package hard to extend.
2. **Expose** three new subcommands that map 1:1 onto those pruners — `image prune`,
   `volume prune`, and `sandbox prune` — with the existing aggregate `prune` becoming a
   thin composition of the three.

## Problem statement

The current `pruning.Prune` (prune.go) couples four orthogonal concerns:

1. **Collection** (`buildCatalog`): lists sandboxes, volumes, and images; filters by
   `naming` prefixes; groups everything into slug-keyed maps.
2. **Policy**: decides *why* each artifact is prunable. The cascade
   (`pruneStaleCascade`) removes a stale VM's volumes and images in the same step;
   orphan slugs (`pruneOrphanSlug`) remove both volumes and images; active-VM digests
   (`pruneActiveVMCleanup`) keep only the current image/volume.
3. **Cross-artifact coupling**: volume/image prunability depends on sandbox lifecycle
   state, so a volume or image prune cannot run standalone.
4. **Removal**: per-ref SDK calls with dry-run and error handling (`removeHomeVolumes`,
   `removeMSBImages`, `pruneDockerImages`).

This coupling is why a targeted `image prune` is not straightforward today: images are
only ever removed as a side effect of the cascade/orphan/active-VM phases, never on
their own.

## Design principle

**For volumes and images, every prune reason collapses to a single rule: an artifact is
prunable iff (a) it is older than the threshold and (b) its slug is not in the "keep"
set.** The keep set is the set of slugs that have a live VM. Only VMs themselves need
age/status machinery.

Therefore:

- **Decouple the cascade.** A stale VM is removed by the VM pruner; its now-unreferenced
  volumes and images are reclaimed independently by the volume/image pruners. In the
  aggregate `prune` (which runs all three) this still happens in a single invocation, so
  end-user-visible behavior is preserved.
- **One shared snapshot**, parameterizable keep-set. Each artifact type owns a
  self-contained predicate; no type needs another type's internals.

## The shared snapshot

Replace `PruningCatalog` with a minimal `PruneState` built from a single `ListSandboxes`
call. It records which project slugs are stale (their VM will be removed), and is the
only shared data across pruners.

```go
// PruneState is a point-in-time view of which project slugs have a VM that
// PruneSandboxes will remove. Slugs absent from the map have a surviving VM
// (running, or stopped-but-not-stale) and their artifacts are kept.
func buildPruneState(ctx context.Context, age time.Duration) (PruneState, error)

type PruneState map[string]msb.SandboxHandle // slug -> stale VM/task handle to remove
```

- A **stale stopped VM** (older than `age`) is included, so `PruneSandboxes` removes it and
  its volumes/images become prunable — this preserves the old cascade behavior without a
  separate cascade code path.
- **Task sandboxes** are always included (no age gate) unless running.
- **Running VMs** and **stopped-but-not-stale VMs** are excluded: their volumes and images
  are kept.

## Three independent pruners

All live in the `pruning` package. Each lists its own artifacts, applies its own
predicate, owns its own report type, and handles dry-run and per-item errors itself.
The *decision* of what to prune lives in `buildPruneState`; the pruners only execute
against it.

```go
func PruneSandboxes(ctx context.Context, pruneState PruneState, dryRun bool, ui termio.UI) (SandboxReport, error)
func PruneVolumes(ctx context.Context, pruneState PruneState, dryRun bool, ui termio.UI) (VolumeReport, error)
func PruneImages(ctx context.Context, pruneState PruneState, dryRun bool, ui termio.UI) (ImageReport, error)
```

All pruners take `threshold`; `0` means "no wait" (prune anything not kept), and callers
are responsible for passing a positive default so auto-prune never prunes too early.

### PruneSandboxes

Two artifact classes, both handled here. It iterates the `PruneState` and removes each
non-running sandbox:

- **Task sandboxes** (`naming.TaskPrefix`): these are the transient prefill/copy VMs
  created by the volume migrate/reset/edit operations (volume.go, operations.go), so
  they are real and must be pruned. Rule: prune any **non-running**
  (`!msb.IsSandboxActive`) task sandbox with **no age gate** ("always prune, except
  running"). A running task sandbox is left alone. Treated as VMs for reporting (StaleType VM).
- **VMs** (`naming.VmPrefix`): pruned iff stopped/crashed (`!msb.IsSandboxActive`).
  Age gating is handled by `buildPruneState`, which only places stale VMs in `PruneState`.
  Remove via `client.RemoveSandbox`.
- Reports counts + per-item details (name, slug, stale-for).

### PruneVolumes

- **Home volumes** (`naming.HomePrefix`): pruned iff the volume's slug is in
  `PruneState` (a stale VM). Because stale VMs are in `PruneState`, a stale VM's home
  volumes are pruned — this replaces the old cascade. After removing a slug's **last**
  home volume, call `state.RemoveState(slug)`, so the state file never outlives the
  volume it references.
- **Clone volumes are dead code**: `ClonePrefix` has no create site anywhere in the
  codebase — clone volumes are never produced, only parsed and pruned. Drop the clone
  volume branch entirely from `PruneVolumes` (and remove `pruneCloneVolumes`,
  `PrunedCloneVolumes`, and the `ClonePrefix` collection in the snapshot/collection
  step). This is a net simplification: no clone handling exists at all.
- Reports counts + details.

### PruneImages

Two sub-parts, both owned here.

- **MSB runner images**: list `naming.ImagePrefix` images (exclude `naming.BaseSlug` and
  `naming.BaseDindSlug`). Unified prune rule per image (`slug`, `ref`, `digest`):
  1. slug **in** `PruneState` (stale VM) → prunable (all digests removed; no surviving VM).
  2. slug **not** in `PruneState` but its digest diverges from the slug's current digest
     recorded in its **state file** → prunable as surplus (a surviving project keeps only
     its current image; older digests are reclaimed). Without a state file the current
     digest cannot be determined, so all digests are kept.
  3. otherwise → keep.
  Remove via `client.ImageRemove(ctx, ref, true)`.
- **Host-side dangling docker images**: the existing `pruneDockerImages` step (removes
  untagged images via `docker.Get().ImagePrune`). It has no slug/age/keep-set and always
  runs (skipped under dry-run, matching today).
- Reports counts + details for MSB images, plus docker dangling count.

## Age model

Defaults by artifact type and invocation mode:

| Invocation | VMs | Volumes | Images |
|---|---|---|---|
| Manual (`prune`, `sandbox prune`, `volume prune`, `image prune`) | `--age` else `manual-prune-age` else 7d | `--age` else `manual-prune-age` else 7d | `--age` else `manual-prune-age` else 7d |
| Auto-prune (before every command) | 30d | 30d | 30d |

- **VMs, volumes, and images** use the same defaults: `manual-prune-age` (default 7d)
  for manual prunes and the existing 30d `auto-prune-age` for auto-prune. The `--age`
  flag overrides the manual default. `image prune` therefore exposes `--age` like the
  other prune commands, and the age setting applies uniformly to all artifact types.
- The pruners are pure functions of `threshold`; `0` means "no wait". Callers
  (`AutoPrune`, the prune commands) resolve a positive default before calling so auto-prune
  never accidentally prunes young artifacts.

## Aggregate `prune` = composition

`Prune(ctx, threshold, dryRun, ui)` becomes a thin orchestration:

1. Build `PruneState` once.
2. Call `PruneSandboxes`, `PruneVolumes`, and `PruneImages` against it.
3. Merge the three typed reports into the existing `StaleReport` so the aggregate
   summary output and CLI tests stay stable.

**Report shape:** task sandboxes count into `PrunedSandboxes` (they are VMs). `PrunedCloneVolumes`
is removed. The aggregate summary becomes:
`Pruned %d VMs, %d home volumes, %d docker images, %d msb images`.

`AutoPrune` (cleanup.go) is unchanged — it already calls `Prune`.

## Subcommands

Each new subcommand maps 1:1 to a pruner. `--age` is a **plain cobra flag** (not
viper-bound, matching today's `prune`). When empty, the command resolves its default via
`resolverFromContext(cmd.Context())`:

- `sandbox prune`, `volume prune`, and `image prune`: `--age` else `manual-prune-age`
  else `7d`.
- Global `-y/--yes` for confirmation, `--dry-run` shared as today.

- `image prune` → `PruneImages` (MSB runner images + docker dangling). Added under
  `buildImageCmd` (`cmdImage`). Flags: `--age`, `--dry-run`.
- `volume prune` → `PruneVolumes`. Added under `buildVolumeCmd` (`cmdVolume`). Flags:
  `--age`, `--dry-run`.
- `sandbox prune` → `PruneSandboxes`. Added under `buildSandboxCmd` (`cmdSandbox`). Flags:
  `--age`, `--dry-run`.

Each renders its typed report (counts + freed bytes where available).

## Files touched

- `internal/sandbox/pruning/` — core refactor:
  - New `state.go`: `PruneState` + builder.
  - New `sandboxes.go`, `volumes.go`, `images.go`: the three pruners + typed reports.
  - `prune.go`: `Prune` becomes the composition; delete cascade/orphan/active-VM phase
    functions (`pruneStaleCascade`, `pruneOrphanSlug`, `pruneActiveVMCleanup`,
    `pruneActiveVMHomeVolumes`, `pruneActiveVMMSBImages`, `pruneCloneVolumes`,
    `pruneTaskSandboxes`).
  - `remove.go`: fold `removeHomeVolumes`/`removeMSBImages` logic into the pruners;
    keep `pruneDockerImages` (moved to `images.go`). Delete `isRecent` if unused.
  - `catalog.go`: delete `buildCatalog` + `findStaleVMs` + `PruningCatalog` and the
    `imageWithDigest`/`volumeWithAge` grouping types.
  - `report.go`: keep `StaleReport` as the aggregate output; add typed reports.
  - `stale.go`: keep `StaleType` (used by reports).
  - Remove **clone volume** code (dead — no create site): `pruneCloneVolumes`,
    `PrunedCloneVolumes`, and the `ClonePrefix` branch in the collection step.
- `internal/sandbox/naming/` — remove the dead clone-volume parsing:
  `ParseCloneVolumeName` and the `ClonePrefix` case in `ArtifactFor` (only consumers
  were the pruned clone code).
- `cmd/opencode-sandbox/commands_system.go` — add the three subcommands;
  `buildPruneCmd` unchanged.
- `cmd/opencode-sandbox/constants.go` — add a `cmdPruneSub` alias if needed for the
  nested `prune` (likely not required; reuse `cmdPrune`).
- `internal/sandbox/image/` — no changes. The earlier `image prune` spec (SDK
  `Image.Prune`) is **replaced** by this design; no SDK `Image.Prune` call and no
  `Prune` entry point in the `image` or `session` packages. Commands call
  `pruning.PruneImages`/`PruneSandboxes` directly.

## Testing

- TDD: write tests first. Unit tests per pruner (`sandboxes_test.go`, `volumes_test.go`,
  `images_test.go`) covering: stale-slug membership in `PruneState`, surplus-digest
  pruning vs the state file, age gate in `buildPruneState`, dry-run (counts reported, no
  SDK call), state removal only when a slug's last volume is gone, and per-item removal
  errors.
- Explicit behavior-parity cases: task sandboxes are pruned with no age gate but a
  running task sandbox is skipped; stale slugs have their volumes and images reclaimed
  in one invocation; clone volumes are gone.
- `buildPruneState` test: stale vs kept VM extraction, task-sandbox handling, age 0.
- Aggregate `Prune` test: confirms the three reports merge into `StaleReport` and that
  a stale VM's volumes/images are still cleaned in one invocation (end-to-end parity).
- CLI tests (`cmd/opencode-sandbox/cli_prune_test.go` + new
  `cli_image_prune_test.go`/`cli_volume_prune_test.go`/`cli_sandbox_prune_test.go`):
  flag parsing (`--age`, `--dry-run`, `-y`), report output for each
  subcommand, error cases (invalid age), and that all three accept `--age`.

## Out of scope

- The SDK `Image.Prune` API (superseded by per-ref removal in `PruneImages`).
- Image save/load.
- Docker dangling-image cleanup exposed outside the aggregate/image-prune paths.