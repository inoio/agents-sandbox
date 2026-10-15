---
description: Builder-backend contract — default msb (buildkitd in a shared, reused microsandbox VM) with an embedded host OCI registry (distribution/v3) for `type=registry` cache, and an opt-in moby backend (Docker Engine API build on the host daemon). Covers backend selection (config + env, no CLI flag), the embedded registry, builder-VM reuse for parallel builds, the moby path's restoration, doctor/docs posture, and testing seams.
read_if: Working on builder backend selection, the embedded cache registry, builder-VM reuse/parallel builds, the moby/engine-API build path, builder doctor checks, or builder docs.
---

# Builder backends (msb default, moby opt-in)

**Status: implemented** — `builder.Get()` selects the `msb` (default) or `moby` backend from config/env; the embedded
cache registry, builder-VM reuse, the restored moby Engine-API path, backend-aware `doctor`/prune, and the removal of the
spike/harness all ship. One clause remains to validate on a KVM host (registry bind; see "Reachability / Bind scope").

## Problem Statement

Building the per-project runner image currently has to pick a single strategy with
poor trade-offs. The production path (buildkitd in a microsandbox VM, cache on a
persistent VM volume) is dramatically slower than a host Docker build on cache
misses, the cache does not survive the builder VM being reaped, and a running
builder VM cannot be rediscovered by another process — so parallel or repeated
builds each spin up a fresh VM and rebuild from scratch. Meanwhile the old host-Docker build path was removed, leaving users who have Docker no
faster/simpler option. Users also face an implicit Docker prerequisite they cannot
opt out of (or into) deliberately.

## Solution

Ship two builder backends behind one selection mechanism:

1. **msb (default):** buildkitd runs inside the microsandbox VM, which is now a
   **shared, long-lived, reused singleton per user** reachable by every process
   (parallel builds from multiple agents-sandbox instances use the one VM). The
   runner image's BuildKit cache is kept in an **OCI registry embedded in the
   agents-sandbox binary** (`distribution/v3`), reached by the builder VM as
   `type=registry` cache. This keeps the build isolated in a microVM, supports
   multiple parallel builds through a single VM, and requires **no Docker** on any
   target platform (Linux KVM, macOS Apple Silicon).
2. **moby (opt-in, second-class):** the formerly-existing build path via the
   Docker Engine API (`ImageBuild` through the moby Go client) on the host
   daemon. Selected only through configuration or an environment variable — never
   a CLI flag — and documented as an advanced alternative.

The default requires nothing but the tool itself; the alternative trades build
isolation for the host daemon and is intentionally presented as a fallback.

## User Stories

1. As a developer on Linux or macOS without Docker, I want `agents-sandbox` to
   build the runner image with just the tool installed, so that I am not forced to
   install Docker.
2. As a security-conscious user, I want the runner-image build (including the
   `RUN` steps that execute package/agent installers) to run inside the same kind
   of isolated microVM as the agent, so that a hostile base or build step cannot
   execute on my host.
3. As a user who already runs Docker and prefers a native host build, I want to
   opt into the moby backend, so that I get a fast host build without any CLI
   subprocess.
4. As a user, I want builds to be fast on the common cache-miss path, so that
   changing the agent version or base image does not cost minutes.
5. As a user, I want the image cache to survive between builds even though the
   builder VM is recreated, so that a mostly-unchanged image rebuild reuses layers.
6. As a user, I want the cache to be prunable through the existing cache/volume
   prune surfaces, so that it does not grow without bound.
7. As a user, I do not want the cache registry to require any manual setup, a
   separate daemon, root, or a published network service.
8. As an operator, I want to select the moby backend via a config file entry, so
   that the choice persists across runs.
9. As an operator/CI author, I want to select the moby backend via an environment
   variable for a one-off run, so that I do not edit config files in ephemeral
   contexts.
10. As an operator, I want the environment variable to override the config entry,
    so that ad-hoc overrides are possible without mutating persisted state.
11. As an operator, I want the absence of any selection to mean the isolated msb
    backend, so that the safe default is the implicit one.
12. As a user reading the docs, I want the moby backend clearly marked as an
    alternative with prerequisites, so that I understand it is not the recommended
    or default path.
13. As a user running `doctor`, I want it to report the prerequisites of the
    backend that is actually selected, so that I am not told to install Docker
    when using the default msb backend, and am warned about Docker when using moby.
14. As a user, I want an unknown or empty backend value to fall back to msb, so
    that a typo never silently disables the build.
15. As a maintainer, I want the Docker-container buildkit path removed entirely,
    so that we do not carry an unsupported third backend.
16. As a maintainer, I want a single, high selection seam, so that adding or
    removing a backend does not ripple through the image package.
