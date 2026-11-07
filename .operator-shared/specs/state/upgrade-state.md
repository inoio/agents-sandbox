---
description: Contract for the agent upgrade-check and provenance state - machine-global network facts (`RootState.updates.agents`: cadence + cached `latest_version`) split from per-(slug, agent) image facts (`HomeState`: `agent_version`, `agent_source`, `docker_source`, `offered_versions`); the runner image's `agent-version` label is the authority and `agent_version` is a cache that the image-ensuring paths reconcile, so the prompt can never name another project's version and a stale cache never rebuilds the image to an older release.
read_if: Changing the agent upgrade prompt, `pendingUpgrade`/`resolveBuildVersion`/`recoverAgentVersion`/`recordImageProvenance`, `PrepareSandbox`'s version handling, the release resolution (`agent.LatestVersion`, `image.BuildOptions.ResolveVersion`, `prepare.agentLatestVersion`), `image.EnsureImage`/`image.Build`/`ImageInfo`, the `agent-version` label, `--agent-version`, or the legacy `updater.yaml` migration; or diagnosing a prompt that names the wrong project's version, a run that silently rebuilds to an older release, a baseline that stays empty after deletion, or an accepted upgrade that reverts on the next run.
---

# Agent upgrade and provenance state

## Scope split

Two documents, split by application:

**Machine-global `<root>/state.yaml` (`state.RootState`)** — launcher-wide, network-facing:

- `migrations` (unchanged).
- `updates.agents: map[agentName]AgentCheckState` with `last_checked` (when the releases endpoint was last successfully hit for this agent — one cadence for the whole machine, so N projects do not each call GitHub) and `latest_version` (the cached newest release, so a project prompt can compare without hitting the network).

**Per `(slug, agent)` `<root>/<slug>/<agent>/state.yaml` (`state.HomeState`)** — describes this project's image only:

- `agent_version` — the agent release baked into this project's image (the upgrade baseline; the value the prompt shows as "current").
- `agent_source` — `tool` | `user`, read from the image's provenance file.
- `docker_source` — `tool` | `user`.
- `offered_versions` — releases whose rebuild prompt this project has already shown (the "don't nag" gate).

`updater.yaml` is retired.

**Why per-agent, not per-project:** the runner image and its version are per `(slug, agent)`; a project-wide `offered_versions` set would wrongly suppress agent B's prompt once agent A had offered the same version. The per-agent file is also the only project-scoped state document that exists; no new project-level file is introduced.

## Upgrade check (`prepare.resolveBuildVersion` → `prepare.pendingUpgrade`)

`resolveBuildVersion` takes the project `state.Key` (available in `PrepareSandbox`) and reads `current`/`agent_source` from that key's `HomeState` instead of a global file.

`pendingUpgrade(ctx, a, k, ui, current)`:

1. Read the global root state under a non-blocking root transaction. On a read error or a held lock, warn and return no offer and do **not** persist: an unreadable document (e.g. a corrupt `state.yaml`) must never be overwritten, so the `migrations` registry and every other agent's cached check survive.
2. Read the global `AgentCheckState` for `a`.
3. If the check is due (`state.AgentCheckState.DueForCheck`: never checked, or at least `state.AgentCheckInterval` = 24h since the last check): **release the lock and call the agent's `LatestVersion` outside it**, so a slow request never serializes unrelated projects (adoption, the runtime-home copy campaign) behind one agent's egress check. On error, warn and return no offer (the window stays open). On success, re-acquire the lock (non-blocking) and re-check `DueForCheck` before persisting: if another process refreshed the window while the request ran, skip the write and use its cached entry, so two concurrent due checks persist at most one refresh. A held lock or unreadable state defers the step; a write failure is warned but the freshly fetched version is still offered this run.
4. `latest := cached latest_version`; if empty, no offer.
5. Compare `NewerThan(latest, current)`. If not newer, no offer.
6. Claim `latest` for this project: inside a single `HomeState(k)` transaction, check whether `offered_versions` already contains `latest` and, if not, append it (an update-only write; no state is created) and return the offer. A single transaction means two concurrent launches of the same project cannot both pass the gate and prompt. Because this runs before home resolution, `resolveBuildVersion` also returns the offered release so `PrepareSandbox` can record it again after home resolution recreates a missing state; otherwise a "don't remind again" choice would be forgotten for a project whose state file was lost.

