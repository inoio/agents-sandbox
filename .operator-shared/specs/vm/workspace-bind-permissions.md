---
description: Contract for the /workspace bind mount's host-permission policy — guest rwx bits mirror to the real host inode (host-permissions=mirror) so host git trees stay consistent, while ownership/setuid are never propagated and configured mounts keep the private default.
read_if: Changing the /workspace bind mount options in buildMounts, diagnosing host-vs-VM permission (exec-bit) divergence or dirty host git trees, or reasoning about whether a VM recreate is needed to apply a mount-permission change.
---

# Workspace bind-mount permissions

## Contract

`/workspace` is bound with `host_permissions=mirror` (see `buildMounts`, `internal/sandbox/vm/vm_lifecycle.go`).

- microsandbox's default for a bind is `stat_virtualization=strict`, `host_permissions=private`: guest `chmod` is stored
  only in the `user.msb.override_stat` xattr, and guest-created/rewritten host files land at the owner floor (`0o600`
  files / `0o700` dirs). The guest still sees its virtual mode, so an in-VM `git checkout`/`reset`/`stash` that rewrites a
  tracked executable leaves the VM tree clean while the host file loses its exec bit — `git diff` on the host then reports
  `100755 → 100644` (git only distinguishes exec vs non-exec), and the guest cannot add the bit back.
- `mirror` makes guest `create`/`mkdir` and explicit `chmod` call `fchmod` on the real host inode. It mirrors ordinary
  `rwx` bits only: setuid/setgid, uid/gid, file type, and device ids are never propagated, and an owner-access floor
  (`0o600`/`0o700`) is always OR'd in, so the host process cannot be locked out.

## Scope

- Only the managed `/workspace` bind sets `mirror`. Configured user mounts (`opts.Mounts`) keep the SDK default
  (`HostPermissions` unset → `private`), and the home volume and `/tmp` are unaffected.
- `stat_virtualization` stays `strict`; identity virtualization (the isolation claim) is unchanged.

## Consequences

- The guest can widen group/other `rwx` bits on host files in `/workspace` (the owner floor only prevents narrowing), so
  on a shared host the mount is no longer privacy-preserving for mode bits.
- The guest can mark files executable on the host, including `.git/hooks/*`, which the host's git executes. `.git` is
  guest-writable by design (hiding is blocked upstream, microsandbox #1448); this raises the bar for running an
  untrusted agent against a repo whose `.git` is not trusted (see issue #39).
- Mirroring `chmod`s the real host inode. An in-VM permission change on a host file the launcher process does not own now
  fails (EPERM) instead of being virtualized.

## Migration caveat

The change applies to newly created project VMs. Existing VMs were created with `private` and are **not** recreated by
this change: the SDK's `Config()` does not round-trip volumes when reading an existing VM back (`state.MountsChanged`
doc), and the managed `/workspace` bind is not part of the persisted `MountState` fingerprint (which covers
`opts.Mounts` only), so `PlanReconfig` detects nothing. A VM recreated for any other reason (image, env/secret, network,
configured mounts, …) picks up `mirror`. If applying it to existing VMs is required, the live `/workspace` bind's
`host_permissions` is now readable (`specs/live-mount-readback.md`), so a comparison against it is possible; the
alternative is a dedicated one-time fingerprint in `HomeState` (seeded like `NetworkState`, empty-applied = changed).
Before #223 the `/workspace` bind was invisible on a read-back and neither route could fire.
