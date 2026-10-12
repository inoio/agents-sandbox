# Docker-backed test harness for the BuildKit builder

**Status: implemented** — a test-only `dockerBackend` in `internal/sandbox/builder` builds via a `docker run
moby/buildkit` container, selected by `AGENTS_SANDBOX_BUILDER=docker`; production keeps the msb builder VM (default).
Two tiers exist: `make test-integration` (docker, no KVM) and `make test-integration-msb` (KVM). Implemented on
`opencode/buildkit-builder-vm`; see `plans/2026-10-12-docker-test-harness.md`.

**Environment caveat (R2).** The docker tier does not pass in this dev VM: microsandbox injects an egress-inspection
TLS CA that registry-pulled base images do not trust, so in-build `curl`/`dnf` fail (the limitation in `AGENTS.md`
"Current limitations"). The tier is expected to pass on CI (`ubuntu-latest`, normal TLS egress). Compilation and
non-network tests run anywhere.

## Why

`specs/buildkit-builder-vm.md` moved runner-image builds into a `buildkitd` running inside a microsandbox
(libkrun) VM, which needs `/dev/kvm`. That makes the builder-VM integration tests unrunnable in the two
places developers and CI most need them:

- **GitHub PR CI**: hosted arm64 runners expose no KVM, and hosted x64 KVM is undocumented and gated to
  main/tags/manual (see `.github/workflows/ci.yml`). Before this harness, PRs never exercised the build path.
- **Agent sessions**: the agents-sandbox dev VM has a working Docker daemon but no functional `/dev/kvm`,
  so an agent cannot run the msb integration tests that validate its own change (it can still run the docker
  tier for non-network paths, with the R2 caveat above).

The build pipeline itself does not need KVM. Only two things do:

1. **Obtaining a `buildkitd` endpoint** — `VM.Ensure` in `internal/sandbox/builder/vm.go` creates the msb VM,
   publishes a loopback port, mounts the cache volume, and starts `buildkitd`.
2. **Loading the built image into the msb image cache** — `msb.ImageLoad`, used by the runner path and by
   `TestBuildRunnerImageInBuilderVM`.

Everything else — the host-side BuildKit client, `dockerfile.v0` solve, `type=oci` export, gateway
`ResolveImageConfig`, `forwardProgress`, registry auth — is backend-agnostic. Notably the builder image is
already a container image (`moby/buildkit:v0.25.2`), so a Docker-backed `buildkitd` is a near drop-in for the
same host-side client.

**This is a test harness only.** Production keeps using the msb builder VM; there is no runtime Docker
fallback.

## Goals

- The builder's build path (rendered Dockerfile → OCI image, layer-cache semantics) is exercisable with only
  a Docker daemon — no KVM, no msb.
- Integration tests that validate the build/composition run on **every PR** (hosted runners, both x64 and
  arm64) and in an agent's dev VM.
- Behavior tests that need to *run* the built image (node/npm starts, login-shell PATH) also run without KVM,
  by using Docker as the runtime instead of an msb sandbox.
- The production msb path stays covered by a KVM-gated tier.

## Non-goals

- No production Docker fallback and no change to how end users build images.
- No change to image content, identity, or the `dockerfile-id` skip-build contract.
- No reliance on `docker buildx`. The Docker backend drives `buildkitd` through the same host-side Go
  BuildKit client as production, over a published loopback port, so only the `docker` CLI is required.
  `buildx` may be present in the dev VM and is useful for manual debugging and for verifying the
  export/`docker load` question, but it is not a dependency of the harness or the tests.

## Anti-goals

- A docker backend that silently diverges from the msb path (different export format, different build args,
  different base-digest resolution) such that a green docker-tier test does not predict production behavior.
- Making the existing msb integration tests skip silently when the docker backend is selected; skips must be
  explicit and attributed.

## Current state (grounded)

- `internal/sandbox/builder/builder.go`: `Builder` is a two-method interface (`Build`, `PruneCache`);
  `var Get = func() Builder` is the injection seam (`var Get` in `client.go`). Constants: builder image
  `moby/buildkit:v0.25.2`, guest port `1234`, cache volume `agents-sandbox-buildkit-cache` →
  `/var/lib/buildkit`, host port base `11234`, idle timeout `5m`.