The prompt (`promptUpgrade`) then names both `latest` and this project's `current` (`"Rebuild the runner image with <agent> <latest>? (current image has <current>)"`; the Rebuild choice reads `"Upgrade <agent> <current> → <latest>"`). Because that `current` is read from the project's own file, it cannot name another project's version.

Under `--dry-run` (`ModePreview`), `resolveBuildVersion` is never reached: the session returns from the preview before it calls the setup hub. There is likewise no recorded-version substitution and no verbose dry-run line. The full `--dry-run` contract is `../architecture/dry-run-preview.md`.

## Release resolution

- The agent's newest release resolves in one place, `agent.LatestVersion` (the agent's `UpgradeChecker` capability, empty for an agent that lacks one): `image.DefaultAgentVersionResolver` adds only the requested-version short-circuit on top of it, and `prepare.agentLatestVersion` aliases the same function for the upgrade check. The build resolver is injected (`BuildOptions.ResolveVersion`; see `../architecture/client-injection.md`), supplied by `cmd.agentVersionResolver` for `build`/`image build`/the `volume` ops and by `prepare.buildVersionResolver` for a run; `prepare.buildVersionResolver` defaults to `prepare.agentLatestVersion`. The two lookups stay separate on purpose — no single resolver is reachable from every entry path (`build`/`image build` and the `volume` ops call `image.EnsureImage` with no `vm` layer) — so do not collapse them into one.

## Authority and cache

- The authority for the release an *existing* image holds is the image itself: the `org.agents-sandbox.identity.agent-version` label baked at build time. A non-release sentinel (`none`, `user-provided`) means "no release".
- `state.HomeState.agent_version` is a **cache** of that label. It exists so the prompt and the build decision have a baseline when the image is absent (pruned, host docker cleaned), without a separate probe on the pure prompt path.

### Baseline recovery from the image label

A pre-existing project may have no `HomeState.agent_version` on its first run (e.g. after the migration dropped a global version). The run decision happens *before* the boot that `recordImageProvenance` could record it, so without recovery `current == ""` and the build resolves the latest release: the existing image is silently rebuilt to latest (no prompt), and offline the launch fails before it can reuse the image.

To close that gap, `resolveBuildVersion` recovers the baseline from the project's own existing image before giving up: `image.ExistingAgentVersion(ctx, slug, agent)` reads the local runner image's `agent-version` label (a `docker inspect`, no network) and returns the release, or nothing for an absent image, a pre-label image, or a non-release sentinel. When it returns a version, `recoverAgentVersion` records it to `HomeState.agent_version` and the normal check/prompt runs against it. The record is written only when a state file already exists (a `WithHomeState` transaction that writes nothing when `exists` is false), because recovery runs before home resolution: creating a state here would leave a fingerprint-less partial state that the home-recovery step would adopt as a `prior` and copy empty fingerprints forward, spuriously detecting a config change. A genuine first run (no image, no label) still returns `""` and resolves at build time.

The label is the authority for an existing image and `HomeState.agent_version` is a cache that follows it. Recovery widens from "empty only" to "empty **or disagreeing**": a recorded value that disagrees with a present image's label is stale, the label wins, and the cache is rewritten, so a stale record can never pull the build toward an older release. The recorded cache stays authoritative only when the image is absent (pruned, host docker cleaned), which keeps offline reuse working. The label is consulted whenever the image is present.

## Writers of record

