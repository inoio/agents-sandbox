# Runner image delivery via registry pull — design

Date: 2026-10-14
Status: implemented on `opencode/buildkit-builder-vm`. Live KVM validation in progress:
the push/pull round-trip works, but the **build-time** hypothesis did not hold — the
builder's per-layer materialization is unchanged (see "Measured").

## Intent

Rebuild times are dominated by the builder's OCI image export: a warm
agent-version rebuild spends ~1 minute in
`[internal] exporting to oci image format`, ~77% of the solve, streaming the
whole runner image out of the builder VM so the host can `msb.Image.Load` it.
That export is byte- and CPU-invariant (compression A/B tied; see
`.operator/guides/builder-build-performance-followup.md`): it is the cost of
re-materializing every layer tar and pushing it through the session channel,
every build, even when one layer changed.

The embedded per-user OCI registry already receives the BuildKit cache export
incrementally. This design promotes that registry to the delivery mechanism:
the builder **pushes** the runner image to it and the project VM is **created
from that image reference**, so msb pulls it — fetching only the layers it does
not already hold. The OCI-archive export and `msb.Image.Load` path are retired.

A throwaway spike (`spike/registry-pull`) validated the load-bearing
assumption: msb pulls a local insecure registry by ref, and its image cache
dedupes layers across tags (a tag sharing two layers with a cached tag fetched
0/2 shared blobs — only its config and one new layer).

## Goals

- A one-layer runner-image rebuild delivers only the changed layer to the
  project VM; the full-image *stream* leaves the hot path. (The full-image
  *materialization* in the builder does not — see "Measured".)
- `internal/sandbox/builder`'s image `Export` (OCI archive) and
  `msb.Image.Load` are removed; the archive plumbing (`BuildResult.ArchivePath`,
  temp archives, `EnsureLoaded`, `removeBuildArchive`) is retired.
- The existing identity gate (rebuild only when agent version / project
  Dockerfile / docker flag change) is unchanged; the registry becomes a
  mandatory delivery component, not just a cache.
- No behaviour change for the agent: the VM still boots from the runner image.

## Non-goals

- Changing the runner Dockerfile composition, stages, or the buildcache.
- Speeding up the BuildKit solve steps themselves (already ~12s).
- A general-purpose image registry; only agents-sandbox's own runner images
  and the BuildKit cache use it.

## Anti-goals

- Re-fetching the whole image on a one-layer change (i.e. keeping the export).
- Reaching for msb's `WithImageDisk`/flat-materialization as a side route.
- Making the registry the source of truth for build decisions (the identity
  labels remain authoritative; a missing registry image is a rebuild, not the
  normal path).

## Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- The registry binds host loopback only (never a wildcard address) and is a
  per-user singleton daemon with a persisted `{host, port, token}` record
  (`cacheregistry`).
- msb's SDK only supports **basic** registry credentials
  (`RegistryAuth{Username, Password}`); it cannot send a bearer token. The
  embedded registry authenticates with a static bearer token.
- Layers: gzip is the safe intersection (the spike pulled gzip OCI fine);
  zstd layer read support by msb is unverified and avoided on this path.
- Test hermeticity: `cacheregistry.Ensure` spawns the daemon by re-exec'ing
  `os.Executable()`, which under `go test` is the *test binary*. Every test
  binary that reaches `image.EnsureImage` must therefore install the offline
  delivery seam (`image.InstallOfflineImageDelivery`) and dispatch the hidden
  `__builder-registry` command (`cacheregistry.RunIfDaemonInvocation`) from its
  `TestMain`; the registry-owner packages (`builder`, `builder/cacheregistry`)
  need at least the dispatch. Without this a spawn re-runs the whole suite and
  fork-bombs the host.

## Decisions

### D1. The solve exports the image to the registry (replaces the archive)

`msbBackend`'s image `Export` no longer writes an OCI archive to a temp file.
The single solve gets a `type=registry` image exporter
(`name=<pushRef>`, `push=true`, `oci-mediatypes=true`, **gzip** compression)
instead. The registry push reuses the auth session already attached for the
BuildKit cache export, so the layer blobs the cache export uploaded are
HEAD-skipped by the image export — the two share one blob store.