- `internal/sandbox/builder/client.go`: `Client.Build` = `c.backend.Ensure(ctx)` (port) → `newBuildkitClient`,
  then `resolveBaseDigest` + `Solve` with the `dockerfile.v0` frontend, an export per `backend.Export(tag)`
  (`type=oci` for msb, `type=docker name=<tag>` for docker) written to a temp file, and `registryAuthSession()`.
  `resolveBaseDigest` now qualifies short base refs (`qualifyBaseRef`, e.g. `debian:trixie-slim` →
  `docker.io/library/debian:trixie-slim`) before the gateway resolves them, fixing base-digest resolution for
  unqualified refs.
- `internal/sandbox/builder/vm.go`: msb-specific endpoint acquisition (`GetSandbox`/`CreateSandbox`, port
  binding, named-volume mount, `exec buildkitd --oci-worker-snapshotter native --addr tcp://0.0.0.0:1234`)
  and `PruneCache` (`buildctl ... prune` in the VM). A host-wide flock serializes creation.
- `internal/sandbox/image/integration_build_test.go`: the integration-tagged end-to-end tests. They build via
  `builder.Get().Build` and, for behavior, run the image via `docker run` (docker tier) or an msb sandbox (msb
  tier); gated by `requireIntegration` (`AGENTS_SANDBOX_INTEGRATION`). `useRealHostRuntime` restores the real msb
  client.
- `internal/sandbox/image/integration_harness_test.go`: the per-run harness setup. It resolves `activeBackend`
  from `AGENTS_SANDBOX_BUILDER` (default `msb`) and, when the docker backend is under test, exposes
  `setupIntegrationHarness` — invoked by the `image` package `TestMain` around the suite — which resets the docker
  harness (container + volume) via `builder.ResetDockerHarness`. A no-op for the msb tier.
- Dev VM: Docker 29.7.2, `vfs` storage driver (classic image store). `buildx` may be supplied for
  debugging/spikes but the harness does not require it.
- MSB runtime install is SDK-driven and non-interactive (`EnsureRuntime` → libkrun + libkrunfw); CI triggers
  it via `agents-sandbox doctor`.

## Design

### Backend seam

Split endpoint acquisition from solving. `Client` keeps all solve/export/ResolveImageConfig logic and gains a
backend that answers "give me a ready `buildkitd` TCP endpoint (and its cache volume)".

```
type backend interface {
    // Ensure returns the host TCP port of a ready buildkitd.
    Ensure(ctx) (port int, err error)
    // Export describes how a solved image leaves the builder (type + attrs).
    Export(tag string) exportSpec
    // PruneCache reclaims buildkitd's cache.
    PruneCache(ctx) error
}

type msbBackend struct{ vm *VM }          // VM.Ensure / VM.PruneCache; Export = type=oci
type dockerBackend struct{ ... }          // docker run moby/buildkit; Export = type=docker (name=tag)
```

`Client.Build` becomes: `port := backend.Ensure(ctx)` → the existing `newBuildkitClient(hostPort)` →
`backend.Export(tag)` drives the export → unchanged solve path. The msb backend preserves the flock, named cache
volume, `native` snapshotter, and idle timeout.

### Docker backend

- Start `moby/buildkit:v0.25.2` detached with a published loopback port:
  `docker run -d --privileged --name agents-sandbox-buildkit-harness -p 127.0.0.1:<freePort>:1234 -v
  agents-sandbox-buildkit-harness-cache:/var/lib/buildkit moby/buildkit:v0.25.2 --oci-worker-snapshotter
  native --addr tcp://0.0.0.0:1234`. `--privileged` is required for the in-container worker; the `native`
  snapshotter avoids overlayfs constraints under the dev VM's `vfs` driver.