- The paths that bake the version — `run`/`shell` via `PrepareSandbox`→`image.EnsureImage`, and `build`/`image build` via `image.Build` — record the effective baked version to `HomeState` through the locked transaction API (`state.WithHomeState`).
- The effective version is the release the ensured image reports through its `agent-version` label (which the call already inspects), including the release resolved at build time when `BuildOptions.AgentVersion` is empty (`image.initialBuildVersions`/`BuildOptions.resolveAgentVersion`). It must be surfaced to the caller rather than discarded; `image.ImageInfo` carries it beside `DockerTag`/`Ref`/`Digest`/`Env`.
- The record is skipped for a non-release sentinel, and a write happens whether or not a rebuild occurred, so a reused image reconciles its cache and a pin persists.

### Boot provenance

`SetUpSandbox` threads the project `state.Key` into `recordImageProvenance` (the caller already holds `k`). On boot it reads `HomeState(k)` and updates it in place — `agent_source`/`docker_source`, and for a tool-installed agent the version the `VersionProvider` detects, written to `agent_version` — so fingerprints and the offered set survive. It is a no-op when no state file exists, like every other pre-home-resolution writer. Warnings remain non-fatal.

It must not be the only writer for any profile: every built-in profile implements `agent.VersionProvider` (in-VM detection) alongside the image label, so the "left empty on a fresh/repaired state, self-healed on boot" assumption holds for all of them. `TestRegisteredAgentsHaveVersionSources` (`internal/agent/version_sources_test.go`) iterates the registry and fails for a profile without an `UpgradeChecker`, an `ImageSpec.VersionArg` or a `VersionProvider`. `TestAgentVersionFollowsTheImageLabel` (`cmd`) pins the label authority end to end for opencode2: after `build` and `run` the cache equals the label, and an offline unpinned run with a stale older cache neither rebuilds toward it nor keeps it.

## Reconciliation

- An empty baseline is recovered from the image label.
- A non-empty baseline that **disagrees** with a present image's label is stale: the label wins and the cache is rewritten. A disagreeing cache is never a reason to rebuild the image back to the cached release.
- Reconciliation runs in `prepare.resolveBuildVersion` before the upgrade check and the build decision, so a stale cache can never select an older build. It reads only a local image label, adding no network call.
- When no image is present, the cache is authoritative (offline reuse, pruned image).

### Pins

`--agent-version <v>` is recorded when `v` is baked, so a later unpinned run keeps `v` until a newer release is accepted or another pin is set. A pin stops being a one-shot override.

## Migration (non-interactive, legacy-file-triggered)

The presence of `<root>/updater.yaml` is the trigger. No user choice, no `migrations` registry entry.

- Read the legacy file with the existing parser (including the flat-field fold). For each agent, if the global `AgentCheckState.last_checked` is **unset**, copy the legacy `last_checked` over; an already-present value wins.
- `latest_version` is not carried (the legacy file never stored it).
- `current_version`/`agent_source`/`docker_source`/`offered_versions` are not carried: they cannot be attributed to a project from a global, agent-keyed file. Each project re-detects its version and source on next boot, and its offered set resets, costing at most one extra prompt.
- After the root-state write succeeds, delete `updater.yaml`. A missing, empty, or unparsable file folds nothing but is still deleted.
- Best effort and silent: run once per startup before the upgrade check, under the same shared `RootState` flock as the upgrade path (non-blocking; skipped if held), and skipped entirely under `--dry-run`. On any failure, warn and continue, leaving `updater.yaml` for a later attempt.

## Invariants