*(builder's call)* Whether the exporter runs as an `Exports` entry in the same
solve (preferred, one graph) or as a distinct post-solve push; the preferred
route is the same-solve `Exports` entry.

### D2. The VM is created from the registry ref, with pull

Every `msbSdk.WithImage(imageTag)` becomes `WithImage(ref)` plus
`WithPullPolicy(msb.PullPolicyIfMissing)` and `WithRegistryInsecure()`. This
covers the project VM (`vm_lifecycle.go`), the home-volume prefill/copy VMs
(`volume/volume.go`, `volume/operations.go`), and any other image consumer.

### D3. Reference shape and authority

- Repository stays the existing runner path: `<naming.ImagePrefix><slug>`
  (i.e. `agents-sandbox/runner-<slug>`), tag `<agent>-latest`.
- **Push ref** (BuildKit, inside the builder VM) uses the guest-visible
  authority: `host.microsandbox.internal:<port>/<repo>:<tag>`.
- **Pull ref** (msb, on the host) uses the host-dial authority:
  `127.0.0.1:<port>/<repo>:<tag>`.
- Both address the same registry repository (distribution keys by repo path,
  not authority). *(implemented)* `cacheregistry.Endpoint` exposes `PushRef(repoTag)`
  (guest authority) and `PullRef(repoTag)` (host-dial authority) alongside the
  unexported `dialAddress()`.

### D4. Registry auth: anonymous read, token write

*(implemented)* `cacheregistry.tokenAuth` allows unauthenticated `GET`/`HEAD`
(pulls and BuildKit blob-existence checks) while requiring the bearer token for
mutating methods (`POST`/`PUT`/`PATCH`/`DELETE`) — the BuildKit cache export
still authenticates. Rationale: the registry binds loopback only, and the
token's job is protecting the cache from poisoning (writes), not reads; msb
cannot present a bearer token.

### D5. Stable registry authority across reaps

The pull ref embeds `:<port>`, so the port must be stable when the daemon is
reaped and restarted, or msb would see a new ref (differing authority) and
re-pull the manifest/config (layers still dedupe, but the entry churns).
`newChildConfig`/`startRegistry` should prefer the persisted port
(`endpointRecord.Port`) when it is still free, rather than always calling
`firstFreeRegistryPort`.

### D6. Registry availability at VM-creation time

Building calls `cacheregistry.Ensure` + `KeepAlive`; VM creation happens later
and must call `Ensure` again before creating the sandbox. Because msb's pull
requests pass through the registry's `trackActivity`, the pull itself refreshes
the idle timer, so no explicit keep-alive is needed across the pull.

### D7. Registry-pull is msb-only; the archive path stays for moby

Registry-pull delivery ships for the **msb** backend only. The archive route
(OCI export + `msb.Image.Load`) is retired *for the msb backend* but kept for
the **moby** backend, so `BuildResult.ArchivePath` and the `EnsureLoaded`
archive branch must remain in the code (they are the moby delivery path, not
dead code). The abstraction keeps a per-backend delivery route rather than a
single archive-vs-pull switch. Porting moby to registry-push is a later,
separate change.

### D8. Invalidate msb's cached ref on every rebuild (measured)

The tag is stable, and the spike proved msb's `IfMissing` **short-circuits on
the cached ref**: after re-pushing a new manifest to the same tag, a plain
`msb pull` fetched 0 blobs. So a rebuild that pushes a new manifest to
`<repo>:<tag>` would leave msb serving the stale image — the existing
`EnsureLoaded` comment already warns that "a same-tag cached image may hold
stale content".

Whenever the identity gate decided to build, the delivery step must make msb
pick up the new manifest. **Shipped:** a build sets `ImageInfo.Refresh`, and
`image.SandboxImageOptions` then creates the VM with `WithPullPolicy(Always)`,
forcing msb to re-resolve the ref. The `Image.Remove(...,force)`-then-`IfMissing`
route was rejected because a stopped or running sandbox may still reference the
cached ref. On a pure cache hit (no build) `Refresh` is false and `IfMissing` is
correct.

### D9. Keep the registry alive across the pull

`trackActivity` touches once per request, but the reaper calls `server.Close()`
on idle, which aborts in-flight connections; a multi-GB layer GET can outlive
the 30s idle timeout. VM creation must therefore hold a registry keep-alive
(the existing `Endpoint.KeepAlive`) across `CreateSandbox`, exactly as the
build holds it across the solve.

### D10. The build-decision probe stays on msb's cache

`buildDecision`/`inspectRunnerImage` and the reuse path read identity labels,
digest, and `Env` from `msb.ImageInspect(ref)`. If msb has no cached ref, the
probe reports "needs build" and a rebuild proceeds — the same behaviour as
today's missing-archive path. Reading them from the registry instead is a
possible later refinement, not phase 1; a wiped msb cache causing one rebuild
is acceptable and matches the identity gate's design.

### D11. Prune clears cache and image repos together; no partial preserve

`Prune` deletes referenced blobs by digest through the distribution delete API,
and distribution's blob store is **global across repositories**. So a cache
repo and a runner-image repo share layer blobs, and pruning the cache while
preserving the image would delete blobs the image manifest still references.
Therefore prune must not preserve the runner-image repos: it drops every
`agents-sandbox/`-prefixed repository (cache *and* images) with its blobs, a
full reset. A run after prune either still finds the image in msb's cache (no
pull needed) or falls back to a rebuild via the identity gate. This subsumes the
old O3.