- **Ephemeral cache volume**, fixed name `agents-sandbox-buildkit-harness-cache`, reset between runs via
  `builder.ResetDockerHarness` (removes the container and volume) from the integration `TestMain`, so the tier
  starts from a clean cache and cross-run state cannot leak. This makes the negative-cache guard
  (`TestRenderDockerfileAgentVersionRebuildsInstall`) deterministic: with a persistent shared volume that
  layer could already exist on a warm run and the "must not be cached" assertion would flake. Cross-
  run cache persistence is a property of the msb backend and is tested there
  (`TestBuilderCacheReuseAcrossVMs`), not in the docker tier.
- Reuse the running container across builds within one invocation; allocate a free host port only on create.
- Readiness: retry `newBuildkitClient` (`ListWorkers`) until it answers or the existing `builderReadyTimeout`
  elapses.
- Prune: `docker exec <name> buildctl --addr tcp://127.0.0.1:1234 prune --keep-duration 24h --all`. No-op when
  no container exists.
- **Lifecycle and cleanup**: one container + one volume per `go test` invocation, reset by the integration
  `TestMain` (there is already a `TestMain` in `internal/sandbox/image`), which removes them at start and exit.
  No idle-timeout equivalent is needed because the harness tears down at process end.
- Drive the `docker` CLI through a small injectable `dockerCLI` interface so the backend is unit-testable
  with a fake, mirroring the existing `buildkitClient`/`msb` seams. Do not re-add the moby SDK client.
- Requires: `docker` on PATH and a reachable daemon. On macOS this is Docker Desktop; on the dev VM it is the
  in-VM daemon.

### Selection

- Env `AGENTS_SANDBOX_BUILDER` (`builderBackendEnv`) = `docker` (harness) | anything else / unset (msb,
  production). `newBackend()` reads the env **at call time** (not once at init), so a test's `t.Setenv` before
  calling `builder.Get()` selects the backend for that test. msb-specific tests select `msb` explicitly rather
  than relying on the default, so the KVM tier does not depend on a default that must stay flipped.
- **Unit tests must not assume the msb default.** Because `make test-integration` exports
  `AGENTS_SANDBOX_BUILDER=docker` for the whole `./...` run, any unit test that constructs the production client
  (`newClient()`/`Get()`) must pin the backend it asserts on via `t.Setenv(builderBackendEnv, builderBackendMsb)`
  (or `...Docker`). `TestNewClientWiresReadySeam` pins msb this way; its assertion is unchanged.

### Test boundary (the `image` package stays untouched)

- Docker-tier tests call `builder.Get().Build` **directly** and load/run the result themselves via the Docker
  daemon. They do **not** call `image.EnsureImageWithClient` or `image.EnsureLoaded`, both of which are msb-only
  (`msb.ImageLoad` / `msb.ImageInspect`). Production `internal/sandbox/image` code and its unit tests are
  therefore unchanged — "callers untouched" holds because the docker tier bypasses the msb load path rather
  than rerouting it.
- To run the result, the docker backend's export carries `name=<req.Tag>` so a subsequent load yields a tagged
  image, and the harness `docker run`s that tag. (Production keeps the untagged `type=oci` archive + explicit
  `msb.ImageLoad` ref.)
- Consequence: a docker-tier `BuildResult.Digest`/`Env` is asserted only where the exporter's response keys
  match. If decision 1 falls back to `type=docker`, the `containerimage.config[.digest]` parsing in
  `client.go` may not populate for the docker tier, so digest/env assertions are docker-tier-local (or skipped)
  and are not used to predict production values. This is the one place the tiers are allowed to differ; the
  build/cache *behavior* assertions still run identically in both tiers.

### Export and running the built image

- The docker backend exports `type=docker` with `name=<req.Tag>` (verified during implementation: Docker 29.7.2
  with the `vfs`/classic store is used via `docker load`/`docker run`), so behavior tests can `docker run` the
  tagged image without msb. Production stays `type=oci` + `msb.ImageLoad` (untagged archive + explicit ref).
- Consequence: a docker-tier `BuildResult.Digest`/`Env` is not asserted, because `type=docker` does not populate
  the `containerimage.config[.digest]` response keys that production `type=oci` does. The docker tier therefore
  asserts build/cache *behavior* only, not digest/env; those are not used to predict production values. This is
  the one place the tiers intentionally differ.
- Behavior tests (node/npm runs, login-shell PATH) run the image via `docker run` under the docker backend and
  via an msb sandbox under the msb backend.