17. As a user on macOS, I want the default backend to work natively (no Docker
    Desktop VM in the path), so that build behavior is consistent with Linux.
18. As a user, I want the built image to land in the microsandbox image store
    regardless of backend, so that the VM creation path is unchanged.
19. As a user, I want the cache registry to run on demand and idle-reap, so that
    no permanently-running service is exposed on my machine.
20. As a user, I want the registry's token restricted to my user (a `0600` state
    file), so that another user on the machine cannot read or poison my cache.
21. As an operator running two agents-sandbox instances, I want concurrent builds
    not to corrupt each other's cache, so that parallel work is safe.
22. As an operator running several builds at once, I want them to share a single
    running builder VM, so that the VM is started once and its warm state is
    reused instead of each build booting its own.
23. As a user, I want a later process to rediscover and reuse a still-running
    builder VM (and start a fresh one only when none is running), so that
    back-to-back builds are fast.

## Implementation Decisions

### Backend selection
- One selection mechanism with two surfaces and no CLI flag:
  - **Config** entry (launcher config, persisted): a `builder` setting accepting
    `msb` (default) or `moby`.
  - **Environment** variable `AGENTS_SANDBOX_BUILDER` accepting the same values,
    overriding config when set.
  - Precedence: environment > config > default (`msb`). Empty/unknown values
    resolve to `msb`.
- **No CLI flag** is added for backend selection, and no interactive prompt
  presents it.
- The existing test-only `docker` value is removed with the harness; production
  selection accepts only `msb` and `moby`.

### Seam
- Backends are selected at the **`Builder` interface** seam
  (`builder.Get()`), the highest point. The buildkit-specific `backend` interface
  stays internal to the buildkit path.
- The msb path keeps the buildkit client but delivers the runner image through
  the **embedded registry** (see
  [registry-pull delivery](registry-pull-delivery.md)): its solve **pushes the
  image to the registry** (`type=image`, `push=true`, guest-authority name)
  instead of exporting an OCI archive, and the project VM is created from the
  host-dial pull ref (`WithImage(ref)` + `WithRegistryInsecure()` +
  `WithPullPolicy(Always)` after a build, so a same-tag re-push is not served
  stale). It also keeps the registry-backed cache and builder-VM reuse
  (persisted port).
- The moby path is a second `Builder` implementation that does **not** speak
  buildkit; it builds via the Docker Engine API and returns a `docker save`
  archive. The archive route is now **moby-only**: `image.EnsureAvailable` loads
  it into the microsandbox image store; msb neither produces nor loads one.
- Archive loads are **by path, not streamed through a reader**:
  `msb.Client.ImageLoad(ctx, ref, archivePath)` hands the builder's `ArchivePath`
  straight to `msb.Image.Load`. It previously took an `io.Reader` and spooled a
  second full copy of the (multi-GB) archive to a temp file plus an fsync before
  loading; the moby backend already produces a file, so the spool was pure
  overhead.
- Registry auth is **anonymous read, token write**: the loopback-only embedded
  registry allows unauthenticated `GET`/`HEAD` (pulls and BuildKit
  blob-existence checks) and requires the per-user bearer token for mutating
  methods. `prune` drops every `agents-sandbox/`-prefixed repository — buildkit
  cache *and* runner-image repos together — because distribution's blob store is
  global across repositories, so a partial preserve would orphan referenced
  blobs.
- Selection reads config/env at construction so callers and tests can choose a
  backend without package-level mutable state (matching the current approach).

### Builder VM reuse (shared builder VM)
- The builder VM is a **single, shared, reused singleton per user**. Multiple
  parallel builds from any number of agents-sandbox processes connect to the same
  buildkitd; BuildKit serves concurrent solves.
- **Per-state-dir identity**: the builder VM's msb name and component label are
  namespaced by a short hash of the state dir (`builderSandboxName`,
  `BuilderComponentLabelValue`), so processes with different state dirs get
  distinct builder VMs. Without it, an integration test's temp state dir and a
  concurrent real binary share one fixed name; each, unable to recognize the
  other's port record and flock (both live in their own state dir), recreates the
  VM with `WithReplace`, thrashing the running builder and its worker cache — and
  a label-based list would match both. The registry is already isolated this way:
  its endpoint, config, lock, and storage root all live under the state dir.
- **Port persistence is the fix**: the current reuse branch (`vm.go:47-52`) is dead
  only because `publishedHostPort` reads `SandboxHandle.Config()`, which drops
  port bindings. Record the chosen host port (with the sandbox name) in an
  agents-sandbox state file, written atomically at creation, and read it back on
  the reuse branch when the SDK returns no port. No msb SDK change is required.
