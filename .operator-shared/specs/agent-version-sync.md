---
description: Contract for keeping the per-project `HomeState.agent_version` baseline in sync with the agent release actually baked into the runner image: the image label is the authority, `agent_version` is a cache, the image-ensuring paths are the writers of record, and recovery reconciles a disagreeing cache, not only an empty one.
read_if: Changing `resolveBuildVersion`, `recordImageProvenance`, `recoverAgentVersion`, `PrepareSandbox`'s version handling, the release resolution (`agent.LatestVersion`, `image.resolveAgentVersion`, `vm.agentLatestVersion`), `image.EnsureImage`/`image.Build`/`ImageInfo`, the `agent-version` image label, or `--agent-version`; diagnosing a run that silently rebuilds the runner image to an older agent release than it already holds, a baseline that stays empty after deletion, or an accepted upgrade (e.g. opencode2) that reverts on the next run.
---

# Agent-version baseline synchronization

## Problem

`state.HomeState.agent_version` is a project's upgrade baseline: the `current` the prompt compares against
(`vm.pendingUpgrade`) and the version `vm.resolveBuildVersion` hands the build. It is treated as authoritative, but
the two writers meant to keep it — `vm.recordImageProvenance` on boot and `vm.recoverAgentVersion` when it is empty —
do not include the path that actually bakes the version. A successful build of release V does not record V unless a VM
boot happens *and* the profile can self-detect it.

Two symptoms follow:

- **Silent downgrade after an accepted upgrade.** The baseline is not updated, so the next run computes a
  `dockerfile-id` from the stale version, sees it differ from the image's, and rebuilds the image *down* to the stale
  release (`image.buildReasons` reports "agent version changed"). Because the field is authoritative, the divergence
  pulls the image toward the field instead of the field toward the image.
- **A baseline that never heals.** A deleted (or otherwise unrecorded) baseline is restored only when a present Docker
  image still carries a real-release label at that moment; otherwise the run builds and boots without writing it back,
  and the `build`/`run` paths never fill it either.

Every built-in profile implements `agent.VersionProvider`, so `detectToolAgentVersion` returns a version and
`recordImageProvenance` writes `agent_version` on boot. The failure this contract guards against is a profile without
one: its baseline is written only by recovery, while a fresh or repaired state deliberately leaves the field empty
(`volume.EnsureNewHome`, `volume.recoveredHomeState`) on the assumption of a boot self-heal it cannot perform.

## Contract

### Release resolution

- The agent's newest release resolves in one place, `agent.LatestVersion` (the agent's `UpgradeChecker` capability, empty
  for an agent that lacks one): `image.resolveAgentVersion` adds only the requested-version short-circuit on top of it,
  and `vm.agentLatestVersion` aliases the same function for the upgrade check. The two seam vars stay separate on purpose
  — no single seam is reachable from every entry path (`build`/`image build` and the `volume` ops call `image.EnsureImage`
  with no `vm` layer) and `cmd`'s fail-fast test isolation installs the `vm` seam — so do not collapse them into one.

### Authority and cache

- The authority for the release an *existing* image holds is the image itself: the
  `org.agents-sandbox.identity.agent-version` label baked at build time. A non-release sentinel (`none`,
  `user-provided`) means "no release".
- `state.HomeState.agent_version` is a **cache** of that label. It exists so the prompt and the build decision have a
  baseline when the image is absent (pruned, host docker cleaned), without a separate probe on the pure prompt path.

### Writers of record

- The paths that bake the version — `run`/`shell` via `PrepareSandbox`→`image.EnsureImage`, and `build`/`image build`
  via `image.Build` — record the effective baked version to `HomeState` through the locked transaction API
  (`state.WithHomeState`).
- The effective version is the release the ensured image reports through its `agent-version` label (which the call
  already inspects), including the release resolved at build time when `BuildOptions.AgentVersion` is empty
  (`image.initialBuildVersions`/`resolveAgentVersion`). It must be surfaced to the caller rather than discarded;
  `image.ImageInfo` carries it beside `DockerTag`/`Ref`/`Digest`/`Env`.
- The record is skipped for a non-release sentinel, and a write happens whether or not a rebuild occurred, so a reused
  image reconciles its cache and a pin persists.

### Reconciliation

- An empty baseline is recovered from the image label (unchanged).
- A non-empty baseline that **disagrees** with a present image's label is stale: the label wins and the cache is
  rewritten. A disagreeing cache is never a reason to rebuild the image back to the cached release.
- Reconciliation runs in `vm.resolveBuildVersion` before the upgrade check and the build decision, so a stale cache
  can never select an older build. It reads only a local image label, adding no network call.
- When no image is present, the cache is authoritative (offline reuse, pruned image).

### Pins

- `--agent-version <v>` is recorded when `v` is baked, so a later unpinned run keeps `v` until a newer release is
  accepted or another pin is set. A pin stops being a one-shot override.

### Boot provenance

- `recordImageProvenance` remains a boot-time reconciler, but it must not be the only writer for any profile. Every
  built-in profile implements `agent.VersionProvider` (in-VM detection) alongside the image label, so the "left empty on
  a fresh/repaired state, self-healed on boot" assumption holds for all of them. `TestRegisteredAgentsHaveVersionSources`
  (`internal/agent/version_sources_test.go`) iterates the registry and fails for a profile without an `UpgradeChecker`,
  an `ImageSpec.VersionArg` or a `VersionProvider` (#453).

## Invariants

- After any successful `run`/`shell`/`build`, `HomeState.agent_version` equals the runner image's baked release, or is
  empty only for a genuine first run (no image), a user-provided agent, or a non-release-sentinel image.
- A recorded baseline never causes the image to be rebuilt to an older release than it already holds.
- A pin is persistent: once baked, it survives later unpinned runs until a newer release is accepted or it is
  superseded by another pin.
- The cache is never the only record: it is reconstructible from the image label whenever the image is present.
- Baselines stay per `(slug, agent)`; no global or cross-project contamination (`upgrade-state-scoping.md`).
- Reconciliation does not recreate or re-import a VM; it is a cache write only.

## Non-goals

- No new network probe; reconciliation reads a local image label, like the build decision's own inspect.
- Correcting the `volume` subcommands' image path (they resolve the latest release rather than the recorded baseline) is
  a separate concern; this contract only requires that whatever they bake is recorded.
- Not moving the baseline back to a machine-global document.
- No change to how `RootState` (the machine-global network cadence and cached `latest_version`) is scoped.

## Spec amendments

- `upgrade-state-scoping.md`: for an **existing** image the label is the authority and the cache follows it;
  `agent_version` stays the fallback only when the image is absent. Baseline recovery widens from "empty only" to
  "empty or disagreeing". Its prompt-scoping intent (the prompt's `current` is always this project's) is unchanged.
- `runner-image-build.md`: "Version selection" gains the writer-of-record clause and the requirement that
  `EnsureImage`/`Build` surface the baked version to their caller.

## Acceptance

- opencode2: accept an upgrade to V; the next run reuses the image and does not rebuild, and `agent_version == V`.
- `run --agent-version V` followed by an unpinned `run`: the image stays on V.
- Delete `agent_version` with the image present, then run: the field is rewritten to the image's release.
- `build --agent-version V` followed by `run`: no downgrade.
- Every built-in profile (`opencode`, `opencode2`, `pi`, `claude-code`, `codex`, `hermes`) records its baseline on boot;
  recording adds an in-VM `--version` exec, never a network request.