### Test tiers and CI

- **`integration` (docker-backed, PR):** the composition matrix, the tool-layer/agent-version cache guards,
  a builder-backend test (endpoint + solve + export), and both behavior tests
  (`TestRunnerImageNodeAgentNodeRuns`, `TestRunnerImageLoginShellPath`) via `docker run`. The tier itself is
  arch-agnostic (any host with Docker + normal TLS egress), but CI currently runs it on x64 only
  (`runs-on: ubuntu-latest`) on every PR.
- **`integration-msb` (KVM):** `TestBuildRunnerImageInBuilderVM` (build → `msb.ImageLoad` → inspect) and
  `TestBuilderCacheReuseAcrossVMs` (msb VM teardown proves the persistent cache volume), plus any pure msb
  lifecycle tests. Stays on the x64 `ubuntu-latest` job with the KVM/msb setup, gated to main/tags/manual.
- msb-only tests skip explicitly (named skip reason) when the backend is `docker` or KVM is absent; the
  docker tier never silently skips.
- Make targets: `make test-integration` (docker, `AGENTS_SANDBOX_BUILDER=docker`) and `make test-integration-msb`
  (`AGENTS_SANDBOX_BUILDER=msb`). CI wires the former into the `integration` job on every PR and the latter into
  the gated `integration-msb` job; `release.needs` includes both.

## Production impact

- None intended. `AGENTS_SANDBOX_BUILDER` defaults to `msb`; the msb backend is the current code path.
- The `Builder` interface and `var Get` seam are unchanged, so `internal/sandbox/image` callers are untouched.

## Verification outcomes

- **OCI vs docker `docker load`**: resolved to `type=docker` with `name=<tag>` for the docker backend (Docker
  29.7.2, `vfs`/classic store). Digest/env assertions are docker-tier-local; the build/cache behavioral
  assertions still run identically in both tiers.
- **`--privileged` + `native` snapshotter** (verified 2026-10-12): `docker run -d --privileged ...
  moby/buildkit:v0.25.2 --oci-worker-snapshotter native --addr tcp://0.0.0.0:1234` starts under the dev VM's
  `vfs` store; the worker registers and the published endpoint answers. Without `--privileged` the container
  exits immediately (exit 1) — `--privileged` is required.
- **Dev-VM egress TLS**: `docker pull moby/buildkit:v0.25.2` succeeds in the dev VM despite the injected
  inspection CA. The R2 caveat above is about in-build pulls inside the built image, not the buildkit image.
- **`buildctl ... prune --filter until=24h` was rejected by buildkit v0.25.2** (found and fixed 2026-10-12):
  both `--filter until=24h` and `--filter=until=24h` fail with `failed to parse prune filters ... unsupported
  operator "="`; `--keep-duration 24h --all` is the working equivalent. The invalid filter was shared by the msb
  backend's `buildctlPruneCommand` (`internal/sandbox/builder/builder.go`), so production `PruneCache` was
  latently broken too. Both invoke sites (msb `buildctlPruneCommand` and the docker backend's `PruneCache` args)
  now use `prune --keep-duration 24h --all`, with `vm_test.go`/`docker_test.go` pinning the new command.

## Resolved calls

1. **Export format**: production keeps `type=oci`; the docker backend uses `type=docker` with `name=<tag>`. The
   divergence is test-only; digest/env assertions degrade per the "Test boundary" section.
2. **Docker-backend client**: shell out to the `docker` CLI behind a small `dockerCLI` interface; no new dependency.
3. **Tier mechanism**: env vars (`AGENTS_SANDBOX_BUILDER`, `AGENTS_SANDBOX_INTEGRATION`) with call-time reads and
   `t.Setenv`; one binary compiles once. No build-tag split.
4. **Behavior tests move to `docker run`.** `TestRunnerImageNodeAgentNodeRuns` (node/npm starts) and
   `TestRunnerImageLoginShellPath` run the built image via `docker run` in the docker tier, so the PR tier is
   fully KVM-free and covers the #132 class of behavior regressions. Under the msb tier they still run via an
   msb sandbox.