- **Discovery/lifetime**: under the host-wide flock, look up the sandbox and the
  recorded port. If it exists and is **active**, reuse it (the readiness probe
  validates buildkitd). If it exists but is **stopped/created** (the idle reaper
  only *stops* an idle builder, never removes it), **resume it in place** —
  `SandboxHandle.Start` then restart buildkitd — because an owned worker volume
  survives stop/start, so the BuildKit cache is preserved instead of re-fetched
  from the registry. Only if the sandbox is missing, the port record is absent, or
  the resume fails does it create a fresh VM with `WithReplace` and rewrite the
  record.
- **Worker is resumed, not discarded — bound it by size**: since the worker cache
  now survives reaps, `buildctl prune` carries `--keep-storage` (`50GB`,
  `builderCacheKeepStorage`) so the 128 GiB worker volume cannot fill; the embedded
  registry remains the authoritative persistent cache, so size-bounded worker
  pruning is safe. Prune still requires the builder running to connect (`PruneCache`).
- **Keep-alive across parallel builds**: every in-flight build refreshes the idle
  timer (existing `KeepAlive`), so a shared VM is not reaped while any build is
  running.
- This makes the builder VM a per-user singleton; the cache registry daemon is a
  separate per-user singleton. Both share one idle timeout
  (`cacheregistry.IdleTimeout`, 30s) so they expire together rather than one
  outliving the other. Each is held open by the launcher for the duration of a
  build: the VM via `KeepAlive`, and the registry via a client-side keep-alive
  (`Endpoint.KeepAlive`, `registryKeepAlive` in the builder). The registry only
  sees HTTP traffic at cache import and export, so without this a build longer
  than the idle timeout would let the daemon reap mid-build and reset the
  cache-export connection.

### Embedded cache registry (msb)
- An OCI registry is embedded in the agents-sandbox binary and started on demand
  (once per user) when an msb build needs `type=registry` cache import/export.
- Properties: pure-Go, cross-platform (Linux + macOS), unprivileged, plain HTTP,
  filesystem-backed storage under the agents-sandbox user state directory. The
  filesystem storage enables the **in-memory blob-descriptor cache**
  (`storage.cache.blobdescriptor=inmemory`) — the registry's hottest lookup path,
  which is otherwise disabled and hits the filesystem for every blob during cache
  import/export.
- **Reachability:** the builder VM reaches the host through the sandbox host
  alias `host.microsandbox.internal`, which microsandbox forwards to the host's
  loopback. The registry therefore binds loopback on the host; bind scope is the
  most security-sensitive detail and is confined to loopback (never `0.0.0.0`)
  for the daemon's lifetime.
