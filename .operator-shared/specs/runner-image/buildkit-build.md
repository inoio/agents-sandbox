---
description: Contract for the BuildKit runner-image build path and its engine-selected classic fallback - the builder is chosen once per build from the daemon's `Builder-Version` ping (dockerd family -> BuildKit, podman -> classic), the BuildKit client reaches the daemon's embedded builder over the `/grpc`+`/session` hijack, and a config-driven cache import/export surface (`type=gha`) makes the CI integration build cacheable.
read_if: Changing how the runner image is built, the builder selection, `buildImage`/`buildDockerImage`, the docker seam's `DialHijack`, `image-cache-from`/`image-cache-to`, or the BuildKit client wiring; diagnosing a runner-image build that used the wrong builder, a base-image pull/auth failure on the BuildKit path, or a fallback that did not happen.
---

# BuildKit runner-image build

The runner image is built through one of two builders, chosen per build from the engine. The BuildKit path is what
lets the CI integration build export and restore a cache; the classic path is retained so podman keeps working. The
source-backed engine compatibility matrix is `../../guides/buildkit-engine-compatibility.md`. Content addressing
(`ImageInfo`, the msb load decision) is `image-build.md`; the rendered Dockerfile is `image-composition.md`.

## Builder selection

`buildImage` reads the daemon's recommended builder once per build command from
`docker.Client.Ping(ctx).BuilderVersion` (the `Builder-Version` response header):

- `build.BuilderBuildKit` (`"2"`) -> the BuildKit path. The dockerd family (Docker Engine, Docker Desktop, colima's
  docker runtime, OrbStack, Rancher Desktop in moby mode, Lima's docker template) advertises this.
- `build.BuilderV1` (`"1"`) or empty -> the classic path, unchanged. podman's compat `/ping` advertises `"1"`.

Selection is **engine-gated only**. A BuildKit build failure is returned as-is; the launcher never retries with the
classic builder. A silent downgrade would hide a real failure and, on the cache surface below, silently stop
exporting the cache. The ping is advisory (moby documents `Builder-Version` as a recommendation), so the contract is
"the daemon says BuildKit, so we ask for BuildKit" — not a capability probe.

## The BuildKit path

The BuildKit path uses `github.com/moby/buildkit/client` and reaches the daemon's **embedded** BuildKit through the
Docker API's hijack endpoints, the way buildx's `docker` driver does: `DialHijack("/grpc", "h2c", meta)` for the
control connection and `DialHijack("/session", proto, meta)` for the session. The `docker.Client` seam therefore
grows a `DialHijack` method; there is no separate gRPC address for the embedded builder.

A BuildKit **session** carries what the classic build got from the daemon:

- the build context, from a temp directory holding the rendered Dockerfile plus `buildContextFiles()` (the same
  single source of truth the classic context tar uses), transferred by filesync;
- registry auth, from the user's docker credential configuration, so a private base image resolves on this path as it
  does on the classic path.

The build runs the **`dockerfile.v0`** frontend and exports with the daemon's **`moby`** exporter under the project's
Docker tag (`naming.DockerImageRef`), so the produced image is indistinguishable from the classic path's:
`ImageInspect` reads back the same `Digest`, and every downstream consumer (the msb load decision, prune, reconfig)
is builder-agnostic. `NoCache` maps to BuildKit's no-cache; each build arg maps to a `build-arg:<name>` frontend
attribute.

The BuildKit dependency is not on `moby/moby`, so it adds no conflict with the split `moby/moby/api` +
`moby/moby/client` modules the launcher already uses.

## Build cache

The BuildKit path accepts cache **import** and **export** entries (`client.CacheOptionsEntry`). They are a build-time
optimization only:

- A cache miss never changes the produced image and never fails the build.
- Import and export are configured separately: a PR restores an existing cache without publishing a throwaway one,
  main push restores and refreshes the shared cache. A cache a PR run *creates* is scoped to `refs/pull/.../merge`
  and is invisible to main and sibling PRs, so main (a `push`) is the single writer; PRs are restore-only.
- The cache is off unless configured; a normal local build carries none.

The concrete backend the CI uses is `type=gha`, whose exporter is BuildKit's (`cache/remotecache/gha`): it reads the
GitHub Actions cache environment itself, so the launcher only has to be told *which entries* to use, not the cache
service URLs.

### Configuration

One config key per direction, `image-cache-from` and `image-cache-to` (each a `[]string`, env-bound, with no CLI
flag), holding BuildKit cache specs such as `type=gha,scope=runner-image,mode=max`. Empty — the default — means no
entry. The CI sets `image-cache-to` on a main push (the single writer) and only `image-cache-from` on a PR, so a PR
never publishes a throwaway cache. The cache spec syntax is BuildKit's; the launcher does not interpret it beyond
passing it through. Config-key registration (the golden env-key list, docs) follows
`../configuration/config-keys.md`.

The CI job gating and the main-writer/PR-restore split are owned by `../ci/ci-pipeline.md` once that section lands
with the implementation.

## Engine contract

- **BuildKit** (advertises `2`): the dockerd family above. A user Dockerfile using BuildKit-only syntax (heredoc
  `RUN`, `RUN --mount`) builds here.
- **Classic** (advertises `1`): podman (CLI, Podman Desktop, podman machines). Such a Dockerfile fails here; that
  divergence is documented in `docs/troubleshooting.md`.
- **Unsupported** (no Docker API): colima `--runtime containerd`, Rancher Desktop's containerd mode — unchanged by
  this feature.

## Invariants

- Builder selection is from the ping alone; no per-image probing and no retry into the other builder.
- For a given render and cache state the two paths produce the same image content; `ImageInfo` is the only interface
  downstream code sees, so no caller branches on the builder.
- The classic path stays exercised (the `integration-podman` CI job), so the fallback cannot rot unnoticed.
- The cache surface never changes build correctness: a disabled, empty, or failing cache is indistinguishable from a
  cache miss.

## Non-goals / limitations

- Windows native images are out of scope (the launcher targets Linux and macOS Apple Silicon).
- The render and the content-addressed reference are unchanged; only the builder that consumes the render changes.
- The repository engine cannot run on the dev VM, so the BuildKit path is verified on a host (`make test-integration`
  / `make test-msb`), like the rest of the engine build.
