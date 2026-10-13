---
description: Builder-backend contract — default msb (buildkitd in a shared, reused microsandbox VM) with an embedded host OCI registry (distribution/v3) for `type=registry` cache, and an opt-in moby backend (Docker Engine API build on the host daemon). Covers backend selection (config + env, no CLI flag), the embedded registry, builder-VM reuse for parallel builds, the moby path's restoration, doctor/docs posture, and testing seams.
read_if: Working on builder backend selection, the embedded cache registry, builder-VM reuse/parallel builds, the moby/engine-API build path, builder doctor checks, or builder docs.
---

# Builder backends (msb default, moby opt-in)

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
- The msb path keeps the existing buildkit client (solve → OCI export → cache),
  gaining a registry-backed cache source and builder-VM reuse (persisted port).
- The moby path is a second `Builder` implementation that does **not** speak
  buildkit; it builds via the Docker Engine API and returns a `docker save`
  archive that the shared `EnsureLoaded` path loads into the microsandbox image
  store (the same delivery path the buildkit OCI archive uses).
- Selection reads config/env at construction so callers and tests can choose a
  backend without package-level mutable state (matching the current approach).

### Builder VM reuse (shared builder VM)
- The builder VM is a **single, shared, reused singleton per user**. Multiple
  parallel builds from any number of agents-sandbox processes connect to the same
  buildkitd; BuildKit serves concurrent solves.
- **Port persistence is the fix**: the current reuse branch (`vm.go:47-52`) is dead
  only because `publishedHostPort` reads `SandboxHandle.Config()`, which drops
  port bindings. Record the chosen host port (with the sandbox name) in an
  agents-sandbox state file, written atomically at creation, and read it back on
  the reuse branch when the SDK returns no port. No msb SDK change is required.
- **Discovery/lifetime**: under the existing host-wide flock, look up the sandbox
  and the recorded port; if the sandbox exists and buildkitd answers its readiness
  probe, **reuse** it; otherwise (absent, stale record, or failed probe) create a
  fresh VM and rewrite the record. The VM is idle-reaped when no build is active,
  so a stale record is expected and handled by recreation.
- **Keep-alive across parallel builds**: every in-flight build refreshes the idle
  timer (existing `KeepAlive`), so a shared VM is not reaped while any build is
  running.
- This makes the builder VM a per-user singleton; the cache registry daemon is a
  separate per-user singleton.

### Embedded cache registry (msb)
- An OCI registry is embedded in the agents-sandbox binary and started on demand
  (once per user) when an msb build needs `type=registry` cache import/export.
- Properties: pure-Go, cross-platform (Linux + macOS), unprivileged, plain HTTP,
  filesystem-backed storage under the agents-sandbox user state directory.
- **Reachability:** the builder VM reaches the host via
  `host.microsandbox.internal`; the registry must bind a host port/interface
  reachable from the sandbox but not broadly exposed. Bind scope is the most
  security-sensitive detail and must be confined to the sandbox's host gateway
  (not `0.0.0.0`) for the daemon's lifetime.
- **On-demand, shared lifecycle:** the registry is a **per-user singleton
  daemon**, not one-per-build. The first agents-sandbox process that needs it
  re-execs itself in a hidden internal serve mode, detached from the parent, bound
  to the sandbox gateway; it idle-reaps after a quiet period. All instances of
  that user connect to the same daemon → one storage, one cache, no duplicate
  caches and no misses between processes. Only its **storage** persists on disk
  under the agents-sandbox state dir.
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
  and the gateway-confined bind plus token keeps other local processes out; other
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

### BuildKit cache/export contract
- Cache import/export uses `type=registry` against the embedded registry
  (`mode=max`, since the runner image is multi-stage and `mode=min` would drop
  intermediate stages' layers).
- The cache export runs only on a real Dockerfile-step miss (existing
  export-on-miss detection), so a full cache hit skips it.
- Folding image export and cache export into a single solve to hide the
  export latency is **out of scope** for this spec.

### moby backend
- Restores the pre-BuildKit path: tar the build context (Dockerfile plus any
  assets), `ImageBuild` with `Tags`, `Remove`, `NoCache`, and the assembled
  `BuildArgs`, drain the streamed build output for errors, then `ImageSave` to a
  host archive returned as the build result's `ArchivePath`. The shared
  `EnsureLoaded` path then loads that archive into the microsandbox image store
  via `msb.Client.ImageLoad` — the same delivery route the buildkit backend uses,
  so `builder` itself does not depend on an `msb.Client`.
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
- **moby backend** is unit-tested with the engine client stubbed: builds invoke
  `ImageBuild` with the expected options and return an `ImageSave` archive, which
  the shared `EnsureLoaded` path loads into a mocked microsandbox client. Prior
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
- Single-solve combined image+cache export.
- Cache-hit-rate tuning beyond `mode=max` export-on-miss.
- Auto-detection of Docker to switch backends silently (moby is explicit only).

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
  `type=registry` cache export/import, and export-on-miss detection incl. the
  "cached if ever reported so" fix and `FROM`/`[internal]` exclusions) — this *is*
  the default backend. The registry cache is what makes it viable (the old volume
  cache was ~3.6× slower on misses), export-on-miss avoids paying the ~13s export
  on a full hit, and the gateway base-digest route is required for image
  identity/inspection under daemonless builds. Removing any of it means full
  rebuilds or broken identity. (Only the non-registry cache modes are trimmed.)
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
  (tmpfs) storage and the registry cache remain.
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
