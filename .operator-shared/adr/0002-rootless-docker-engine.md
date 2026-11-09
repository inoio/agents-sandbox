---
description: Decision to run the agent's Docker engine rootless as `dev` (rootlesskit + `dockerd-rootless.sh`) instead of a rootful `dockerd` reached through the `docker` group; covers the missing-prerequisite policy (fail the build), the data root on the VM root disk, the socket path and `/var/run/docker.sock` symlink, the `/dev/fuse` and `/dev/net/tun` permission fix, and where each prerequisite comes from.
read_if: Changing how the in-VM Docker engine is installed, started, or reached (`dockerAdoptionBlock`, `vm/dockerd.go`), its prerequisites or storage driver, the docker socket path, or evaluating podman/a daemonless builder/an authz proxy instead.
---

# Run the agent's Docker engine rootless

Date: 2026-11-08. Issue #487.

Status: Proposed.

## Context

With `--docker`, the runner image adds `dev` to the `docker` group so the agent reaches the rootful `root:docker`
socket. Membership in that group is root-equivalent: `docker run -v /:/host … chroot /host` and
`docker run --privileged --pid=host … nsenter -t 1` both yield VM root. The microVM stays the isolation boundary, but VM
root hands the agent kernel-facing capabilities, root-owned `0600` files, and root-owned runtime state, a far larger
surface against the microVM/`msb` boundary than the `dev` account should hold.

A spike in a real microsandbox VM (runtime 0.7.13, guest kernel 6.12.113, trixie base, Docker 29.7.2) established:

- Unprivileged user namespaces are enabled and `useradd` writes `dev:100000:65536` to `/etc/subuid`/`/etc/subgid`.
- The guest's `devtmpfs` creates `/dev/fuse` and `/dev/net/tun` as `0600 root:root` on every boot. With those modes
  `fuse-overlayfs` and the slirp4netns tap setup fail for `dev`; at `0666` (the usual distro mode) rootless dockerd
  starts with `Storage Driver: fuse-overlayfs`, `Security Options: rootless`.
- `dockerd-rootless.sh` needs `ip` (iproute2), `newuidmap`/`newgidmap` (setuid, from `uidmap`), and `slirp4netns`.
- Working rootless: image pull, container egress (`apt-get update`), `docker build`, `-p 8080:80` reachable on the VM's
  loopback, and access through a `/var/run/docker.sock` symlink without `DOCKER_HOST`.
- Blocked rootless: reading a root-owned `0600` file through `-v /:/host` (`Permission denied`), and
  `--privileged --pid=host nsenter -t 1` (`reassociate to namespaces failed: Operation not permitted`). Container root
  maps to `dev` (`uid_map 0 1000 1`). Files a container writes to a bind mount as container root are owned by `dev`;
  a non-root container user cannot write a `dev`-owned bind mount.
- The guest kernel ships no loadable modules (`/lib/modules` absent), so module loading fails regardless; a non-initial
  user namespace never holds `CAP_SYS_MODULE` in any case.
- Without systemd the cgroup driver is `none`: per-container resource limits (`--memory`, `--cpus`) are not enforced.

## Options

1. **Rootless Docker** (rootlesskit + `dockerd-rootless.sh`, running as `dev`): the Docker CLI/API, `docker build`,
   `docker run` and the socket stay; `--privileged` is privileged only inside `dev`'s user namespace. Costs userspace
   networking and unenforced per-container cgroup limits.
