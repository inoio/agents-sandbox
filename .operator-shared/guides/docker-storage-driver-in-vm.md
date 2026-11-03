# Docker storage driver inside the microsandbox VM

Reference for the runner image's forced `vfs` storage driver: why the others are unavailable, what the guest
filesystem actually is, and what spikes showed about the one viable alternative. `specs/image-composition.md`
owns the *contract* (the forced `vfs`, the daemon.json probe, the #287 flag rule); this guide owns the *why*
and the alternative-driver findings.

## The guest filesystem is virtiofs + overlay — there is no FUSE mount

`/proc/self/mountinfo` inside a project VM shows three virtiofs shares and the root overlay:

```
/                 overlay   upperdir=/.msb/rootfs/upperfs/upper  lowerdir=/.msb/rootfs/lower
/.msb             virtiofs  msb_runtime
/workspace        virtiofs  workspace_<id>
/home/dev         virtiofs  home_dev_<id>
/var/lib/docker   overlay   (a self-bind of /)
```

There is **no FUSE mount and no FUSE process** in the guest (`grep fuse /proc/self/mountinfo` is empty).
`stat -f` reports `fuse` for the virtiofs shares — that is microsandbox's `stat_virtualization` (default
`strict`, see `specs/workspace-bind-permissions.md`), not a real filesystem. Do not read the storage driver's
constraint off `statfs`; read it off `mountinfo`.

## Why not `overlay2`

Docker's graph root `/var/lib/docker` is on the guest root **overlay**, so `overlay2` needs a second overlay
whose upperdir sits on the first. The kernel rejects exactly that, with a specific message:

```
mount: fsconfig() failed: overlay: filesystem on <upperdir> not supported as upperdir.
```

Putting the data root on a virtiofs share instead does not help either — the kernel rejects virtiofs as an
overlay upper with a generic error, `wrong fs type, bad option, bad superblock` (EINVAL), and adding the
`userxattr` mount option does not change it. A `dockerd --storage-driver=overlay2 --data-root /home/dev/...`
fails at driver init with `failed to mount overlay: invalid argument` → `driver not supported: overlay2`, and
`--storage-opt overlay2.userxattr=true` is rejected as an unknown option. So *no* guest-accessible mount can
back `overlay2`.

## `fuse-overlayfs`: viable, verified

`fuse-overlayfs` sidesteps the kernel check entirely — it merges in userspace and exposes its own FUSE mount,
so neither the overlay-on-overlay nor the virtiofs-upper restriction applies. Its requirements are kernel FUSE
(present: `fuse` in `/proc/filesystems`), `/dev/fuse`, and the `fuse-overlayfs` binary; that binary links
libfuse3 and is **not** in the docker static tarball the runner installs, so the image must add
`fuse-overlayfs` + `libfuse3` (they are not present by default).

Docker compiles the driver in — dockerd's driver-priority string is `overlay2,fuse-overlayfs,btrfs,zfs,vfs` —
so `--storage-driver=fuse-overlayfs` is selectable, and a *rootful* daemon accepts it (it is not rootless-only).

**Spike result** (second dockerd, own `--data-root`/socket, `--storage-driver=fuse-overlayfs`):

- Direct `fuse-overlayfs` over guest-overlay dirs: mount, write, copy-up (a modified lower file landed in
  `upper/`), and umount all succeed.
- `dockerd --storage-driver=fuse-overlayfs` reached ready in ~1s; `docker info` reports
  `Storage Driver: fuse-overlayfs`; a `docker run` and a `docker build` with a `RUN` layer both succeed.

Performance against `vfs` was **not** measured; fuse-overlayfs here is FUSE stacked on the virtiofs-backed
root overlay, so a win over vfs's full-copy-per-layer is plausible but unproven.

## Spiking a second dockerd without killing the first

A second `dockerd` sharing the host network namespace removes `docker0` on shutdown, taking the running
daemon's bridge down with it (`adding interface veth… to bridge docker0 failed: Device does not exist`).
Restore as root with `ip link add name docker0 type bridge; ip addr add 172.17.0.1/16 dev docker0;
ip link set docker0 up`, then reattach any surviving `veth*` via `ip link set <veth> master docker0`. Safer:
run the second daemon in its own network namespace (`unshare -n`) — but then it has no registry access, so
`docker save` the image from the running daemon and `docker load` it into the second (`docker load` needs no
network).

Getting root in the VM is possible from the running daemon: a privileged, `--pid=host`, `--network=host`
container running `nsenter -t 1 -m -u -i -n -p sh` lands you as root in the VM's namespaces, with `/dev/fuse`
and the guest binaries on the host mount.

## The msb-level alternative

For kernel-speed `overlay2` inside the VM, microsandbox would need to expose a mount whose backing can serve
as an overlay upper with the overlay's `overlay.*` xattrs — none of the current mounts qualify (virtiofs is
rejected). It is not something msb provides today, and it is a microsandbox capability/design question, not
launcher-controllable. Until then `fuse-overlayfs` is the only copy-on-write option.
