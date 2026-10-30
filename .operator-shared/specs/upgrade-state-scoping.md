---
description: Design and contract for scoping agent upgrade/provenance state by application - machine-global network-check facts (cadence + latest known version) vs per-(slug, agent) image facts (current version, sources, offered set) - so the upgrade prompt can never name another project's version. Retires the global updater.yaml via a non-interactive, legacy-file-triggered fold of last_checked into the root state.
read_if: Changing the agent upgrade prompt, pendingUpgrade/resolveBuildVersion, recordImageProvenance, the updater state model, or the legacy updater.yaml migration.
---

# Scoping of agent upgrade and provenance state

## Intent

The interactive agent-upgrade prompt can name a version that belongs to a different project. The prompt's `current`
value comes from `vm.currentUpgradeVersion(a)`, which reads the machine-global `updater.yaml` keyed only by agent name.
`recordImageProvenance` rewrote that file on every VM boot, so it tracked "the last image that booted", not the
project being launched (it now updates the project's `HomeState` in place, skipped when no state file exists; #430).
Because the runner image is per `(slug, agent)` (`naming.RunnerImageRef`) and the prompt fires
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
- `updates.agents: map[agentName]AgentCheckState` with:
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

1. Read the global root state under a non-blocking root transaction. On a read error or a held lock, warn and return no
   offer and do **not** persist: an unreadable document (e.g. a corrupt `state.yaml`) must never be overwritten, so the
   `migrations` registry and every other agent's cached check survive.
2. Read the global `AgentCheckState` for `a`.
3. If the check is due (`state.AgentCheckState.DueForCheck`: never checked, or at least `state.AgentCheckInterval`
   = 24h since the last check): **release the lock and call the agent's `LatestVersion` outside it**, so a slow request
   never serializes unrelated projects (adoption, the runtime-home copy campaign) behind one agent's egress check. On
   error, warn and return no offer (the window stays open). On success, re-acquire the lock (non-blocking) and re-check
   `DueForCheck` before persisting: if another process refreshed the window while the request ran, skip the write and use
   its cached entry, so two concurrent due checks persist at most one refresh. A held lock or unreadable state defers the
   step; a write failure is warned but the freshly fetched version is still offered this run.
4. `latest := cached latest_version`; if empty, no offer.
5. Compare `NewerThan(latest, current)`. If not newer, no offer.
6. Claim `latest` for this project: inside a single `HomeState(k)` transaction, check whether `offered_versions`
   already contains `latest` and, if not, append it (an update-only write; no state is created) and return the offer. A
   single transaction means two concurrent launches of the same project cannot both pass the gate and prompt. Because
   this runs before home resolution, `resolveBuildVersion` also returns the offered release so `PrepareSandbox` can
   record it again after home resolution recreates a missing state; otherwise a "don't remind again" choice would be
   forgotten for a project whose state file was lost.

The prompt (`promptUpgrade`) then names both `latest` and this project's `current` (PR #225 wording: the prompt reads
`"Rebuild the runner image with <agent> <latest>? (current image has <current>)"` and the Rebuild choice reads
`"Upgrade <agent> <current> → <latest>"`). Because that `current` is read from the project's own file, it cannot name
another project's version.

Under `--dry-run` (`ModePreview`, resolved by `runoptions.ResolveMode`), `resolveBuildVersion` is never reached: the
session returns from the preview before it calls the setup hub, and the hub has no dry-run gate of its own. There is
likewise no recorded-version substitution and no verbose dry-run line. This is the upgrade-check half of `--dry-run`'s
side-effect-freedom, matching the home-migration offer; the legacy fold below is unreached on the preview path
too. The full `--dry-run` contract (no image build, no volume create, no state write, no network request) is
[dry-run-preview.md](dry-run-preview.md).

