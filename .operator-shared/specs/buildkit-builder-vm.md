# BuildKit builder VM — runner-image build without host Docker (issue #15)

## Goal

Build the per-project runner image **without requiring Docker/Podman on the
host**. Today the launcher drives the host Docker daemon (`internal/sandbox/
docker`) to build the image, save it as a `docker save` archive, and load that
into the microsandbox cache. Issue #15 asks to remove that host dependency by
moving image construction to **BuildKit**, while keeping BuildKit's native
Dockerfile support, OCI output, and persistent build cache.

Success criteria:

- The launcher never requires a host Docker/Podman daemon for a normal
  `build`/`run`/`shell`.
- Runner-image builds honor the existing content-identity/cache-reuse
  semantics (no needless rebuilds) and persist BuildKit's cache across
  invocations.
- The built image reaches the microsandbox image cache exactly as before, so VM
  creation is unchanged.
- Works on Linux (KVM) and macOS (Apple Silicon) through the same code path.

## Decision

Run **`buildkitd` inside a microsandbox (msb) VM** and drive it from the
**host-side BuildKit Go client** over a published TCP port. This is the only
option that is uniform across both target platforms (BuildKit needs a Linux
kernel; on macOS that is the msb microVM), and it removes the host Docker
prerequisite entirely (no host `runc`, no daemon, no privileged host access
beyond msb itself).

Validated by the throwaway spike in `spike/buildkit-vm/` on a Linux KVM host:

- an msb VM booted from `moby/buildkit:v0.25.2`, with `buildkitd` started
  explicitly, built both `FROM scratch` and `FROM alpine` + `RUN` images (nested
  `runc` works in the microVM);
- registry pulls work through msb egress/TLS inspection (no TLS bypass needed);
- the BuildKit cache persists across ephemeral builder VMs via a named msb
  volume at `/var/lib/buildkit` (a second build showed `RUN … CACHED`);
- the exported OCI/docker archive loads via `microsandbox.Image.Load`
  (which accepts a `docker save` tarball or an OCI Image Layout archive);
- the **host-side BuildKit client over the published port** completed a build
  and loaded the result (`gRPC build OK`).

Rejected alternatives: in-process embedding (Linux-only, needs host
`runc`+namespaces, unstable embedding API — see
`spike/buildkit-inprocess/`); Podman/Buildah (same platform constraints as
BuildKit, and still needs a Linux VM on macOS).

### Critical gotcha

Published-port access must use a **unique, free host port** per builder VM.
Reusing a fixed host port while a stale sandbox/forwarder still holds it makes
every connection accept-then-`EOF`, which is easy to misdiagnose as a protocol
(HTTP/2) problem. This cost the spike an extensive detour. Allocate the port
with `options.FirstFreeHostPort` (as serve-only already does).

## Architecture

New `internal/sandbox/builder` package owns the BuildKit builder lifecycle and
the build, exposing a narrow interface to `internal/sandbox/image`. The
`image` package keeps its orchestration (version resolution, Dockerfile
rendering, identity/cache decision) but delegates the actual `docker build` +
`docker save` to the builder.

Components:

1. **Builder VM lifecycle** (`builder.VM`): ensure a single shared msb sandbox
   from a pinned BuildKit image exists across launcher invocations, start
   `buildkitd`, wait for readiness, and expose its gRPC address. See
   "Concurrency and lifecycle".
2. **Build** (`builder.Client`): connect the host-side BuildKit client, stream
   the synthetic build context, `Solve` the `dockerfile.v0` frontend with the
   build args and export an image archive, and return the archive plus image
   metadata (digest, config/ENV/labels).
3. **Delivery**: the existing `msb.Client.ImageLoad` boundary is unchanged.

### Builder VM configuration

- **Image**: a pinned `moby/buildkit:<version>` (constant; pulled by msb like
  any image). The builder image is infrastructure, not user-configurable in the
  first cut. Include the pinned version in the builder VM's name/identity so a
  version bump recreates the shared VM instead of reusing a stale daemon.