## Resolved questions

- **O1 (security posture) — resolved: anonymous read is approved.** D4 stands:
  token required for writes, anonymous `GET`/`HEAD` for pulls, loopback-only.
- **O2 (moby backend) — resolved: registry-pull is msb-only for now.** D7
  stands: moby keeps the archive path; archive retirement is backend-scoped.

## Shipped shape

- Solve exports via `type=image`, `push=true`, `oci-mediatypes=true`, zstd, name
  = `Endpoint.PushRef` (guest authority); no OCI archive for msb.
- `builder.ImagePullRef`/`HoldRegistry` are exposed through the `image` package
  (`imagePullRef`/`holdRegistry` seams, `EnsureAvailable`, `SandboxImageOptions`),
  so `vm`/`volume` never import `builder` directly.
- `ImageInfo` gained `Ref`, `RegistryInsecure`, `Refresh`; `ImageRef()` prefers
  `Ref`; labels and prune keep using the bare `Tag`.
- `naming.IsRunnerImageRef` tolerates the registry authority so `image list` and
  `prune` still match registry-qualified refs; prune drops cache and runner-image
  repos together (D11).
- Test hermeticity: every test binary that reaches `image.EnsureImage` installs
  the offline delivery seam and dispatches the hidden registry command (see
  Constraints); this prevents the re-exec'd daemon child from re-running the
  suite.

## Measured (2026-10-15, host KVM)

The push works, but the build-time premise is falsified. The solve's
`[internal] exporting to image` vertex (the registry push) is **invariant to how
many layers changed**: 1m14.3s cold (all 15 steps) vs 1m21.6s on a one-layer
agent-version bump. BuildKit materializes + compresses every layer of the result
to compute its compressed digest before the registry HEAD check, so HEAD-skipping
removes only the blob PUT — and bytes were never the bottleneck. The host OCI
archive is gone (`export≈300ms`), but the dominant per-layer materialization
remains; net build-time change ≈ zero.

The wins are therefore architectural, not in the solve: no host temp archive, no
full-image `msb.Image.Load`, and layer dedupe on **pull**. Those live outside
`Build timings` — end-to-end `run` (the `CreateSandbox` pull replacing
`Image.Load`) is the measurement that decides the net benefit. See
`.operator/guides/builder-build-performance-followup.md`.

## Remaining

- Live msb/KVM validation on the host: finish the cold build + push + pull, the
  one-layer rebuild **pull** side (only the changed layer served to the project
  VM), and `prune`.
- Measure end-to-end `run`: `CreateSandbox` pull cost vs the retired
  `Image.Load`, to confirm the architectural win is worth it.
- moby registry-push (phase 2, deferred; the archive route stays).
- Reading identity/`Env` from the registry instead of msb's cache on reuse (D10).