2. **Rootless Podman** behind a Docker-compatible socket: same security property, but changes the engine the agent
   and users already script against (#15 tracks exploring it separately).
3. **Daemonless builder only** (rootless BuildKit/buildah): closes the hole completely and is cheaper, but removes
   `docker run`/`compose`/the socket from the agent's workflow.
4. **Keep rootful dockerd and constrain it** with `userns-remap` or an authz plugin/socket proxy: `userns-remap` breaks
   `/workspace` ownership for container writes; an authz filter is a deny-list over an evolving API with `docker build`
   itself as a privileged surface, so a missed field reintroduces root.

## Decision

Option 1. The settled rules:

- **No `docker` group grant.** `dockerAdoptionBlock` no longer runs `usermod -aG docker dev`; the engine runs as `dev`
  and owns its socket.
- **Missing prerequisites fail the image build; there is no rootful fallback.** The final-stage prerequisite check
  covers the rootless runtime (`newuidmap`, `newgidmap`, `slirp4netns`, `ip`, `rootlesskit`, `dockerd-rootless.sh`)
  alongside the existing static-install ones; it checks presence only, not the setuid bit, because some distributions
  (Fedora) grant `newuidmap`/`newgidmap` their privilege through file capabilities instead, and it applies to a base-provided engine too. A silent fallback would
  make the security property depend on which base a project happens to use, and it would re-grant root-equivalence
  without the user noticing; a build error naming the package is the existing contract for docker prerequisites.
- **Where prerequisites come from.** `rootlesskit` and the rootless entrypoint scripts come from Docker's
  `docker-rootless-extras-<version>.tgz`, installed by the docker stage into `/opt/agents-sandbox`, so no base has to
  provide them. `uidmap`, `slirp4netns`, and `iproute2` are system packages (setuid binaries and shared-library
  dependencies), so the embedded managed base installs them, as it does `fuse-overlayfs`; a custom base provides them
  itself or fails the check. slirp4netns is chosen over pasta because it is `dockerd-rootless.sh`'s default and the
  path the spike verified.
- **Data root on the VM root disk** (`/var/lib/agents-sandbox/docker`, owned by `dev`), not the rootless default
  `~/.local/share/docker` in the home volume. The engine data keeps today's lifecycle (lost on VM recreate, like
  `/var/lib/docker`), images do not consume the home volume or its quota, and the root overlay is the backing the
  `fuse-overlayfs` measurements were taken on.
- **Socket in a fixed `dev`-owned runtime directory, with `/var/run/docker.sock` a symlink to it.** The launcher
  creates `/run/agents-sandbox/docker` (`0700`, the engine's `XDG_RUNTIME_DIR`; there is no logind to provide
  `/run/user/<uid>`) and the symlink at engine start. A fixed path does not depend on `dev`'s uid, which a custom base
  may set. Tools that hard-code the default socket (Testcontainers, scripts, the launcher's own root-run
  `docker info`/`docker ps -q` probes) keep working without `DOCKER_HOST`; the `0700` directory keeps the socket
  `dev`-only for non-root users.
- **The image's `/etc/docker/daemon.json` stays the engine's configuration.** Rootless dockerd reads
  `~/.config/docker/daemon.json` by default, which would silently drop the configuration a project bakes into its
  image (the docs promise those edits are preserved). The launcher passes `--config-file /etc/docker/daemon.json` when
  the file exists, and the #287 rule generalizes: a launcher flag (`--storage-driver`, `--data-root`) is dropped when
  the file already sets that key, because dockerd rejects a directive set both ways.
- **The launcher makes `/dev/fuse` and `/dev/net/tun` world-accessible (`0666`) at every engine start**, because the
  guest recreates them `0600` each boot. Opening either device grants nothing by itself: mounting FUSE or creating a
  tap device still needs the matching capability in a user namespace the caller owns, which is the standard distro
  permission model.
- **No migration of rootful engine data.** Images and containers created by the old rootful daemon in
  `/var/lib/docker` are not carried over; the new runner image needs a rebuild anyway, and the engine data already does
  not survive a VM recreate.

## Consequences

- `--privileged`-dependent tooling (nested dockerd, some systemd-in-container setups) and per-container cgroup limits
  stop working; container networking is userspace (slirp4netns), so the daemon no longer owns `docker0`. The docs and
  changelog state these limits.
- A custom base that enables docker must add `uidmap`, `slirp4netns`, and `iproute2`; the build error names the
  missing one.
- `vm/dockerd.go` starts `dockerd-rootless.sh` as `dev`; the `fuse-overlayfs` probe runs as `dev`, after the device
  permissions are fixed. A `daemon.json` that sets `data-root` to a directory `dev` cannot write fails the engine
  start with dockerd's own error in the job log tail.
- A VM still running an image built before this change is replaced on the next start, because the rendered Dockerfile
  changes the image's content identity; the launcher still reports a clear error, naming `--rebuild`, when an image
  with dockerd lacks `dockerd-rootless.sh`.
- Follow-up: re-check cgroup delegation if the guest ever gains a systemd user manager, which would make per-container
  limits enforceable.