- **Guest port**: a fixed constant (`builderGuestPort`); no other guest process
  in the builder VM needs a port.
- **Host port**: `options.FirstFreeHostPort(base)` bound to `127.0.0.1` via
  `WithPortBindings` (`Bind: "127.0.0.1"`), **not** the legacy `WithPorts` map.
- **Cache volume**: one **global** named msb volume (e.g.
  `agents-sandbox-buildkit-cache`) mounted at `/var/lib/buildkit`. Global scope
  maximizes cache hits across projects; it is mounted by exactly one builder VM
  at a time (see below).
- **Network**: the builder VM is **build** infrastructure, distinct from the
  project **run** VM. For parity with today (builds run on the host Docker
  daemon with unrestricted host networking), the first cut gives the builder VM
  **permissive egress** (public internet + gateway DNS), rather than inheriting
  the project's restrictive run policy. Build-vs-run network policies are
  deliberately separate concerns; restricting builder egress (e.g. registries
  only) is a possible later refinement, not part of this change.
- **Detached** with a short idle timeout so a crashed launcher cannot leak the
  VM.
- **Command**: `ShellStream("exec buildkitd --oci-worker-snapshotter native
  --addr tcp://0.0.0.0:<builderGuestPort>")`. The `native` snapshotter avoids
  overlayfs/`fuse` requirements inside the microVM. `buildkitd` output is
  mirrored to verbose UI.

### Build flow (replaces the Docker flow)

1. `image` renders the synthetic context (rendered Dockerfile + the
   build-context files from `buildContextFiles`, today assembled by
   `writeDockerfileTar`) into a temporary host directory. BuildKit streams this
   directory over the session, so nothing is bind-mounted into the builder VM.
2. Connect `client.New(ctx, "tcp://127.0.0.1:<hostPort>")`; wait for `ListWorkers`
   to succeed (readiness).
3. `Solve`:
   - Frontend `dockerfile.v0`; `LocalDirs{context,dockerfile}` → the temp dir.
   - `FrontendAttrs`:
     - `build-arg:<KEY>=<value>` for every arg produced by `userBuildArgs`
       (`USER_UID`, `USER_GID`, the agent version arg, `BASE_IMAGE`,
       `DOCKERFILE_ID`, `IDENTITY_*`, and `DOCKER_VERSION` when Docker is
       enabled). The exact arg set is preserved so the image content and
       identity labels are byte-for-byte compatible with today.
     - `platform=linux/<host arch>` (amd64 on x86_64, arm64 on Apple Silicon).
   - `Exports`: a single `type=docker` (or `type=oci`) export whose `Output`
     writes the archive to a host temp file.
   - `Session`: a Docker auth provider (see "Registry auth").
4. Consume `SolveStatus` to drive the existing spinner/verbose output
   (replacing `scanBuildOutput`).
5. Read image metadata from the exporter response: the config JSON
   (`containerimage.config`) and digest (`containerimage.digest`) give
   `ImageInfo.Env` and `ImageInfo.Digest`, and `containerimage.buildinfo` gives
   the base image digests — all without a host image store.
6. `msb.Client.ImageLoad(tag, archive)` loads the image into the cache.

### Identity, reuse and base image

- **Reuse decision** (`buildDecision`): inspect the image already present in
  the **msb cache** (`msb.ImageInspect`) and compare the `dockerfile-id` label
  (and identity labels) instead of inspecting a host Docker image. A cache hit
  skips both the build and the load. This removes the last read of the host
  Docker image store. The label is trusted for the *build* decision only: the
  load-side content check (`cachedImageMatches`) stays digest-only, and a
  cached image with no digest is reloaded, never matched by the label
  (`specs/runner-image-build.md`).
- **Base image digest**: no registry resolver is needed. BuildKit's solver
  response includes `containerimage.buildinfo` (base64 JSON; also embedded in
  the image config), whose `sources[]` list every `docker-image` source with
  `type`, `ref`, and `pin` (the resolved digest). Take the source whose `ref`
  matches the rendered Dockerfile's final-stage base (from `baseImageRef`) and
  use its `pin` to form `<ref>@sha256:<digest>` for the `BASE_IMAGE` arg —
  replacing `resolveBaseDigest` with no extra dependency. (`buildinfo` is
  deprecated in favour of the provenance attestation's `resolvedDependencies`;
  if it is ever removed, switch to reading the provenance attestation.)
