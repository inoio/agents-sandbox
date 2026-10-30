---
description: Contract for the per-startup project-VM reconfiguration decision: how changed settings are detected via persisted fingerprints, which changes recreate the VM vs restart the daemon, the fingerprint-preservation invariant, and the #183 seeding fix that closes the state-write race, plus the per-project creation lock that also covers the recreate destroy (#397).
read_if: Changing resolveReconfigDecision/applyReconfigHomeAction/PlanReconfig, the env/secret/network/mount fingerprints, the reconfig prompt, the ensure-vm.lock scope, or diagnosing a spurious VM rebuild or "network policy changed" prompt.
---

# Project-VM reconfiguration decision

`PrepareSandbox` calls `resolveReconfigDecision` (`internal/sandbox/vm/reconfig.go`) once per startup to choose between
recreating the project VM, restarting the agent daemon, or doing nothing. It returns a named `reconfigDecision`
(`Recreate`/`Restart`/`HomeAction`/`ServeHostPort`) instead of a positional tuple; the home-volume action it resolves
is applied separately by `applyReconfigHomeAction`, keeping plan resolution free of home-volume side effects. The
decision is computed by `reconfig.PlanReconfig` and resolved by `reconfig.ResolveReconfig` (silent when alone, a
keep/quit[/restart] prompt when other clients are attached).

## Fingerprint-backed change detection

The microsandbox SDK's `Config()` does not round-trip env, secrets, or network when reading an existing VM back, so
those changes are detected against fingerprints persisted in the per-`(slug, agent)` `state.yaml` (`state.HomeState`).
Mounts are the exception: they are read live from the persisted config JSON (`specs/live-mount-readback.md`), and
`MountState` fingerprints only the user's extra host bind mounts. The fingerprints:

- `EnvState`, `SecretState`, `MountState` — content hashes. Empty applied means "no persisted state yet" and counts as
  changed **only when the desired setting is non-empty**.