- **Reachability / Bind scope (implemented; validated on a KVM host):** the
  registry binds the host **loopback** — `cacheregistry.registryLoopbackBind`
  (`127.0.0.1`), plus a best-effort `::1` bind so a guest that resolves the host
  alias to its AAAA record is served too — and never `0.0.0.0`. This was
  validated on a KVM host: a diagnostic proved that a sandbox connecting to
  `host.microsandbox.internal:<port>` is delivered to the host's loopback (the
  accepted connection's local address is `127.0.0.1`), and a listener bound on
  `127.0.0.1` only is reachable from inside the sandbox. An earlier plan pinned
  the builder VM's `IPv4Pool` to a dedicated `/30` and bound the subnet gateway
  (network+1); that was **wrong** — microsandbox carves the pool into per-sandbox
  `/30` subnets and rejected the single-`/30` pool at VM creation ("cannot assign
  network slot 3"), and the host is not the owner of the guest's subnet gateway
  anyway. The pin was removed (`builderNetworkPolicy` now uses the default pool,
  `NetworkPolicy.AllowAll()`).
  Two addresses are deliberately distinct in `Endpoint`: `Host` is the
  guest-visible name `host.microsandbox.internal` (the authority of the cache
  reference and the BuildKit auth key, so buildkitd resolves and authenticates
  against it), while the unexported `dialHost` is the host-side loopback the
  daemon listens on, used by the host-side health probe and `Prune`. The free
  port is probed on the actual bind host; the bind host/port are vars
  (`registryBindHost`/`registryBindPort`) so tests rebind without a real host.
  `TestBuilderVMReachesEmbeddedRegistry` (in `internal/sandbox/builder`, msb tier)
  asserts this invariant: it starts the real `cacheregistry.Serve` bind and proves
  the builder VM can TCP-reach `host.microsandbox.internal:<port>` from inside the
  VM; it fails loudly (bind error, or guest unreachable) rather than surfacing
  only as a missing cache hit. `TestMsbSecondBuildHitsRegistryCache` remains the
  end-to-end proxy; this test isolates the bind/reachability half.
- **On-demand, shared lifecycle:** the registry is a **per-user singleton
  daemon**, not one-per-build. The first agents-sandbox process that needs it
  re-execs itself in a hidden internal serve mode, detached from the parent, bound
  to the host loopback; it idle-reaps after a quiet period. All instances of
  that user connect to the same daemon → one storage, one cache, no duplicate
  caches and no misses between processes. Only its **storage** persists on disk
  under the agents-sandbox state dir.
- **Integration-test state dir:** the msb tier runs against a mocked state dir
  that is **shared across the whole test binary run**
  (`configpaths.WithMockConfigPathsAt` → one `os.MkdirTemp` created on first use,
  removed by `integrationTestCleanup` after the run), never the live user
  registry (the store stays under a temp dir). Sharing is the point: the persisted
  builder-VM record (`builder-vm.json`) survives between tests, so
  `resolveBuilderPort` **reuses the running builder VM** instead of recreating it
  (`WithReplace`), and with it BuildKit's **worker cache** (native snapshotter in
  the VM) — identical build steps across tests then hit cache. A per-test dir (the
  general `configpaths.WithMockConfigPaths` default) left no record, so every test
  recreated the VM and rebuilt from scratch, making the tier far slower. Note the
  registry store also persists across tests, but registry cache refs are
  per-image-tag (`Endpoint.CacheRef(tag, platform)`), so registry-cache reuse
  across tests holds only when tags match; its role is surviving a VM reap/restart
  (the VM idle-reaps after 30s of inactivity; a stopped VM is now **resumed** with
  its worker cache intact rather than recreated). Tests that deliberately remove the
  builder sandbox
  (`removeBuilderSandboxes`) still reset the worker cache for their successors.
  Consequence for cache-invalidation tests: a test asserting that a changed input
  re-runs a layer must use inputs **disjoint from those warmed by earlier tests**,
  or the rebuild is a legitimate cache hit. `TestRenderDockerfileAgentVersionRebuildsInstall`
  therefore bumps between opencode releases other than the pinned
  `defaultIntegrationOpencodeVersion` (which the rest of the suite bakes) — a
  guard fatals if the pinned default is changed onto the test's versions.
  Because the shared dir is also hashed into the builder name and label, the test
  run's builder is distinct from a concurrently running real binary's, so the two
  never recreate each other's VM (see "Per-state-dir identity").
- **Integration tests must remove their builder VM.** The idle reaper only *stops*
  an idle builder, never removes it (production relies on this: the builder name is
  stable per state dir, so at most one exists and it is resumed in place on next
  use). A test run's state dir is a fresh temp dir per `go test`, so its hashed
  builder name is new every run and would leak a stopped builder — plus its owned
  128 GiB worker volume — forever. `internal/sandbox/image`'s `integrationTestCleanup`
  therefore removes the run's builder (by its cached per-state-dir label) before
  deleting the shared dir, and `internal/sandbox/builder`'s
  `TestBuilderVMReachesEmbeddedRegistry` removes the builder it creates via
  `t.Cleanup`.
- **Discovery:** the daemon publishes its endpoint (and token) to an
  agents-sandbox state file written atomically with mode `0600`; a start-lock
  (flock) serializes racing starters, and a stale/mismatched entry (dead pid or
  failed health check) triggers a restart. This mirrors how buildkitd/dockerd
  expose a local endpoint.
- **Authentication:** the daemon mints a random token at start, stored only in the
  `0600` state file (never in config, never world-readable). Every request must
  authenticate (constant-time comparison); ASB injects the token for the embedded
  registry's host into the BuildKit session auth provider (the same mechanism the
  base-image pull path uses). Mode-`0600` restricts the token to the same user,
  and the loopback-confined bind plus token keeps other local processes out; other
  *users* cannot read the token. Plain HTTP over the sandbox channel is
  acceptable, so no TLS/pinning is added.
- The cache reference is derived deterministically from image identity
  (project/agent/platform) so caches are reusable across builds and do not
  collide between projects, and so a stale ref can be pruned by identity.
- Because the registry is ours, cache pruning is implemented directly (delete
  cache manifests/blobs), integrated with the existing builder cache prune
  surface rather than relying on BuildKit-side GC alone.
- The external host-registry and the `AGENTS_SANDBOX_BUILDER_CACHE*` spike knobs
  used during investigation are **not** shipped; the embedded registry is the only
  cache mechanism for the default backend.
