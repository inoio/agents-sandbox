---
description: Design and contract for scoping agent upgrade/provenance state by application - machine-global network-check facts (cadence + latest known version) vs per-(slug, agent) image facts (current version, sources, offered set) - so the upgrade prompt can never name another project's version. Retires the global updater.yaml via a non-interactive, legacy-file-triggered fold of last_checked into the root state.
read_if: Changing the agent upgrade prompt, pendingUpgrade/resolveBuildVersion, recordImageProvenance, the updater state model, or the legacy updater.yaml migration.
---

# Scoping of agent upgrade and provenance state

## Intent

The interactive agent-upgrade prompt can name a version that belongs to a different project. The prompt's `current`
value comes from `vm.currentUpgradeVersion(a)`, which reads the machine-global `updater.yaml` keyed only by agent name.
That file is rewritten by `recordImageProvenance` on every VM boot, so it tracks "the last image that booted", not the
project being launched. Because the runner image is per `(slug, agent)` (`naming.RunnerImageRef`) and the prompt fires
before this project's image is touched (see `resolveBuildVersion`), a project can be told its image has another project's
version (PR #225 review finding).

The fix is to stop treating upgrade state as one global blob and split it by application: the network-facing facts are
machine-global; everything that describes a project's image is per `(slug, agent)`. This removes the cross-project
contamination structurally rather than by relabeling.

## Current state (before this change)

Three files under the user state dir (`configpaths.UserStateDir()`):

- `<root>/state.yaml` - `state.RootState`, launcher-global, currently only the `migrations` registry.
- `<root>/updater.yaml` - `vm.upgradeState`, machine-global, keyed by agent name: `last_checked`,
  `current_version`, `agent_source`, `docker_source`, `offered_versions` (plus legacy flat fields folded into
  `agents["opencode"]` on load).
- `<root>/<slug>/<agent>/state.yaml` - `state.HomeState`, per `(slug, agent)`: `home_volume`, `image_digest`, and the
  env/secret/network/mount fingerprints.

`upgrade.json` (the launcher's own self-upgrade state, `internal/upgrade`) is unrelated and out of scope.

## Target model

Two documents, split by application:

**Machine-global `<root>/state.yaml` (`state.RootState`)** - launcher-wide, network-facing:

- `migrations` (unchanged).
- `agents: map[agentName]AgentCheckState` with:
  - `last_checked` - when the releases endpoint was last successfully hit for this agent. One cadence for the whole
    machine, so N projects do not each call GitHub.
  - `latest_version` - the cached newest release, so a project prompt can compare without hitting the network.

**Per `(slug, agent)` `<root>/<slug>/<agent>/state.yaml` (`state.HomeState`)** - describes this project's image only.
Existing fields plus:

- `agent_version` - the agent release baked into this project's image (the upgrade baseline; the value the prompt
  shows as "current").
- `agent_source` - `tool` | `user`, read from the image's provenance file.
- `docker_source` - `tool` | `user`.
- `offered_versions` - releases whose rebuild prompt this project has already shown (the "don't nag" gate).

`updater.yaml` is retired.

### Why per-agent, not per-project

The runner image and its version are per `(slug, agent)`; a project-wide `offered_versions` set would wrongly suppress
agent B's prompt once agent A had offered the same version. The per-agent file is also the only project-scoped state
document that exists; no new project-level file is introduced (see Non-goals).

## Flow

### Upgrade check (`vm.resolveBuildVersion` -> `vm.pendingUpgrade`)

`resolveBuildVersion` takes the project `state.Key` (available in `PrepareSandbox`) and reads
`current`/`agent_source` from that key's `HomeState` instead of the global file.

`pendingUpgrade(ctx, a, k, ui, current)`:

1. Read the global root state. On a read error, warn and return no offer and do **not** persist: an unreadable document
   (e.g. a corrupt `state.yaml`) must never be overwritten, so the `migrations` registry and every other agent's cached
   check survive.
2. Read the global `AgentCheckState` for `a`.
3. If `dueForCheck(now)`: call the agent's `LatestVersion`. On error, warn and return no offer (the window stays open).
   On success, set `last_checked = now` and `latest_version = latest`, and persist the root state.
4. `latest := cached latest_version`; if empty, no offer.
5. Compare `NewerThan(latest, current)`. If not newer, no offer.
6. Read `HomeState(k)`; if its `offered_versions` already contains `latest`, no offer.
7. Append `latest` to the `HomeState` `offered_versions` (an update-only write; no state is created) and return the
   offer. Because this runs before home resolution, `resolveBuildVersion` also returns the offered release so
   `PrepareSandbox` can record it again after home resolution recreates a missing state; otherwise a "don't remind
   again" choice would be forgotten for a project whose state file was lost.

The prompt (`promptUpgrade`) then names both `latest` and this project's `current` (PR #225 wording: the prompt reads
`"Rebuild the runner image with <agent> <latest>? (current image has <current>)"` and the Rebuild choice reads
`"Upgrade <agent> <current> → <latest>"`). Because that `current` is read from the project's own file, it cannot name
another project's version.

