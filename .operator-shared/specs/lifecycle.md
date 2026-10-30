---
description: Canonical model of the `run`/`shell` lifecycle: the ordered stage ids the code constants and trace test will share (#450, #452), what each stage reads, writes and guarantees, the single authority for every cross-stage value, the liveness principle and its per-artifact rules, the mode model, and the invariants a stage change must not break.
read_if: Making any change to a run/shell/session lifecycle stage or a cross-stage value — `cmd.runFunc`/`buildShellCmd`, `pruning.AutoPrune`/`keepImage`/`PruneVolumes`, `vm.PrepareSandbox` and its steps, `session.Run`/`session.Shell`, the `--dry-run`/`--dry-run-vm`/`--serve-only` modes — or diagnosing two subsystems that disagree about what is live or current (a run that reclaims, rebuilds or reverts something it is about to use).
---

# Run lifecycle

Tracking epic: [inoio/agents-sandbox#448](https://github.com/inoio/agents-sandbox/issues/448).

This document is the map of the `run`/`shell` lifecycle. It names the stages, the order they run in, and which stage
owns each value another stage consumes. It does not repeat the per-subsystem contracts; it links them. Read it before
moving work between stages, adding a stage, or changing who writes a value another stage reads.

The lifecycle defects this model exists to prevent were **decision-placement** bugs, not ordering bugs: one subsystem
decided "what is live/current" on its own and disagreed with another (the startup prune reclaiming the runner image a
`build` had just loaded; the `agent_version` baseline split between state and the image label). The rule that follows
from them: every cross-stage value has exactly one writer of record, and every other stage reads it or derives from it.

## Stage vocabulary

The stage ids below are the shared vocabulary. The stage-id constants (planned as `lifecycle.StageID` in a
constants-and-recorder package `internal/sandbox/lifecycle`) and the ordered lifecycle trace test do not exist yet
(#450, #452); once they land they must spell these ids identically, and a stage renamed here is renamed there, and the
reverse. Until then nothing checks the stage order automatically.

| # | Stage id           | Owner                                                | Reads → writes                                                    |
|---|--------------------|------------------------------------------------------|-------------------------------------------------------------------|
| 1 | `preflight`        | root `PersistentPreRunE`, `doctor.CheckAll`          | host prerequisites → private msb runtime installed                |
| 2 | `upgrade-check`    | `checkForUpgrade` (`cmd`)                            | launcher release, root state → self-upgrade, `updates.launcher`   |
| 3 | `prune`            | `pruning.AutoPrune` (from `runFunc`)                 | msb VMs/volumes/images, home states → reclaimed artifacts         |
| 4 | `resolve-identity` | `vm.resolveSessionIdentity`                          | cwd, agent, legacy artifacts → project key, adopted legacy state  |
| 5 | `resolve-version`  | `vm.determineAgentVersion`/`resolveBuildVersion`     | pin, image label, `agent_version`, upgrade prompt → build version |
| 6 | `ensure-image`     | `image.EnsureImage`                                  | build plan, Docker → runner image, `ImageInfo` with content ref   |
| 7 | `ensure-home`      | `vm.prepareHomeVolume` → `volume.ResolveHomeVolume`  | state, live VM mounts → home volume, `HomeState`, agent version   |
| 8 | `plan-reconfig`    | `vm.resolveReconfigDecision`/`applyReconfigHomeAction` | fingerprints, live VM config → recreate/restart, home action    |
| 9 | `ensure-vm`        | `vm.ensureProjectVM`                                 | decision, image, home volume → image loaded in msb, VM up         |
| 10 | `setup-sandbox`   | `vm.setUpSandbox`                                    | config files, boot kind → provisioned config, daemons, target     |
| 11 | `attach`          | `session.runAttach` / `session.runServeOnlySession`  | VM, target → interactive session or serve-only keeper             |

Stages 4–10 run inside `vm.PrepareSandbox`; stages 1–3 run in `cmd` before `session.Run`/`session.Shell`. Two inputs
are loaded once per startup between stages and shared by every consumer instead of being re-read: the host env/secret
files (`loadEnvFingerprints`, after `ensure-image`; consumed by `ensure-home`'s state seed, `plan-reconfig` and
`ensure-vm`) and the merged agent config files (`reprovision.LoadConfigFiles`, after `ensure-home`; consumed by
`plan-reconfig` and `setup-sandbox`).

## Stages

**`preflight`.** The root `PersistentPreRunE` installs or inspects the private msb runtime (`msbruntime.PrepareRuntime`;
a preview only applies the private `MSB_HOME`), then `doctor.CheckAll` verifies host prerequisites and fails the run on
a missing one. Guarantees every later stage a usable runtime and Docker. `run` and `shell` skip `doctor.CheckAll`
under `--dry-run`. Stages 1–3 are one helper both commands share (`cmd.runStartupStages`).

**`upgrade-check`.** Offers, performs or announces a launcher self-upgrade (`self-upgrade-channels.md`); an
upgrade-and-exit ends the invocation here. Skipped under `--dry-run` for both `run` and `shell`.

**`prune`.** Reclaims stale VMs, home volumes and runner images once per process under the liveness rules below,
passing the current invocation's keys as `RetainKeys`. It runs before `resolve-identity`, so it pre-resolves those keys
itself (`currentRunImageKeys`: the checkout slug plus the legacy repo slugs, best effort, nothing retained on failure);
`resolve-identity` stays the authority for the key the rest of the run uses. Under `--dry-run` it only reports.

**`resolve-identity`.** Looks up the agent, resolves the workspace identity (`git.ResolveIdentity`: bind root, repo
slug, checkout slug) and adopts a legacy origin-keyed identity (`AdoptLegacyIdentity`) before any artifact is touched,
so every later stage keys on the effective checkout slug. Under `--dry-run-vm` the adoption is read-only and the run
keeps resolving against the legacy location. See `checkout-identity.md`, `bare-worktree-layout.md`.

**`resolve-version`.** Decides the agent release to bake: an explicit pin, else the release the existing image's label
reports, else the `agent_version` cache, then the upgrade prompt, which may raise it. A user-provided agent is the one
exception: the tool does not own it, so `resolveBuildVersion` returns its recorded version before the pin, skipping
label recovery and the upgrade prompt (empty when nothing is recorded, leaving resolution to the build). The
`--dry-run` preview (`previewAgentVersion`) follows the same precedence. Folds legacy updater state. Its only
`HomeState` writes copy the authority into the cache (a stale or empty `agent_version` is rewritten from the
label) and mark an offered upgrade when state already exists.
Prevents a stale `agent_version` cache from selecting a release older than the image already holds; an explicit pin
may intentionally select an older release. See `agent-version-sync.md`, `upgrade-state-scoping.md`.

**`ensure-image`.** Builds or reuses the runner Docker image (`image.EnsureImage`) and returns `ImageInfo` carrying the
Docker tag, the content-addressed msb ref, the digest and the baked agent version. It does not load the image into msb:
the stage that first needs it there loads it under that ref (`image.EnsureLoaded`), skipping the load when the cached
content already matches. Guarantees a Docker image whose content every later stage refers to by the same ref. See
`runner-image-build.md`, `image-ref-content-addressing.md`, `image-composition.md`.

**`ensure-home`.** Offers the opt-in legacy runtime-home copy, then resolves the project's home volume: state, else the
live VM's mounted volume, else a freshly created and prefilled one with a fingerprint-seeded state
(`volume.Manager.ResolveHomeVolume`); prefilling a fresh volume loads the runner image into msb for a throwaway task
sandbox. Once state exists it records the deferred offered-upgrade mark and the baked agent version
(`persistOfferedVersion`, `persistAgentVersion`). Guarantees a `HomeState` that names the volume the VM will
mount. Under `--dry-run-vm` a project without a home creates no volume and writes no state: it carries the would-be
name and an in-memory, fingerprint-seeded state, so the first run with a VM creates and prefills the volume (#429). A
concurrent first run that loses the state commit adopts the winner's volume and removes its own (#431). See
`home-volume-recovery.md`, `project-state-transactions.md`, `runtime-home-decoupling.md`.

**`plan-reconfig`.** Compares the desired fingerprints and image with the persisted ones and the live VM to decide
recreate vs daemon restart vs nothing (`resolveReconfigDecision`), then applies the resulting home-volume action
(keep/migrate/reset) separately (`applyReconfigHomeAction`). Guarantees `ensure-vm` a decision whose home volume is
final. Under `--dry-run-vm` a migrate or reset action is left uncommitted. See `reconfig-decision.md`.

**`ensure-vm`.** Creates, starts, recreates or reconnects the project VM. Creation and recreation take the per-project
`ensure-vm.lock`; starting or reconnecting to an existing VM takes the lock-free fast path (`resolveExistingVM`). A
creation first loads the runner image into msb unless it is cached with matching content (`createProjectVM` →
`image.EnsureLoaded`), then persists the fingerprints (`persistConfigHashes`). Returns the boot kind. Skipped under
`--dry-run-vm` (no VM, nil sandbox). See `reconfig-decision.md`, `sandbox-identity.md`, `vm-idle-timeout-default.md`.

**`setup-sandbox`.** Provisions the agent config files, records boot provenance on a fresh boot
(`recordImageProvenance`), runs startup hooks, starts dockerd and the agent daemon (or restarts the daemon when the
plan asked for it), and resolves the attach target, including `--worktree` (`ResolveTarget`). Skipped under
`--dry-run-vm`. See `detached-jobs.md`, `opencode2-worktree-api.md`.

**`attach`.** Opens the interactive session as a login shell (`runAttach`), with the notify watcher and host link
opener alongside; `shell` attaches a plain login shell. Under `--serve-only` the attach is replaced by a keeper that
holds a client lease until EOF/SIGINT/SIGTERM (`runServeOnlySession`). Under `--dry-run-vm` it prints the would-start
line and returns.

## Cross-stage authorities

Each value has one writer of record. A reader never re-derives it from a second source and never writes it back.

- **Project key / identity.** Writer: `resolve-identity`. The checkout slug names state, VM, home volume, locks and the
  runner image; the repo slug names the per-slug config dir and the grouping label. A legacy adoption changes the
  effective slug before any artifact is touched. `prune`'s pre-resolved retain keys are a best-effort projection of it,
  not a second authority. (`checkout-identity.md`)
- **Agent version.** Authority: the runner image's `agent-version` label. `HomeState.agent_version` is a cache that
  follows it. Writer of record: the image-ensuring path — `ensure-image` surfaces `ImageInfo.AgentVersion` and
  `ensure-home` persists it once state exists; `build` writes it through `image.Build`. `resolve-version`'s
  reconciliation copies the label into the cache and is never the only writer. Boot provenance in `setup-sandbox` is a
  separate source, not label recovery: on a fresh boot `recordImageProvenance` records the agent and docker sources and,
  for a tool-provided agent, detects the running agent's version via its `VersionCmd()` and writes that detected value
  to the cache. (`agent-version-sync.md`)
- **Runner image ref.** Content-addressed; the authority is the Docker image ID, which equals the msb config digest.
  Writer: `ensure-image`. The Docker tag is a stable name, never an identity. (`image-ref-content-addressing.md`)
- **Home volume.** Authority: `HomeState.home_volume`; when state is lost, the live VM's mounted volume. Writer:
  `ensure-home`, changed later only by `plan-reconfig`'s home action. (`home-volume-recovery.md`,
  `project-state-transactions.md`)
- **Fingerprints.** Written once when a VM is created (`ensure-vm`), seeded into a fresh state by `ensure-home`; every
  other state rewrite carries them forward unchanged. Read by `plan-reconfig`. (`reconfig-decision.md`)
- **Liveness.** Decided once per process by `prune`, under the rule below.

## Liveness

**Principle.** An artifact is live, and must not be reclaimed, if **any** of these holds: a kept VM holds it; it
belongs to the current invocation's key; or it was used within the prune age and its project is not being retired
in this pass. Recent use does not save an artifact whose key's VM is reclaimed (`ToPrune`); only the first two
conditions do. A project with no VM is **not yet retired** — a `build` loads its image before any VM exists, and a VM
create can fail after its home volume exists.

`internal/sandbox/pruning/liveness.go` is the only implementation of these rules (`prunable`, `retires`, `keepImage`,
`allowsNativeImagePrune`); a change to what counts as live goes there. The key-unaware runtime memory sweep
(`PruneStorage`) is the one age-only exception. `pruning.buildPruneState` builds every input once: it classifies
project VMs into `ToPrune` (stopped and older than the age; their keys are retired in this pass, `PruneState.retires`),
`ToKeep` (every other project VM) and neither (no VM), and records the current invocation's keys as `RetainKeys`. Each
artifact kind applies the principle as follows:

| Artifact     | Reclaimed when                                                                                       | Retained by                                                                     |
|--------------|-------------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------|
| Runner image | not retained, and its key is in `ToPrune`, it parses no agent, or it was last used beyond the age (`keepImage`) | a kept VM's config holds it; its key is in `RetainKeys`; used within the age, unless its key is in `ToPrune` |
| Home volume  | its key is in `ToPrune` and no state file references it (`PruneVolumes`)                               | any key not in `ToPrune` (VM-less included); a referencing `HomeState`           |
| Project VM   | stopped and older than the age — including the current invocation's own VM                             | running, or stopped within the age                                              |
| Runtime memory backings | older than the age (`PruneStorage`, the runtime's own age-based sweep) | newer than the age; not key-aware |
| Task sandbox | stopped                                                                                               | running; never counts toward its project's liveness                             |

`keepImage` decides in this order, first match wins: a kept VM's config holds the ref (keep) → its key is in
`RetainKeys` (keep) → its key is in `ToPrune` (reclaim) → it parses an agent and was used within the age (keep) →
reclaim. The current invocation's key therefore wins over a `ToPrune` entry for the same key.

The native msb image-data prune (`Image.Prune`) reclaims any image no sandbox references and has no retention
parameter, so outside `--dry-run` it runs only when `RetainKeys` is empty: an explicit `prune`, or a `run` whose
identity `currentRunImageKeys` could not resolve (that run fails later with the real error). A `run` with retained keys
defers it.

The current invocation's own stale VM is reclaimed on purpose: project VMs are ephemeral and recreated by `ensure-vm`,
while what the run actually reuses — its runner image (retained by key) and its home volume (retained by its state
reference) — survives. The two landed fixes satisfy the principle: the image prune no longer treats a VM-less key as
retired and always retains `RetainKeys`, and the native prune is skipped while anything is retained.

## Mode model

`runoptions.ResolveMode` maps the flags to one `options.Mode`; `Mode.Previews()` is `--dry-run`, `Mode.SkipsVM()` is
`--dry-run` or `--dry-run-vm`. Serve-only is orthogonal to the mode.

| Stage              | `run`                | `run --dry-run`               | `run --dry-run-vm`                    | `shell`           |
|--------------------|----------------------|-------------------------------|---------------------------------------|-------------------|
| `preflight`        | yes                  | runtime home only, no doctor  | yes                                   | as `run`          |
| `upgrade-check`    | yes                  | skipped                       | yes                                   | as `run`          |
| `prune`            | yes                  | report only                   | yes                                   | as `run`          |
| `resolve-identity` | yes                  | read-only adoption            | read-only adoption                    | as `run`          |
| `resolve-version`  | yes                  | read-only (no upgrade check)  | yes                                   | as `run`          |
| `ensure-image`     | yes                  | build decision only           | yes (builds)                          | as `run`          |
| `ensure-home`      | yes                  | read-only probe               | recover/reuse only; no new home       | as `run`          |
| `plan-reconfig`    | yes                  | —                             | yes, home action uncommitted          | as `run`          |
| `ensure-vm`        | yes                  | —                             | skipped                               | as `run`          |
| `setup-sandbox`    | yes                  | —                             | skipped                               | as `run`          |
| `attach`           | session or keeper    | preview line                  | would-start line                      | login shell       |

- **`--dry-run`** is a read-only preview (`dry-run-preview.md`): `session.previewAndRender` runs read-only variants of
  `resolve-identity` through `ensure-home` (`vm.PreviewSandbox`), reports would-build/would-reuse, and returns before
  `vm.PrepareSandbox` is ever entered. No build, pull, network version lookup, volume, state write or VM.
- **`--dry-run-vm`** prepares for real through `plan-reconfig` (the KVM-less mode) and stops before `ensure-vm`. It
  builds the Docker image but loads nothing into msb and never creates a home volume; a fresh project's state stays
  in memory.
- **`--serve-only`** runs every stage; `attach` is replaced by the lease-holding keeper.

## Invariants

A change to any stage must keep all of these. Each names the spec that owns the detail.

- Every cross-stage value has exactly one writer of record (above); a second stage that needs it reads or derives it,
  never re-decides it.
- A run never reclaims the runner image or the home volume it is about to use (Liveness;
  `image-ref-content-addressing.md`).
- A project with no VM is not yet retired (Liveness).
- The image's `agent-version` label is the version authority; a cache that disagrees is rewritten, never built toward
  (`agent-version-sync.md`).
- The msb image ref is a pure function of `(slug, agent, docker image ID)` (`image-ref-content-addressing.md`).
- A state rewrite preserves the fields it does not own, through the locked transaction API, and never holds a state lock
  across msb I/O (`project-state-transactions.md`).
- Reconfig and every other state rewrite carry the fingerprints forward; only VM creation writes them
  (`reconfig-decision.md`).
- Identity adoption happens before any artifact is created under the new slug (`checkout-identity.md`).
- A lost state recovers the live VM's home volume before minting a new one (`home-volume-recovery.md`).
- `--dry-run` performs no mutation and no network request (`dry-run-preview.md`).