- **Embedding (decided): `distribution/distribution/v3`.** Embedded as an
  in-process Go library with the **filesystem storage driver** rooted under the
  agents-sandbox state dir; its `http.Handler` is served on the daemon's endpoint
  and wrapped in the token middleware. It is the reference OCI implementation; its
  filesystem driver persists blobs *and* manifests and supports tag/manifest
  deletion (needed for prune); it is pure Go (zig-cross-compilable). Accepted cost:
  **+11.5 MB** stripped and a heavier tree (OpenTelemetry, gonum, `acme/autocert`,
  cobra) — judged a fair fit for the correctness and prune support it buys.
  Alternatives rejected: `go-containerregistry/pkg/registry` (its manifests are
  in-memory — no cache persistence without patching); Harbor / kraken
  (multi-service platforms, not embeddable); zot (a standalone server, not an
  embeddable library — only usable as a co-shipped second binary/subprocess); a
  hand-rolled OCI subset (smallest, but owns digest/media-type correctness).
- **distribution/v3 embedding mechanics (non-obvious):**
  - The registry config is built in Go, not parsed from YAML, so
    `Catalog.MaxEntries` and `Tags.MaxTags` must be set explicitly — their YAML
    defaults do not apply and the `_catalog`/tags endpoints return empty when
    they are left at the zero value.
  - The filesystem storage driver must be blank-imported
    (`.../registry/storage/driver/filesystem`) or `handlers.NewApp` panics with
    `StorageDriver not registered`.
  - Prune requires `storage.delete.enabled=true`; the HTTP delete removes
    manifests/blobs but leaves the repository directory, so dropping a repo from
    `_catalog` additionally requires removing that directory on disk.