Under `--dry-run` (`opts.DryRun`, which implies `--dry-run-vm`), `resolveBuildVersion` is not called at all:
`PrepareSandbox` uses the project's recorded `HomeState.agent_version` as the build arg (no network check, no prompt)
and emits a verbose `dry-run: would resolve build version`. This keeps `--dry-run` side-effect-free, matching the
home-migration offer gate; the legacy fold below is likewise skipped under `--dry-run`.

### Baseline recovery (image-label fallback)

Migration drops the global `current_version`, so a pre-existing project has no `HomeState.agent_version` on its first
run after the upgrade. The run decision happens *before* the boot that `recordImageProvenance` could record it, so
without recovery `current == ""` and the build resolves the latest release: the existing image is silently rebuilt to
latest (no prompt), and offline the launch fails before it can reuse the image. Both are regressions from the
pre-change behavior, where the non-empty global version made the prompt fire and let the existing image be reused
offline.

To close that gap, `resolveBuildVersion` recovers the baseline from the project's own existing image before giving up:
`image.ExistingAgentVersion(ctx, slug, agent)` reads the local runner image's
`org.agents-sandbox.identity.agent-version` label (a `docker inspect`, no network) and returns the release, or nothing
for an absent image, a pre-label image, or a non-release sentinel (`none` / `user-provided`). When it returns a
version, `recoverAgentVersion` records it to `HomeState.agent_version` and the normal check/prompt runs against it. The
record is written only when a state file already exists (via `state.UpdateExistingHomeState`), because recovery runs
before home resolution: creating a state here would leave a fingerprint-less partial state that the home-recovery step
would adopt as a `prior` and copy empty fingerprints forward, spuriously detecting a config change (#183). A genuine
first run (no image, no label) still returns `""` and resolves at build time.

The label is a *recovery fallback only*: `HomeState.agent_version` remains the single source of truth once recorded.
This does not resurrect the rejected "read the current version from the image label on every run" design; it only
bridges the gap left by dropping the global version.

### RootState writer serialization

`RootState` now has two independent writers: `runtimemigration` (the `migrations` map) and the upgrade path (the
`agents` map). They share one file, so all `RootState` read-modify-write is serialized by a single host-only exclusive
flock. `runtimemigration` adopts this shared lock (retiring its private `migration.lock`) and keeps holding it across
its copy campaign; the upgrade path acquires it **non-blocking** and, if it is held, skips the whole upgrade step for
this run (no check, no fold), retrying next run. `state.UpdateRootState` stays lock-free and documents that callers must
hold the lock; this avoids the non-reentrant double-lock that a lock-inside-`UpdateRootState` design would cause for
`runtimemigration` (flock is not reentrant).

### HomeState write serialization

> **Superseded.** The `UpdateHomeState` / `UpdateExistingHomeState` / `ReplaceHomeState` trio and `LockHomeState`
> described in this section are replaced by the transaction API (`WithHomeState` / `RecoverHomeState`) in
> [`project-state-transactions.md`](project-state-transactions.md). The serialization intent below still holds; read that
> spec for the current contract.

Every per-project `HomeState` read-modify-write is serialized by a single host-only flock per `(slug, agent)`
(`state.LockHomeState`, at `<slug>/<agent>/home.lock`). This PR adds two HomeState writers (the upgrade offered set and
`recordImageProvenance`) to a file the volume operations already read-modify-write, so without the lock a concurrent
volume op could be clobbered — e.g. an upgrade write restoring an old `home_volume` after a volume op committed a new
one, which with `--rm-old` points at a deleted volume. The lock makes each writer re-read under mutual exclusion, so no
writer loses another's field.

`state` exposes three locked writers, chosen by intent:

- `UpdateHomeState(k, mutate)` — create-on-missing, for merge writers that run after home resolution (volume operations,
  fingerprint persistence, `recordImageProvenance`). It mutates the freshly-read state, preserving fields the caller
  does not touch; a present-but-unreadable file returns an error without writing.
- `UpdateExistingHomeState(k, mutate)` — writes only when a state file already exists, for the pre-home-resolution
  writers (baseline recovery, the offered set). It never creates a state, so those writers cannot leave a
  fingerprint-less partial state that home recovery would adopt as a `prior` (#183).
- `ReplaceHomeState(k, hs)` — lock plus a blind write, for recovery paths that rebuild a lost or malformed state
  (`volume.Manager.recoverOrCreateHome`, `EnsureNewHome`). These must *replace* the document, so they cannot read it
  first (a corrupt file would fail the read and leave the state broken).

Lock ordering is **root lock (if held) → home lock**. `pendingUpgrade` holds the root lock across its step and then
takes the home lock for the offered-set write; no holder of a home lock acquires the root lock (the volume operations
do not touch `RootState`), so the order cannot cycle. flock is not reentrant, so no single call path may take the same
key's home lock twice.

### Provenance (`vm.recordImageProvenance`)

`setUpSandbox` threads the project `state.Key` into `recordImageProvenance` (the caller already holds `k`). On boot it
reads `HomeState(k)`, sets `agent_source`/`docker_source`, and for a tool-installed agent detects the installed version
via the agent's `VersionProvider` and writes it to `agent_version`. It is a read-modify-write (now via
`state.UpdateHomeState`) so fingerprints and the offered set survive. Warnings remain non-fatal.

## Migration (non-interactive, legacy-file-triggered, no registry entry)

The presence of `<root>/updater.yaml` is the trigger. No user choice, no `migrations` registry entry.

- Read the legacy file with the existing parser (including the flat-field fold). For each agent, if the global
  `AgentCheckState.last_checked` is **unset**, copy the legacy `last_checked` over; an already-present value wins.
- `latest_version` is not carried (the legacy file never stored it).
- `current_version`/`agent_source`/`docker_source`/`offered_versions` are not carried: they cannot be attributed to a
  project from a global, agent-keyed file. Each project re-detects its version and source on next boot, and its offered
  set resets, costing at most one extra prompt.
- After the root-state write succeeds, delete `updater.yaml`, so the legacy file is effectively removed. A missing,
  empty, or unparsable file folds nothing but is still deleted.
- Best effort and silent: run once per startup before the upgrade check, under the same shared `RootState` flock as the
  upgrade path (non-blocking; skipped if held), and skipped entirely under `--dry-run`. On any failure, warn and
  continue, leaving `updater.yaml` for a later attempt.

## Invariants

- **No cross-project version.** The prompt's `current` is always read from the launched project's `HomeState`.
- **Split by application.** `RootState` holds only machine-wide facts (migrations, per-agent network cadence/cache);
  image-describing facts live only in the per-agent `HomeState`.
- **HomeState rewrites preserve upgrade fields.** As with the fingerprints, every writer must carry `agent_version`,
  `agent_source`, `docker_source`, and `offered_versions` forward. The read-modify-write writers do so already; the
  writers that rebuild a fresh `HomeState` and copy only the fingerprints must be extended to copy the upgrade fields
  too: `volume.operations.volumeOp`, `volume.Manager.ApplyHomeAction`, and `volume.Manager.recoveredHomeState` (#219,
  when a prior state exists). A volume op or a recovered volume does not change the image, so dropping the fields would
  falsely reset the baseline and the offered set.
- **No silent rebuild; offline reuse.** A project with an existing runner image never silently rebuilds to the latest
  release: when no baseline is recorded it is recovered from the image label so the normal prompt fires, and an offline
  check reuses the existing image (the recovered, non-empty `current` short-circuits release resolution).
- **Migration never loses the cadence.** `last_checked` is folded only when unset; the legacy file is deleted only
  after a successful root-state write.
- **Serialized HomeState writes.** Every `HomeState` read-modify-write holds the per-`(slug, agent)` home lock, so
  concurrent writers (volume operations, upgrade bookkeeping, fingerprint persistence) cannot lose each other's fields.
  Lock order is root → home; the home lock is never taken twice on one path (flock is not reentrant).
- **No partial state before home resolution.** Writers that run before the home volume is resolved (baseline recovery,
  the offered set) never create a state, so a state that exists at home resolution is never fingerprint-less by
  construction.
- **Recovery repairs malformed state.** The home-recovery paths replace the state file wholesale under the lock, so a
  corrupt `state.yaml` is rewritten rather than left broken (which would repeat recovery and keep volume commands unable
  to read the home).
- **Serialized RootState writes.** Every `RootState` read-modify-write holds the shared host-only flock;
  `state.UpdateRootState` stays lock-free and requires the caller to hold it. `runtimemigration` and the upgrade path
  share the one lock, so neither can clobber the other's map.
- **A RootState read failure never writes.** A `RootState` read error must warn and return no offer without persisting,
  never overwrite the file with a zero-value document that would discard the `migrations` registry.
- **`--dry-run` is side-effect-free.** Under it there is no upgrade network check, no state persistence, and no legacy
  fold; `resolveBuildVersion` is replaced by a verbose "would resolve build version" message.

## Edge cases

- First run after the change for a *pre-existing* project: `HomeState` has no `agent_version`, but the existing image
  carries the label, so `current` is recovered, the normal prompt fires, and the baseline is persisted. No silent
  rebuild.
- A genuine first run (no existing image): no label to recover, so `current == ""` and no prompt fires; the build
  resolves the latest release, and boot writes the baseline.
- A project whose `state.yaml` was lost but whose image still exists: the baseline is recovered from the image label for
  this run but not persisted (no state file to update), so no partial state is created; home recovery then writes the
  state and a later run re-recovers the baseline to persist it.
- A malformed (unparseable) `state.yaml`: home resolution routes it to recovery, which replaces the file via
  `ReplaceHomeState`, so the launch continues and the next run reads a healthy state.
- Legacy `updater.yaml` present but global `last_checked` already set: no fold, file deleted.
- A freshly folded `last_checked` intentionally defers the first check: if the legacy timestamp is recent, the first
  post-migration run is not due, so no network fetch happens, `latest_version` stays empty, and no prompt fires until
  the 24h window expires. This is the intended consequence of honoring the folded cadence, not a missing check.
- Concurrent launches: the shared flock serializes `RootState` writes and the fold; a held lock (e.g. an in-flight
  runtime-home copy campaign) makes the upgrade step and fold skip this run and retry next run. Each project's prompt
  path is independent.
- `--dry-run`: `resolveBuildVersion` is skipped and prints the verbose message; the fold does not run; `updater.yaml` is
  left for a real run.

## Non-goals

- No new project-level (cross-agent) state file. "Project-only" state means the existing per-agent
  `<slug>/<agent>/state.yaml`, which already sits under the slug.
- No project identity data (origin URL, workspace path, recency) is introduced.
- No `migrations` registry entry; the legacy file itself is the trigger.
- No `sandbox create`/`run` prompt-order change; the prompt still fires before the image is built.
- Reading the version from the image label is not the source of truth; `HomeState.agent_version` remains the baseline.
  The label is read only as a one-time recovery fallback when no baseline is recorded (see Baseline recovery).
- `config harden`, `upgrade.json`, and launcher self-upgrade are untouched.

## Docs and changelog

Update `docs/runner-image.md` and add a `CHANGELOG.md` `[Unreleased]` line. The existing PR #225 wording change to the
prompt is retained; only the source of `current` and the state layout change.
