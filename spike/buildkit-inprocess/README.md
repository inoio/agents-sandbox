# Spike: in-process BuildKit (issue #15)

**Throwaway feasibility spike — not production code.** Separate Go module
(`go.mod`) so BuildKit's large dependency closure never enters the main module.

## Question

Can the runner image be built with **no Docker/Podman daemon** by embedding
BuildKit *in-process* in the launcher, and does that work on macOS?

## What it does

Reimplements the minimal subset of `buildkitd`'s own `newController` /
`newWorkerController` wiring using BuildKit's exported packages:

- one **OCI (runc) worker** with the `native` snapshotter,
- the `dockerfile.v0` frontend, and a bolt-backed cache store,
- served on a local unix socket, driven by the BuildKit Go client
  (`client.Solve`), exporting an **OCI Image Layout** (`-exporter oci`) or a
  **`docker save`** archive (`-exporter docker`).

Both archive formats are what `microsandbox.Image.Load` accepts
(`sdk/go/image.go`: "a `docker save` tarball or an OCI Image Layout archive").

## Run (Linux)

```sh
cd spike/buildkit-inprocess
go build -o bkspike .

# FROM scratch, no base image, in a user namespace (rootless BuildKit):
unshare -Ur -m --propagation private \
  ./bkspike -scratch -exporter oci -root /tmp/bk-state -out /tmp/bk-state/image.tar
tar -tf /tmp/bk-state/image.tar   # index.json, oci-layout, blobs/
```

Flags: `-exporter oci|docker`, `-scratch` (`FROM scratch`, no pull),
`-rootless` (default true), `-root`, `-out`.

If you are root (or in a VM where you are root), you can drop `unshare` and
pass `-rootless=false`.

## Findings

1. **In-process, daemonless build works on Linux.** Verified end-to-end:
   controller + OCI worker start in the launcher process, `client.Solve`
   builds a Dockerfile and exports valid OCI and docker archives. No
   `buildkitd` daemon, no Docker daemon.
2. **It is not dependency-free.** The OCI worker needs `runc` + a snapshotter
   (`native`/`overlayfs`/`fuse-overlayfs`) + Linux namespaces. So "remove
   Docker" becomes "require runc + user namespaces" on the host — a lateral
   move, plus a large Go dependency closure and an unstable embedding API
   (`buildkitd`'s controller wiring is `package main` and must be mirrored).
3. **Rootless needs subordinate-ID tooling.** Layer application `lchown`s
   files to non-root gids; without `newuidmap`/`newgidmap`
   (the `uidmap` package) and `/etc/subuid`+`/etc/subgid`, rootless builds
   fail with `lchown ... invalid argument (Hint: try increasing the number of
   subordinate IDs)`. `FROM scratch` builds avoid pulling arbitrary-ownership
   layers and succeed.
4. **macOS cannot run this in-process at all.** BuildKit executes `RUN` steps
   as Linux containers via `runc`, namespaces and cgroups. Darwin has no such
   primitives, so an in-process library build on macOS is impossible; the
   build must run inside a **Linux VM**. (macOS "native containers"
   projects run *Darwin* containers, not Linux ones.)

## Implication for #15

In-process embedding is viable only on Linux and buys little over an
app-managed `buildkitd` binary (both need runc/namespaces on Linux). On
macOS, BuildKit must live in a Linux VM regardless — the natural candidate
being a microsandbox-managed builder (msb sandboxes support port publishing,
vsock, and bind mounts), which would remove Docker on *both* platforms.

## Residual unknowns (need real hardware)

- Does `microsandbox.Image.Load` actually ingest BuildKit's OCI/docker
  archive end-to-end? (SDK documents both; `/dev/kvm` is unavailable in the
  dev sandbox so it could not be exercised here.)
- A plain `alpine` pull-and-`RUN` build on a host with `uidmap` configured.
- Cache reuse across runs (the bolt cache store under `-root` persists).