### BuildKit cache/export contract
- Cache import/export uses `type=registry` against the embedded registry
  (`mode=max`, since the runner image is multi-stage and `mode=min` would drop
  intermediate stages' layers).
- Both the import and export entries must set `registry.insecure=true`: the
  embedded registry serves **plain HTTP** on the host loopback (reached from the
  guest through the host alias) and buildkitd has no insecure-registry config, so
  without it BuildKit defaults to HTTPS and every cache import/export against the
  HTTP registry fails.
- The registry cache export is folded into the **single image solve**: `solve`
  receives both the image `Exports` and the registry `CacheExports`, and BuildKit
  runs the cache export after the build graph completes. An earlier design ran a
  second, cache-only solve gated on a step miss; folding removes a redundant cache
  import and graph traversal and lets BuildKit share layer compression between the
  two exports. The cost is that a full cache hit now also exports the cache (cheap:
  the registry already holds the blobs, so only the manifest is re-pushed).
- **Compression**: both the image export (`msbBackend.Export`, a registry push
  since the registry-pull redesign) and the registry cache export use
  `compression=zstd` (`builderCompression`, `oci-mediatypes=true` for the image)
  instead of BuildKit's default gzip. On large images the in-VM image-export
  vertex dominates a rebuild (measured ~1m30s→~61s for the heavy dev image), and
  that cost is
  compression CPU + streaming the whole tar through the microVM's published port.
  The measured cache-push delta (~8s) against the full-image stream (~61s) indicates
  the export is **transfer-bound**. Env overrides for A/B measurement:
  `AGENTS_SANDBOX_BUILDER_COMPRESSION` (e.g. `uncompressed`) and
  `AGENTS_SANDBOX_BUILDER_COMPRESSION_LEVEL`.
- **Process sandbox**: `AGENTS_SANDBOX_BUILDER_NO_PROCESS_SANDBOX` (opt-in) adds
  buildkitd's `--oci-worker-no-process-sandbox`, dropping the per-step PID/mount
  namespace setup (and the guest cgroup-controller warning) at the cost of steps
  sharing buildkitd's PID namespace — acceptable in the throwaway per-user builder
  VM.
- **Phase timings** are recorded on every `BuildResult.Timings` and logged at
  verbose level by the image layer (`Build timings: ensure / base / solve /
  export / total`, plus `solve warmup`). `solve warmup` is solve start → first
  status update: the registry cache import and graph setup, which is invisible in
  Dockerfile-step progress and was the largest unaccounted gap before this
  instrumentation. `export` is the solve tail after the last step (finalizing the
  image archive) plus archive close. `BuildResult.VertexTimings` carries **every**
  BuildKit vertex's reported duration (from `Started`/`Completed`) tagged as a
  Dockerfile step or a buildkit-internal vertex, with its cached flag, and the image
  layer logs them all plus the cumulative step and internal totals. This exists
  because a solve's time is often **not** in the Dockerfile steps: `[internal]`
  vertices (image export/push, registry cache import/export) can
  dominate, and a **cached** step can be slow because BuildKit materializes its layer
  inside the vertex window. The "solve minus cumulative" figure is the residual.

### BuildKit worker storage
- The builder VM mounts its BuildKit worker storage (`/var/lib/buildkit`) as a
  **disk-backed owned volume** (`Mount.Owned{Kind: disk}`, removed with the
  sandbox) — **not `tmpfs`**. A `tmpfs` worker is guest-RAM-backed, and BuildKit's
  native snapshotter materializes every layer into it, so a large build (node/npm
  agents, Fedora tool installs) exhausts the VM's memory and `buildkitd` is
  OOM-killed mid-solve. That surfaced as either `ResourceExhausted: ... cannot
  allocate memory` (when a `RUN` process itself could not allocate) or
  `failed to receive status: ... EOF` (when `buildkitd` died). The volume is
  sized generously (128 GiB, `builderCacheDiskSizeMiB`): the native snapshotter
  keeps a full copy of every unpacked layer, so one test process building many
  image variants accumulates well beyond the low tens of GiB and can otherwise
  fill the worker and fail a copy with `copy_file_range: no space left on
  device`. The worker survives an idle reap (the VM is resumed, not recreated) and
  is size-bounded by the prune `--keep-storage`; the persistent layer cache is the
  embedded registry, so a cleared or pruned worker only costs a re-import.
- **Snapshotter**: buildkitd runs `--oci-worker-snapshotter fuse-overlayfs` by
  default. The guest fs cannot host a kernel overlay mount (`mount ... "overlay"
  ... invalid argument`), and the copy-based `native` snapshotter was the dominant
  rebuild cost (full-rootfs copies per changed layer). The guest provides FUSE
  (`/dev/fuse`, `fuse` in `/proc/filesystems`), so the userspace `fuse-overlayfs`
  snapshotter works and roughly halves build-step time on large images; the builder
  image is the upstream **rootless** BuildKit image because it bundles
  `fuse-overlayfs` (and `buildkit-runc`), and the sandbox pins `User=root` since
  that image defaults to uid 1000. `AGENTS_SANDBOX_BUILDER_SNAPSHOTTER` overrides
  the snapshotter (e.g. `native`) for troubleshooting. The start command installs
  `fuse-overlayfs` as a fallback if absent and resets the worker when a marker file
  shows it was formatted by a different snapshotter (BuildKit snapshot data is
  snapshotter-specific).
- **Daemon GC budget**: buildkitd runs `--oci-worker-gc-keepstorage
  50000,50000,50000` (`builderCacheGCKeepStorage`, MB) instead of the 2 GiB default,
  so multi-GB images are not evicted continuously during a build (re-unpack churn +
  GC IO). It aligns with the 50 GB `buildctl prune` bound; the worker volume is
  128 GiB so the budget leaves headroom.

### moby backend
- Restores the pre-BuildKit path: tar the build context (Dockerfile plus any
  assets), `ImageBuild` with `Tags`, `Remove`, `NoCache`, and the assembled
  `BuildArgs`, drain the streamed build output for errors, then `ImageSave` to a
  host archive returned as the build result's `ArchivePath`. `image.EnsureAvailable`
  then loads that archive into the microsandbox image store via
  `msb.Client.ImageLoad` — the archive route is now moby-only, and `builder`
  itself does not depend on an `msb.Client`.
- Depends on the Docker Engine API client library only — a Go dependency, no
  command execution and no TCP network path (it talks to the daemon socket).
- Its layer cache is the daemon's; agents-sandbox does not manage it. `NoCache`
  forces a full rebuild; prune maps to the daemon's dangling-image prune.
- It must populate the same build result contract (digest, env, base digest) the
  image package consumes, sourced from engine inspect calls.
- The `moby/moby/client` (and its `api`) modules become production dependencies.

### doctor
- `doctor` reports prerequisites for the **selected** backend: msb requires the
  microsandbox runtime/KVM; moby requires a reachable Docker daemon. The Docker
  check is gated on moby selection and is not shown for the default backend.

### Documentation posture
- README/`docs` describe the msb backend as the default and only fully supported
  path; the moby backend gets a short, clearly-labelled "alternative" section
  listing its prerequisites (Docker daemon) and stating it is second-class and
  not recommended.
- `CHANGELOG.md` `[Unreleased]` records the new default (embedded-registry msb)
  and the opt-in moby backend.

## Testing Decisions

- Good tests exercise external behavior through the highest seam and assert
  observable outcomes (which backend ran, whether a build reused cache, whether
  the produced image reached the store) — never internal wiring.
- **Selection** is unit-tested at the `Builder` seam: default resolves to msb;
  config selects moby; env overrides config; empty/unknown falls back to msb.
- **Embedded registry** is unit-tested at its own package seam: start/serve,
  push/pull round-trip, deterministic cache-ref derivation, storage under the
  state dir, and prune deletes. It also covers the security/lifecycle contract:
  the shared daemon starts on demand, is idempotent for a second client, idle-reaps,
  rejects unauthenticated requests, and restarts on a stale endpoint/token. Prior
  art: `internal/sandbox/builder/vm_test.go`, `tuning_test.go`.
- **The daemon spawner is test-binary aware at every consumer.** `Ensure`
  re-execs `os.Executable()`, which under `go test` is the *test* binary, not the
  agents-sandbox CLI. A test binary that triggers a spawn must therefore dispatch
  the hidden `__builder-registry` command from its `TestMain` via
  `cacheregistry.RunIfDaemonInvocation()`, or the re-exec'd child re-runs the
  whole suite and spawns again — a fork bomb that exhausts host memory. Unit
  suites must additionally stay offline by installing
  `image.InstallOfflineImageDelivery()` (stubs `imagePullRef`/`holdRegistry`), so
  a resolve never spawns at all; the tagged integration tier omits it to drive the
  real seam. The guards live in the `TestMain`s of `image`, `vm`, `volume`,
  `cmd/agents-sandbox`, and the registry owners `builder` and
  `builder/cacheregistry`; `TestTestBinaryServesRegistryDaemon` (integration) and
  `TestUnitSuiteDeliversOffline` guard them. The msb integration tier also runs packages serially
  (`-p 1`): the builder VM is a user-global singleton with a fixed name while each
  test package isolates its own state dir, so parallel packages race to
  stop/recreate it (`... was replaced (expected identity ...)`).
- **Integration builds pin the agent version**: resolving "latest" hits agent
  release APIs (opencode's GitHub `releases/latest` is unauthenticated and
  rate-limited to 60/hr per IP), which flakes the tier. `internal/sandbox/image`'s
  integration helper returns a pinned opencode release
  (`AGENTS_SANDBOX_INTEGRATION_OPENCODE_VERSION` overrides it) and only resolves
  through the network for other agents; the resolver itself is covered by unit
  tests. Failed integration builds dump the builder VM's status and the in-VM
  `buildkitd` log tail (`dumpBuilderFailure`) — the only trace of a crashed or
  reaped builder.
- **moby backend** is unit-tested with the engine client stubbed: builds invoke
  `ImageBuild` with the expected options and return an `ImageSave` archive, which
  `image.EnsureAvailable` loads into a mocked microsandbox client. Prior
  art: the pre-removal `internal/sandbox/docker` mocks/tests and the current
  `builder` test fakes.
- **Builder-VM reuse**: a unit test covers port-record write/read and the
  discovery decision (reuse a healthy VM via the recorded port when the SDK
  returns none; recreate on a stale record or failed readiness probe).
- **Integration**: the msb tier (KVM, `make test-integration-msb`) asserts a
  second build hits the registry cache and that **two parallel builds share one
  builder VM** (same port, one VM); the moby tier (Docker available,
  `make test-integration`) asserts a build succeeds and loads into the
  microsandbox store. With the BuildKit container harness gone, the BuildKit
  path's integration coverage is KVM-only. Prior art: the existing
  `integration`-tagged harness (`integration_build_test.go`,
  `integration_harness_test.go`).
- **Concurrency**: a test starts the shared daemon, has a second client discover
  and attach to it, and asserts both use the same storage (a cache written by one
  is read by the other); a stale endpoint entry triggers a restart.
- **CLI/config**: adding no CLI flag is asserted; the config key and env binding
  are covered in the `cmd/agents-sandbox` config tests.

## Out of Scope

- A `docker build` CLI / "host" backend; it was only ever a benchmark baseline.
- Reintroducing any Docker-container buildkit backend.
- Cache-hit-rate tuning beyond `mode=max` cache export.
- Auto-detection of Docker to switch backends silently (moby is explicit only).
- Incremental image delivery (registry-pull or partial export): the whole-image
  tar stream to `msb.Image.Load` is the current floor (see the perf guide).

## Keep vs Remove inventory

The investigation left throwaway code and `AGENTS_SANDBOX_BUILDER_*` knobs. The
end state must contain **only** the two production backends, the selection
mechanism, and the embedded registry — no maintainable experiment leftovers.

### Keep (production)

Each kept element is load-bearing for the two backends; none is experiment
residue.

- The `Builder` seam (`builder.Get()`) with config/env selection — the single
  highest selection point. Keeping it means backend choice never leaks into the
  image package or CLI, and it is the one seam tests mock. Rebuilding selection
  lower would duplicate it.
- The BuildKit path (solve, OCI export, gateway base-digest resolution, the
  `type=registry` cache export/import folded into the image solve, and the
  cached-step reporting incl. the "cached if ever reported so" fix and
  `FROM`/`[internal]` exclusions) — this *is*
  the default backend. The registry cache is what makes it viable (the old volume
  cache was ~3.6× slower on misses), and the gateway base-digest route is required
  for image identity/inspection under daemonless builds. Removing any of it means
  full rebuilds or broken identity. (Only the non-registry cache modes are trimmed.)
- The msb builder-VM lifecycle and its `KeepAlive` handling — required to run
  buildkitd in the microVM at all, and `KeepAlive` specifically prevents the idle
  reaper from killing the VM mid-build, since builds use the forwarded port which
  msb does not count as activity. Without it a build can be terminated partway.
- The registry-auth BuildKit session — lets the builder VM pull the base image
  (including private bases) using the user's existing Docker credentials; without
  it private base images cannot be built.
- The embedded cache-registry package (new code) — the default backend's cache.
- The moby backend, **restored from git history** (the pre-removal Engine-API
  path) and adapted only at its `Builder` seam call-site — not rewritten.
- The `moby/moby/client` + `moby/moby/api` modules, restored alongside the moby
  backend — its Go-only path to the Docker Engine API; no subprocess and no TCP
  service.
- `distribution/distribution/v3` — the embedded registry implementation (+11.5 MB).

### Remove (experiment-only)

- The throwaway benchmark and its extra backend: `bench_integration_test.go` and
  `bench_moby_integration_test.go` (and the `BENCH_*` scaffolding).
- Every `AGENTS_SANDBOX_BUILDER_*` spike knob: TLS bypass, memory, cache mode,
  cache size, snapshotter, idle timeout, trace, cache ref, cache insecure. Any of
  these values the production code still needs become internal constants, not env
  knobs.
- The non-shipping cache modes and their helpers: named volume, host bind, and
  `tmpfs-export` (local `CacheExports`/`CacheImports`), including the host
  cache-dir preparation and the bind/export path helpers. Only the worker
  storage and the registry cache remain — and the worker storage is a
  **disk-backed owned volume**, not `tmpfs` (see "BuildKit worker storage").
- Diagnostic trace helpers added for the spike, unless deliberately re-adopted as
  a documented debug facility (default: remove).
- The TLS-bypass branch of the builder network/TLS config (keep the production
  default policy only).
- The `spike/` tree (`buildkit-inprocess`, `buildkit-vm`, `ensuretest`).
- The Docker-container buildkit backend in full — the `dockerBackend`, the
  `dockerCLI` seam, `ResetDockerHarness`, the harness container/volume, and the
  `docker` selection value. It is neither a production backend nor a test
  harness we keep. The `docker-test-harness` spec is retired. Its integration
  tier is repurposed: the non-KVM (`Docker`-available) tier now exercises the
  **moby** backend, and the BuildKit path's integration coverage lives on the KVM
  tier (`make test-integration-msb`).
- Unit tests that only assert spike knobs; rewrite coverage around the retained
  registry and selection behavior instead.
- Dependencies pulled in solely by the spikes; re-run module tidy afterwards.

### Convert (keep as record, not as live code)

- The build-performance findings guide becomes a historical decision record
  (rationale for the default/alternative choice); its catalog entry is reframed
  accordingly. Do not leave it reading as active guidance.
- The removed host-Docker modules are **not** restored wholesale; only the moby
  backend is revived, reshaped to the `Builder` seam.

### Prune surfaces to rewire

- `PruneCache` must be backend-aware: msb prunes the embedded registry's cache
  (by derived ref/identity); moby maps to the daemon's dangling-image prune. The
  old VM-volume/harness prune targets are removed with the modes above.

## Further Notes

- **Rationale for msb default:** build isolation (the agent's own promise) plus
  removing the Docker prerequisite on both target platforms.
- **Rationale for moby over `docker build` (`host`):** it is a Go library path
  with no subprocess/command execution and no TCP network service — it only uses
  the daemon socket. Both the `host` baseline and the Docker-container backend
  are excluded (removed), not shipped.
- Performance context lives in the private guide
  `builder-build-performance.md`; the moby path is competitive with the
  alternatives while the default msb path trades a few seconds of build time for
  isolation and no host dependency.
- The moby implementation should be revived from the pre-removal history (the
  host Docker client removed in `5ed59d4`, the BuildKit migration in `f2048fe`)
  rather than reinvented.
- The benchmark (`TestBenchBuildBackends`) and all `AGENTS_SANDBOX_BUILDER_*`
  spike knobs are throwaway and must be stripped per the Keep vs Remove
  inventory; only the two production backends and the selection mechanism remain.
