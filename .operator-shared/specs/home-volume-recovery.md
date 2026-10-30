---
description: Contract for recovering a project's home volume when its state.yaml is lost — reuse the live VM's mounted volume instead of minting a duplicate and orphaning the original.
read_if: Changing home-volume resolution (volume.Manager.ResolveHomeVolume / recoverOrCreateHome), liveHomeVolume in the vm package, or diagnosing a duplicate/orphaned home volume after state loss.
---

# Home-volume recovery on lost state

A project's `state.yaml` is the record of which home volume its VM mounts. It can go missing without the VM or the
volume disappearing: deleted by hand, a partial/corrupt write, or a crash between volume creation and the state write
(`EnsureNewHome` creates the volume, then writes state). Previously `ResolveHomeVolume` treated absent state as
"create fresh" and `EnsureNewHome` always minted a new timestamped `HomeVolumeName`, so the next start created a
duplicate volume, orphaned the previous one (only reclaimable by a manual `volume remove`/`prune`), and forced a
rebuild via `homeVolumeChange` (#219).

## Invariant: reuse a surviving volume, never silently duplicate

Home-volume resolution reuses an existing volume when one can be found, and only creates a fresh prefill when it
cannot. Resolution order (`volume.Manager.ResolveHomeVolume`):

1. **State** — if `state.yaml` names a volume that still resolves, reuse it (unchanged).
2. **Live VM** — when state is absent or its recorded volume is gone, adopt the home volume the project VM still
   mounts, if that volume still exists. The mount is read from the VM's persisted config JSON as the `Named` mount at
   `mounts.VMHomeDir` (`msb.SandboxHandle.ConfigMounts()`, `specs/live-mount-readback.md`); the vm package supplies it
   via `liveHomeVolume` (`client.GetSandbox(projectVMName(k))`), because the volume package cannot import vm.
3. **Fresh prefill** — otherwise `EnsureNewHome` creates a new volume, as before.

A fresh prefill that fails removes the volume it just created before returning the error (the same cleanup
`ApplyHomeAction` performs), so an unreferenced volume is never left behind and each retry does not accumulate one
(#427). A fresh prefill whose final state write fails does the same — it surfaces the error and removes the
uncommitted volume — so a run never proceeds against a volume with no state record; the volume is kept when the
write error is an installed-but-not-durable document (`state.WriteInstalled`), because the state that already names
it must not point at a removed volume (#428).

Absence is only the SDK's `ErrVolumeNotFound` kind (`msb.IsVolumeNotFound`). Any other lookup error — daemon,
timeout, permission, cancellation — **fails closed**: resolution returns an error and performs no create, prefill, or
state rewrite, so a transient error can never be mistaken for a gone volume and orphan the real one (#331). Only a
true not-found takes the recovery/fallback path below.

A state written by a VM-only identity adoption (#144) records a pending `recovered_home_volume` and no
`home_volume` yet. That field is recovered before the live-VM branch: the volume is reused and the state is
rewritten with the current digest and seeded fingerprints. Unlike the live-VM branch it does not consult the
VM, because adoption destroyed it; the durable field is what preserves the volume. Recovery also retires the
adoption's ownership claim under the legacy identity (`state.ClearAdoptedHomeVolume`), which is what keeps a
second clone from adopting the same volume; see `checkout-identity.md`.

`liveHomeVolume` originally read `cfg.Volumes[mounts.VMHomeDir].Named`, which the SDK always leaves empty on a
read-back, so until #223 this branch never fired and recovery always fell through to a fresh prefill (a silent
duplicate volume, the very failure #219 was filed for).

The live VM config is authoritative for which volume is *current*, unlike probing the volume list: a project can
legitimately own several home volumes (migrate/reset keep the old one), so a name-prefix match is ambiguous. Adopting
the VM's mount is therefore preferred over any list-based probe.

Recovery rewrites `state.yaml` to the recovered volume (with the current image digest) and emits a warning naming the
recovered volume.

## Fingerprints

A recovered `HomeState` is never fingerprint-less while the VM exists (the #183 invariant). Fingerprint handling depends
on which branch triggered recovery:

- **State lost entirely** — no prior fingerprints exist, so recovery seeds the desired fingerprints
  (`fingerprints.Apply`), exactly as `EnsureNewHome` does (#183). No spurious rebuild fires; as with the fresh-home
  seed, a live VM whose config differs from the desired one is masked (the trade-off #183 accepted).
- **State readable but its recorded volume gone** — the state's fingerprints are still valid (they describe the live
  VM, not the home volume), so they are carried forward into the recovered state, matching `ApplyHomeAction`'s
  preservation invariant (`specs/reconfig-decision.md`). A genuine config change is still detected against them.
- **No recoverable volume** — falls back to `EnsureNewHome`, which seeds the desired fingerprints (#183).

## Non-interactive

Recovery is silent (a warning, no prompt) and unconditional whenever a mount is recoverable, including under
`--dry-run-vm`. It introduces no new deletion: the original volume is never removed, only re-referenced.