### Baseline recovery from the image label

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
record is written only when a state file already exists (a `WithHomeState` transaction that writes nothing when
`exists` is false), because recovery runs
before home resolution: creating a state here would leave a fingerprint-less partial state that the home-recovery step
would adopt as a `prior` and copy empty fingerprints forward, spuriously detecting a config change (#183). A genuine
first run (no image, no label) still returns `""` and resolves at build time.

The label is the **authority for an existing image** and `HomeState.agent_version` is a cache that follows it
([agent-version-sync.md](agent-version-sync.md)). Recovery widens from "empty only" to "empty **or disagreeing**": a
recorded value that disagrees with a present image's label is stale, the label wins, and the cache is rewritten, so a
stale record can never pull the build toward an older release. The recorded cache stays authoritative only when the
image is absent (pruned, host docker cleaned), which keeps offline reuse working. This supersedes the earlier
"one-time recovery fallback" reading: the label is consulted whenever the image is present.

### State write serialization

> **Superseded by [`project-state-transactions.md`](project-state-transactions.md).** The `UpdateRootState` /
> `UpdateHomeState` / `UpdateExistingHomeState` / `ReplaceHomeState` / `LockHomeState` seam and the standalone root and
> home locks described here are retired by the transaction API (`WithRootState` / `WithHomeState` / `RecoverHomeState` /
> `RemoveHomeState`). The intent is unchanged: every read-modify-write is serialized by one lock per document, the
> upgrade path and the legacy fold acquire the root transaction non-blocking and defer when it is held, and a writer
> preserves the fields it does not own. Read that spec for the current mutation API, the lock files, and the lock
> ordering.

### Provenance (`vm.recordImageProvenance`)

`setUpSandbox` threads the project `state.Key` into `recordImageProvenance` (the caller already holds `k`). On boot it
reads `HomeState(k)` and updates it in place — `agent_source`/`docker_source`, and for a tool-installed agent the version
the `VersionProvider` detects, written to `agent_version` — so fingerprints and the offered set survive. It is a no-op
when no state file exists, like every other pre-home-resolution writer
([project-state-transactions.md](project-state-transactions.md), #430). Warnings remain non-fatal.

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
  release: the image's `agent-version` label is the baseline for a present image (recovered when empty and reconciled
  when it disagrees), so the normal prompt fires and a disagreeing cache cannot select an older build. An offline
  check reuses the existing image (the non-empty `current` short-circuits release resolution).
- **Migration never loses the cadence.** `last_checked` is folded only when unset; the legacy file is deleted only
  after a successful root-state write.
- **Serialized HomeState writes.** Every `HomeState` read-modify-write goes through the locked transaction API and
  preserves fields the caller does not own, so concurrent writers (volume operations, upgrade bookkeeping, fingerprint
  persistence) cannot lose each other's fields (`project-state-transactions.md`).
- **No partial state before home resolution.** Writers that run before the home volume is resolved (baseline recovery,
  the offered set) never create a state, so a state that exists at home resolution is never fingerprint-less by
  construction.
- **Recovery repairs malformed state.** The home-recovery paths replace the state file wholesale under the lock, so a
  corrupt `state.yaml` is rewritten rather than left broken (which would repeat recovery and keep volume commands unable
  to read the home).
- **Serialized RootState writes.** Every `RootState` read-modify-write goes through the shared locked transaction API,
  so `runtimemigration` and the upgrade path cannot clobber each other's map; the upgrade path acquires it non-blocking
  and defers when it is held (`project-state-transactions.md`).
- **No network request under the root lock.** The agent-version request runs with the root lock released; the lock is
  taken only to read the cached check and to persist the refreshed one, and the re-check under the lock keeps the
  single-write property when two processes' checks overlap.
- **A RootState read failure never writes.** A `RootState` read error must warn and return no offer without persisting,
  never overwrite the file with a zero-value document that would discard the `migrations` registry.
- **`--dry-run` skips the upgrade check.** The preview returns before the setup hub, so there is no upgrade network check,
  no state persistence, and no legacy fold, and `resolveBuildVersion` is never reached. (The broader
  no-side-effects contract lives in [dry-run-preview.md](dry-run-preview.md).)

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
- Reading the version from the image label is the source of truth for a present image; `HomeState.agent_version` is a
  cache that follows it and remains the fallback when the image is absent (see [agent-version-sync.md](agent-version-sync.md)
  and Baseline recovery).
- `config harden`, `upgrade.json`, and launcher self-upgrade are untouched.

## Docs and changelog

Update `docs/runner-image.md` and add a `CHANGELOG.md` `[Unreleased]` line. The existing PR #225 wording change to the
prompt is retained; only the source of `current` and the state layout change.