- `NetworkState` — a `network.Policy.Fingerprint()` hash. Empty applied counts as changed **unconditionally**, to force
  one-time migration of VMs created before the deny-by-default network change (#86). A deny-bearing profiled policy's
  fingerprint additionally carries a `rule-order` marker, so a VM built before the egress-deny-before-profile-allow fix
  (#231) recreates once to adopt the corrected rule order; a policy whose emitted rules did not change keeps its
  historical hash and is not recreated. `BuildNetworkState` applies `Policy.Effective()`, so an unset profile fingerprints
  as the `public` default; `Fingerprint` sorts its lines, so profile and list order do not matter. Profile composition and
  its fingerprint-stability rule are in `specs/network-profiles.md`.

The host env and secret files are read once per run (`envsecret.LoadEnvAndSecrets`, in `PrepareSandbox`) and the one
`envsecret.EnvSecrets` value is threaded to the fingerprint seed, `resolveReconfigDecision`, and VM creation. The
persisted `EnvState`/`SecretState` hashes and the host env and secrets baked into a newly created VM therefore come from
the same read and cannot disagree. The image's Dockerfile `ENV`, which `buildProjectVMEnv` merges on top of that host env
(winning on collisions), is not part of the fingerprint; a change there is caught by the image-digest recreate path. The
loader's warnings are emitted once instead of once per consumer (#342).

A read of an existing VM's persisted config can fail (`msb.SandboxHandle.Config()`), and `findProjectVM` can fail to
locate a VM it cannot confirm absent. Both are treated fail-closed by `resolveReconfigDecision`: a non-not-found
lookup error is returned (the run aborts) rather than treated as "no VM"; and a `Config()` failure is surfaced as a
warning and forces a recreate through the normal `ResolveReconfig` path (a `unreadable VM config` change label, so an
attached client still gets the keep/quit prompt). Without this, `PlanReconfig` sees `cfg == nil`, returns an empty
plan, and every image/env/secret/network/mount trigger is silently skipped — leaving the VM running stale config
(#339). `PlanReconfig`'s `cfg == nil` early return stays for the genuine no-VM case only. A `ConfigMounts()` failure in
`applyLiveMounts` is deliberately different: it degrades to pre-#223 detection rather than forcing a recreate.

Fingerprints are written by `persistConfigHashes`, which runs only when the VM was created this run (`boot ==
vmBootCreated`). A fresh home volume's state is additionally seeded with the desired fingerprints at creation
(`volume.Manager.EnsureNewHome`), so a `HomeState` is never fingerprint-less while a VM exists (see Resolution below).

## Invariant: preserve fingerprints on every state rewrite

Every path that rewrites a project's `HomeState` must carry the existing env/secret/network/mount fingerprints forward,
because an absent fingerprint is treated as "changed". `volume.operations.volumeOp`, `volume.Manager.ApplyHomeAction`,
and `volume.Manager.RecordHomeImage` all preserve them; `volume.Manager.EnsureNewHome` seeds them on a fresh state.
Dropping them makes the next run rebuild the VM and blame a setting (e.g. the network policy) that did not change.

## Determinism

Change detection is order-independent by design: `state.SecretsContentHash`, `state.EnvContentHash`, and
`network.Policy.Fingerprint` all sort their inputs before hashing, and `reprovision.AgentConfigEqual` compares the merged
and mirror files by map lookup. The helper outputs feeding them are also kept deterministic: `reprovision.ConfigFiles.Keys`
has a single ordering authority (the merged config leads when snippets exist, the remainder sorted; callers must not
re-sort) and `envsecret.BuildSecretsFromSpecs` emits entries in sorted `EnvVar` order. Map iteration order therefore never
reaches the decision — a periodic spurious rebuild was the state-write race resolved below, not input ordering.

## Resolution (issue #183)

The spurious change came from `EnsureNewHome` writing a fingerprint-less fresh `HomeState` outside the `ensure-vm.lock`
flock, while `persistConfigHashes` ran only after VM creation. A second session could read the fingerprint-less state
after the first session created the VM but before it persisted fingerprints, and then see a bogus "network policy" (and
env/secret/mount) change — a silent rebuild when alone or a rebuild/restart prompt when another client was attached.

The invariant now held is: **a `HomeState` is never fingerprint-less while a VM exists.** The desired fingerprints are
computed once (`vm.desiredFingerprints`) and seeded as part of the fresh-home write: `volume.Manager.EnsureNewHome`
applies `state.Fingerprints` to the new `HomeState` in one atomic write, and `persistConfigHashes` persists that same
value, so the seeded and persisted sets cannot diverge. Because state resolution runs before VM creation, the written
state already matches the desired config by the time any session can observe the VM, so the empty-applied "changed" rule
can never fire spuriously. This closes the concurrent window and the crash route (a process killed before
`persistConfigHashes` now finds a seeded state).

The deleted-state route is now handled by home-volume recovery (issue #219): when `state.yaml` is deleted while the VM
and its volume survive, `ResolveHomeVolume` adopts the volume the VM still mounts and seeds the recovered state with the
desired fingerprints, so no duplicate volume is minted, the old volume is not orphaned, and `homeVolumeChange` does not
fire. When a prior state is still readable but its recorded volume is gone, that state's fingerprints are carried
forward instead. See `specs/home-volume-recovery.md`.

The trade-off: the seed write briefly asserts "applied" before the VM exists. If creation then fails the state overstates
what is baked in, which is harmless because with no VM `PlanReconfig` short-circuits to no recreate. Recreated VMs (a
genuine config change against an existing home) still refresh fingerprints via `persistConfigHashes` after creation; the
state is never fingerprint-less on that path, so the change detected there is real.

The per-`(slug, agent)` creation flock (`ensure-vm.lock`) now also covers a recreate's teardown (#397); the identity
contract governing that destroy is in [sandbox-identity.md](sandbox-identity.md).

## Live mounts and the size/quota/home detectors (issue #223)

`tmpSizeChange`, `workspaceQuotaChange`, and `homeVolumeChange` compare against the live VM's mounts. The launcher
reads those from the persisted config JSON through `msb.SandboxHandle.ConfigMounts()`
(`specs/live-mount-readback.md`), because the SDK's `Config()` leaves `cfg.Volumes` empty on every read-back.
`resolveReconfigDecision` applies that read before calling `PlanReconfig`, so the three detectors see the VM's real `/tmp`
(`size_mib`), `/workspace` (`quota_mib`), and `/home/dev` (`Named.name`) mounts. `rootDiskChange` reads
`cfg.RootDisk`, which does round-trip, and is unaffected.

Until #223 landed, all three compared against an always-empty `cfg.Volumes` and never fired, so a changed `tmp_size`
or `workspace-quota` on an existing VM was silently ignored until an unrelated recreate — behaviour the launcher's own
docs already describe as triggering recreation.