- **No cross-project version.** The prompt's `current` is always read from the launched project's `HomeState`.
- **Split by application.** `RootState` holds only machine-wide facts (migrations, per-agent network cadence/cache); image-describing facts live only in the per-agent `HomeState`.
- **HomeState rewrites preserve upgrade fields.** Every writer must carry `agent_version`, `agent_source`, `docker_source`, and `offered_versions` forward. The read-modify-write writers do so by construction; the writers that rebuild a fresh `HomeState` and copy only the fingerprints must be extended to copy the upgrade fields too: `volume.operations.volumeOp`, `volume.Manager.ApplyHomeAction`, and `volume.Manager.recoveredHomeState` (when a prior state exists). A volume op or a recovered volume does not change the image, so dropping the fields would falsely reset the baseline and the offered set.
- **No silent rebuild; offline reuse.** A project with an existing runner image never silently rebuilds to the latest release: the image's `agent-version` label is the baseline for a present image (recovered when empty and reconciled when it disagrees), so the normal prompt fires and a disagreeing cache cannot select an older build. An offline check reuses the existing image.
- After any successful `run`/`shell`/`build`, `HomeState.agent_version` equals the runner image's baked release, or is empty only for a genuine first run (no image), a user-provided agent, or a non-release-sentinel image.
- A pin is persistent: once baked, it survives later unpinned runs until a newer release is accepted or it is superseded by another pin. The cache is reconstructible from the image label whenever the image is present, and reconciliation does not recreate or re-import a VM (a cache write only).
- **Migration never loses the cadence.** `last_checked` is folded only when unset; the legacy file is deleted only after a successful root-state write.
- **Serialized writes.** Every `HomeState`/`RootState` read-modify-write goes through the locked transaction API and preserves fields the caller does not own (`specs/state/state-model.md`); the upgrade path acquires the root transaction non-blocking and defers when held.
- **No partial state before home resolution.** Writers that run before the home volume is resolved (baseline recovery, the offered set) never create a state, so a state that exists at home resolution is never fingerprint-less by construction. Home recovery replaces a malformed state file wholesale under the lock.
- **No network request under the root lock.** The agent-version request runs with the root lock released; the lock is taken only to read the cached check and to persist the refreshed one, and the re-check under the lock keeps the single-write property when two processes' checks overlap.
- **A RootState read failure never writes.** A `RootState` read error must warn and return no offer without persisting, never overwrite the file with a zero-value document that would discard the `migrations` registry.
- **`--dry-run` skips the upgrade check.** The preview returns before the setup hub, so there is no upgrade network check, no state persistence, and no legacy fold.

## Edge cases

- First run after the change for a *pre-existing* project: `HomeState` has no `agent_version`, but the existing image carries the label, so `current` is recovered, the prompt fires, and the baseline is persisted. No silent rebuild.
- A genuine first run (no existing image): no label to recover, `current == ""`, no prompt; the build resolves the latest release, and boot writes the baseline.
- A project whose `state.yaml` was lost but whose image still exists: the baseline is recovered from the image label for this run but not persisted (no state file), so no partial state is created; home recovery then writes the state and a later run re-recovers the baseline to persist it.
- Legacy `updater.yaml` present but global `last_checked` already set: no fold, file deleted.
- A freshly folded `last_checked` intentionally defers the first check: if the legacy timestamp is recent, the first post-migration run is not due, so no network fetch happens, `latest_version` stays empty, and no prompt fires until the 24h window expires.
- Concurrent launches: the shared flock serializes `RootState` writes and the fold; a held lock (e.g. an in-flight runtime-home copy campaign) makes the upgrade step and fold skip this run and retry next run.
- `--dry-run`: `resolveBuildVersion` is skipped, the fold does not run, `updater.yaml` is left for a real run.

## Non-goals

- No new project-level (cross-agent) state file. "Project-only" state means the existing per-agent `<slug>/<agent>/state.yaml`.
- No project identity data (origin URL, workspace path, recency) is introduced.
- No `migrations` registry entry; the legacy file itself is the trigger.
- No prompt-order change; the prompt still fires before the image is built.
- No new network probe (reconciliation reads a local image label). Correcting the `volume` subcommands' image path (they resolve the latest release rather than the recorded baseline) is a separate concern; this contract only requires that whatever they bake is recorded. Not moving the baseline back to a machine-global document.
- `config harden`, `upgrade.json`, and launcher self-upgrade are untouched.