- **Env read-back**: `readImageInfoFromDocker` becomes a read of the exporter
  config (step 5) and/or `msb.ImageInspect`; no Docker.

## Contracts and invariants

- **No host container runtime**: nothing in the normal path may require a
  Docker/Podman daemon, a Docker socket, or a host `runc`. `internal/sandbox/
  docker` and its `doctor.CheckDocker` preflight are removed.
- **Free host port**: every builder VM publishes on a freshly allocated free
  host port; never a fixed constant. The port is bound on `127.0.0.1`.
- **Explicit `buildkitd` start**: msb does not auto-run a long-lived service
  from the image entrypoint; the builder must start it via `ShellStream` and
  wait for readiness before dialing.
- **Cache persistence**: the BuildKit cache lives only in the named msb volume;
  builder VMs are ephemeral. Cache survives across invocations.
- **Delivery boundary unchanged**: images enter the VM cache only through
  `msb.Client.ImageLoad`. No new image transport is introduced.
- **Identity compatibility**: the rendered Dockerfile, build args, and resulting
  labels (`org.agents-sandbox.*`) are unchanged, so existing images and the
  reconfig/provenance logic keep working.
- **No in-image Docker coupling**: the `--docker` (DinD) option and the runner's
  Docker engine block are unaffected — they concern the engine baked into the
  runner image, not the host build mechanism.

## Concurrency and lifecycle

Use a **single shared builder VM per host** (keyed on the global cache volume),
not one per build. BuildKit is a concurrent build engine: one `buildkitd`
serves many `Solve` requests in parallel, so a shared VM handles concurrent and
parallel builds while giving a warm cache, no repeated VM boot, and a single
writer for the cache volume.

- **Cache volume safety**: `buildkitd` locks its root (`/var/lib/buildkit`) and
  its Bolt-backed cache is not safe for concurrent writers — so exactly one
  `buildkitd` may mount the cache volume at a time. A single shared builder
  guarantees this; multiple builder VMs sharing the volume would not.
- **Creation coordination**: an "ensure builder VM" step is guarded by a
  host-side flock (reusing `state.FlockExclusive`) so concurrent launcher
  processes don't race to create/replace it. The existing VM is discovered by
  name and its published host port read from its config (mirroring
  `ResolveServeHostPort`). Clients then connect to that port; parallel builds
  simply issue concurrent `Solve` calls to the same daemon.
- **Reaping**: the shared VM carries an idle timeout; a crashed launcher cannot
  leak it, and it is removed when idle. It is host-level infrastructure (like a
  Docker daemon today), not per-project, so a single instance is consistent with
  the "one VM per project" rule that governs the *agent* VM.

If a shared VM proves operationally awkward (e.g. version skew across launcher
versions), an ephemeral builder per build with a host flock serializing cache
access is the fallback — but the shared VM is the recommended design.

## Registry auth and secrets

Registry credentials for pulling private base images are supplied to the
**host-side BuildKit session** via `authprovider`, not injected into the VM.
Candidate sources, in recommended precedence:

1. **Launcher config** (`internal/launcherconfig` `/ `~/.config/agents-sandbox/
   config.yaml` registry entries). Explicit, cross-platform, consistent with the
   `config`/onboarding surface. Requires `config` support for registry creds if
   it does not exist yet.
2. **`~/.docker/config.json`**, read (read-only) via `github.com/docker/cli/
   cli/config` (`LoadDefaultConfigFile`), including credential-helper binaries.
   Familiar and already what `docker login` writes; needs no daemon. This is
   what the spike used and it worked cross-platform. Prefer launcher config
   first, fall back to this.
3. **msb secrets** are injected into a *VM's* egress and therefore apply to
   **guest-side** pulls (the `buildctl`-inside fallback), not to the host-side
   BuildKit session. Not the source for the host-side client.
4. **Environment/static token** as a last-resort override.

The chosen provider reads whichever sources are configured and feeds one
`authprovider`; registry creds are static (contrast the refreshable-OAuth
handling in `specs/guided-onboarding.md`). The builder VM's egress remains
permissive (see "Builder VM configuration"), so credentials are needed only for
authentication, not to punch through the network policy.

## Prerequisites, pruning, dry-run

- **doctor**: drop the Docker check; `CheckAll` should verify the msb runtime
  only (plus existing checks). Update preflight messaging and docs.
- **Pruning**: `pruning/images.go` currently prunes dangling managed images via
  the Docker daemon. Replace with BuildKit cache GC (automatic) and/or a
  `buildctl prune` step against the builder, plus the existing msb image prune.
  Define retention consistently with current 24h/dangling semantics.
- **Dry-run**: `--dry-run` must remain side-effect free — in particular it must
  not boot a builder VM or start `buildkitd`. Dry-run reports the build plan
  (tag, identity, cache decision) only.

## Interfaces and seams

- `internal/sandbox/builder` exposes a small interface (e.g. `Build(ctx,
  BuildRequest) (BuildResult, error)` where `BuildResult` carries the archive
  reader/path, digest, and config). `internal/sandbox/image` depends on this
  interface, not on the concrete BuildKit client, so tests inject a fake.
- The msb `Sandbox` interface already exposes `ShellStream`/`Shell`, so
  starting `buildkitd` needs no new msb abstraction.
- `EnsureImageWithClient` keeps its signature; its internals swap Docker for the
  builder. `EnsureLoaded`/`Build` change to consume the builder's archive
  instead of `docker.Get().ImageSave`.
- Because there is no persistent host image store, "build" and "load" become a
  single pipeline: the archive produced by the build is loaded into the msb
  cache in the same operation (or persisted briefly between the two calls).
  Where `EnsureImage` is currently called without loading, revisit whether env
  is needed pre-load or can come from the exporter config.

## Testing

- **Unit**: builder orchestration with a fake BuildKit client and the existing
  `msb.MockMsbClient` — assert the sandbox options (free/unique host port,
  `127.0.0.1` bind, cache volume mount, detached), the `SolveOpt` build-arg
  mapping, the export→`ImageLoad` handoff, cache-hit skip, and teardown.
- **CLI**: `build -r`, `run`/`shell` image-ensure paths, and `doctor` (no Docker
  requirement) in `cmd/agents-sandbox/cli_*_test.go`.
- **Integration** (build-tagged, requires KVM/msb): build the real rendered
  Dockerfile in a builder VM and load it into the msb cache; verify cache reuse
  on a second run. Follow existing integration patterns.

## Alternatives considered

- **In-process BuildKit** (`spike/buildkit-inprocess/`): works on Linux only,
  needs host `runc`+namespaces and an unstable embedding API; rejected.
- **App-managed `buildkitd` on the host**: macOS still needs a Linux VM, so the
  host prerequisite isn't actually removed; rejected.
- **`buildctl` inside the VM** (context/output bind mounts): equally proven and
  needs no BuildKit client dependency; kept as a fallback if the host-side
  client proves problematic. The host-side client is preferred for host-side
  progress/streaming and no bind mounts.

## Open decisions

1. **Export format**: recommended **`type=oci`** (OCI Image Layout) — the
   portable, registry-transferable standard, supports annotations and
   multi-platform indexes, and `microsandbox.Image.Load` accepts it (validated
   by the spike). `type=docker` (a `docker save` archive) is the Docker-specific
   alternative and also accepted; use it only if an OCI-layout incompatibility
   surfaces with `ImageLoad`.
2. **Registry credential source**: confirm the recommended precedence (launcher
   config, then `~/.docker/config.json`); depends on whether `config` gains a
   registry-credentials surface.
3. **Cache-volume scope**: settled as **global** (more cache hits); revisit only
   if cross-project cache sharing becomes undesirable.
