---
description: Which container engines agents-sandbox supports would survive switching the runner-image build to BuildKit, per mechanism (moby `/build?version=2` vs a real BuildKit client), and what the BuildKit feature gaps are.
read_if: Considering or implementing a BuildKit runner-image build, or diagnosing a runner-image build that fails on a non-Docker engine.
---

# BuildKit engine compatibility

The contract this study informs is `../specs/runner-image/buildkit-build.md`; the implementation is planned in
`../plans/2026-11-08-buildkit-runner-image-build.md`.

The question: if agents-sandbox set `ImageBuildOptions.Version = client.BuilderBuildKit`
(`"2"`) on its `dclient.ImageBuild` call, which engines it claims today would still build,
per mechanism? Facts below are pinned to primary sources and dated where the upstream state
has moved recently (checked 2026-11-08).

## Summary

- **The two mechanisms do not break the same engines.** Mechanism 1 (`POST /build?version=2`,
  the single REST call the moby client already makes) is served only by the dockerd family.
  Mechanism 2 (a real BuildKit client — buildx/buildkit over the daemon's `/grpc` + `/session`
  hijack, or a standalone BuildKit daemon) also works only against dockerd, and additionally
  fails against podman.
- **Docker Engine, Docker Desktop, colima (docker runtime), OrbStack, Rancher Desktop (moby
  mode), Lima (`template:docker`)** all run a stock dockerd, so all serve `/build?version=2`
  and BuildKit is their default builder since Docker Engine 23.0 (2023-02-01). They survive a
  switch in the sense that the endpoint exists — but see the caveat below.
- **The dockerd-family path has a real correctness gap today.** The moby `ImageBuild` client
  establishes **no BuildKit session**, and BuildKit resolves/pulls each `FROM` through the
  session. Without one, base-image resolution failed with `…: no active sessions`. Public
  (anonymous) registry pulls were fixed upstream in BuildKit v0.31.0 (2026-06-17); private
  registry credentials (`X-Registry-Config`) are still dropped on the `/build?version=2` path
  (moby PR #52565 open as of 2026-11-02). Any engine whose bundled BuildKit predates the fix
  still fails on public base images too. agents-sandbox's base is
  `docker.io/library/debian:trixie-slim`, so this is the load-bearing risk.
- **podman** (CLI/engine, Podman Desktop, podman machines) does not break under mechanism 1
  for the wrong reason: its Docker-compat `/build` handler has **no `version` parameter**, so it
  ignores `version=2` and builds with Buildah. The build still succeeds, but it never runs
  BuildKit. Under mechanism 2 podman **does** break: buildx's docker driver dials `/grpc`
  (h2c), which podman answers 404.
- **colima `--runtime containerd` and Rancher Desktop containerd mode** expose no Docker API at
  all; they are already unsupported and stay unsupported under either mechanism.
- **The render's own Dockerfile features are all BuildKit-safe** (multi-stage `COPY --from`,
  `RUN`, `ENV`, build args, `--no-cache`). `Remove` is inert under BuildKit. BuildKit-only
  syntax in a *user's* project Dockerfile (heredoc `RUN`, `RUN --mount`) currently fails on the
  classic builder and would start working under BuildKit.

## Engine-by-engine matrix

Legend: **M1** = moby client `/build?version=2`; **M2** = real BuildKit client.

| Engine / mode | Docker API socket? | `docker build` uses BuildKit | Serves `POST /build?version=2` | M2 builder reachable | Verdict under a switch |
|---|---|---|---|---|---|
| Docker Engine (Linux) | yes | yes, default since 23.0 | yes (`builder/backend` dispatches `BuilderBuildKit`) | yes — daemon's embedded BuildKit via `/grpc`+`/session` | survives; carries the session/auth caveat below |
| Docker Desktop | yes | yes, default | yes (bundles dockerd) | yes (ships buildx + BuildKit) | survives; same caveat |
| colima, default docker runtime | yes | yes (stock dockerd) | yes | yes (needs buildx plugin installed) | survives; same caveat |
| colima `--runtime containerd` | no | n/a (nerdctl/BuildKit, no Docker API) | no | n/a | already unsupported; unchanged |
| podman (engine), Podman Desktop, podman machines | yes | n/a — Buildah builder, not BuildKit | handler ignores `version` (no such field); builds with Buildah | no — `/grpc` 404 | M1 "works" but silently runs Buildah (not BuildKit); M2 breaks |
| Rancher Desktop, moby mode | yes | yes (stock dockerd) | yes | yes | survives; same caveat |
| Rancher Desktop, containerd mode | no | n/a | no | n/a | already unsupported; unchanged |
| OrbStack | yes | yes — "unmodified Docker engine", BuildKit backend, ships buildx | yes | yes | survives; same caveat |
| Lima, `template:docker` | yes | yes (installs a stock dockerd via `get.docker.com`, rootless) | yes | yes | survives; same caveat |

"Same caveat" = the no-session registry-resolution gap described in the summary and in
[Mechanism 1](#mechanism-1-moby-buildversion2-no-session).

## Mechanism 1: moby `/build?version=2`, no session

What the client actually sends (moby `client/image_build.go`): `POST /build` with
`version=2`, the build args/tags/nocache/rm as query params, and the `X-Registry-Config`
header built from `ImageBuildOptions.AuthConfigs`. It sets the `session` query param **only**
if `ImageBuildOptions.SessionID` is non-empty; agents-sandbox leaves it empty.

- **Default is v1.** The daemon's `/build` defaults to `version=1` (classic, deprecated);
  `version=2` is opt-in and selects the daemon's embedded BuildKit
  (`api/swagger.yaml`; `daemon/server/router/build/build_routes.go`;
  `daemon/builder/backend/backend.go`, `useBuildKit := options.Version == …BuilderBuildKit`).
- **`features.buildkit` is only a recommendation.** The daemon's `BuilderVersion(features)`
  comment (moby `daemon/server/router/build/build.go`) states the value is "only a
  recommendation as advertised by the daemon, and it is up to the client to choose which
  builder to use." The actual dispatch is by the request's `version` param. `--features
  buildkit=false` / `DOCKER_BUILDKIT=0` affect what `/info` advertises, not whether
  `version=2` is served.
- **No session → registry resolution can fail.** BuildKit asks the client session for token
  authority/credentials before building token options; with no session the group is empty and
  it returns `no active sessions` (moby issue #48112, open; diagnose comments 2025-05-28 and
  2026-05-06, plus a 2026-03 regression report). Practical effect: a `FROM` whose image is not
  resolvable without a pull, or whose metadata must be fetched, fails at parse/resolve time.
- **`X-Registry-Config` is dropped on this path.** `daemon/internal/builder-next` forwards only
  the caller session ID and discards `AuthConfigs` (moby #48112; fix PR moby #52565,
  "[wip] builder-next: pass build auth configs through BuildKit sessions", still **open** and
  last updated 2026-11-02). Private base images therefore cannot authenticate.
- **Public-image fix status.** BuildKit merged anonymous-Bearer fallback when no session is
  active (PR #6744, merged 2026-05-06), then restored/refined it after a regression (PR #6760,
  merged 2026-06-03), documented in the **BuildKit v0.31.0 release notes (2026-06-17)**: "In
  special modes where the client does not expose the session connection to transfer
  credentials, builds can now still fall back to anonymous registry auth instead of erroring."
  An engine only benefits if the BuildKit it bundles is ≥ the release carrying this.
- **Session-only build features are unreachable.** Build secrets, SSH forwarding, and
  `RUN --mount=type=secret/ssh` are delivered *through the session*, which the moby
  `ImageBuild` client cannot provide. So `version=2` alone cannot build a user Dockerfile that
  relies on `--secret`/`--ssh`, even though BuildKit supports them.
- **`# syntax=` frontends.** A user Dockerfile beginning `# syntax=docker/dockerfile:1` makes
  BuildKit pull that frontend image; under mechanism 1 with no session this fails the same way
  (`failed to resolve source metadata for docker.io/docker/dockerfile:1: no active sessions`,
  in #48112). The classic builder ignores `# syntax` entirely.

## Mechanism 2: a real BuildKit client

A real BuildKit client does not use `/build` at all. buildx's **docker driver** hijacks two
endpoints on the daemon (buildx `driver/docker/driver.go`):

- `DialHijack(ctx, "/grpc", "h2c", meta)` for the BuildKit control connection, and
- `/session` (via `client.WithSessionDialer`) for the session services (auth, secrets, ssh).

That is why buildx's docker driver works where the bare `/build?version=2` call fails, and
why it fails on podman: podman answers the h2c upgrade with 404 (`failed to dial gRPC: unable
to upgrade to h2c, received 404`, podman issue #17836). Buildx's `docker` driver also uses the
daemon's bundled BuildKit ("The BuildKit server components built directly into the Docker
Engine", buildx driver docs), so it exists on every dockerd-family engine above.

For podman, mechanism 2 requires a **separate** BuildKit, not the podman socket:

- buildx `docker-container` driver — create a `moby/buildkit` container with the engine's
  container API; or
- buildx `remote` driver, or BuildKit's own `podman-container://` scheme (`buildctl`), pointed
  at a BuildKit daemon running as a podman container (buildkit README "Containerizing
  BuildKit → Podman").

This is a heavier change than mechanism 1: it means depending on a BuildKit client library and a
session (and for the GHA cache goal, on BuildKit's cache-export path), not just setting a string
field.

### Which client

- **`github.com/moby/buildkit/client`** (the `buildctl` library) is the direct choice. Reaching a
  **dockerd**'s embedded BuildKit needs no network address: build through the same hijack buildx
  uses — `client.WithContextDialer` dialing `/grpc` (`h2c`) and `client.WithSessionDialer`
  dialing `/session` (`buildx driver/docker/driver.go`). The pinned `moby/moby/client v0.6.2`
  already has `DialHijack`, but the launcher's `docker.Client` seam does not expose it, so the
  seam gains a method. The build then supplies a `session` for the build context (`filesync`) and
  registry auth (`authprovider`), names the `dockerfile.v0` frontend, runs `client.Solve`, and
  passes `CacheExports: []client.CacheOptionsEntry{{Type: "gha", Attrs: {...}}}`. Cost: it
  re-implements buildx's build wiring; `cmd/buildctl/build` is the reference.
- **`github.com/docker/buildx` as a library** (`build.Build` / `Options.CacheTo`) is the
  alternative — least reimplementation, but buildx is a CLI plugin, not a versioned public API,
  and it drags `github.com/docker/docker v28.5.2+incompatible` plus its own pinned buildkit into
  the module graph; constructing a `builder.Node` outside buildx's store is awkward.
- **Shelling out to `docker buildx build`** avoids the dependency but adds a binary requirement,
  breaks the "dial any Docker-API engine" architecture and the test seam, and podman has no
  buildx.
- The **cache backends are BuildKit's, not buildx's** (`github.com/moby/buildkit/cache/
  remotecache/gha` and `.../registry`): the client sets `CacheOptionsEntry{Type: "gha"}` (or
  `"registry"`), and for `gha` BuildKit reads the GitHub env (`ACTIONS_CACHE_URL` /
  `ACTIONS_RESULTS_URL` / `ACTIONS_CACHE_SERVICE_V2`), so either is available on either client path.

The buildkit client keeps the launcher's Go-client, no-shelling-out model and couples only to
`github.com/moby/buildkit` (which does not depend on `moby/moby`, so it adds no split-module
conflict), at the cost of writing the session/context/frontend glue.

## Fallback: an engine-selected builder

"Use BuildKit where available, fall back to classic" is implementable, and the signal already
exists — no new dependency for the detection:

- The daemon advertises its recommended builder on `GET /_ping` in the `Builder-Version`
  response header, surfaced by the moby Go client as `client.PingResult.BuilderVersion`
  (`BuilderV1 = "1"`, `BuilderBuildKit = "2"`). The launcher's docker seam already exposes `Ping`
  (`internal/sandbox/docker`), and the pinned `github.com/moby/moby/client v0.6.2` carries the
  field.
- **dockerd (Linux) advertises `2`** since Engine 23.0 (its default builder). **podman's compat
  `/ping` advertises `1`** with an empty `BuildKit-Version` (podman
  `pkg/api/handlers/compat/ping.go`: "Docker uses Builder-Version 1 for classic builder and 2 for
  BuildKit"). So a ping-gated choice keeps podman on the classic path with no build-time probe —
  and unlike `/build?version=2`, which podman silently ignores, podman tells the truth on `/ping`.
- The header is explicitly "only a recommendation as advertised by the daemon" (moby
  `daemon/server/router/build/build.go`), so it is a hint the client acts on, not a capability
  guarantee.

Two consequences for the CI-cache goal:

- **Mechanism 1** makes this a one-field change (`Version` from the ping), but does **not** deliver
  cache export: `ImageBuild` has no `CacheTo`, so a BuildKit cache stays out of reach. It also leaves
  podman unchanged — podman ignored `version` already, so a fallback adds nothing there.
- **Mechanism 2** is what makes the fallback load-bearing: production would carry both the BuildKit
  client and the existing classic moby path, choosing by the ping. The BuildKit branch is what
  exports the cache and what the Docker integration job must exercise; the podman job then
  exercises the classic fallback. That reframes the "podman Tested claim": podman support is
  **retained on the older builder**, not lost, but the claim should say so.

Feature divergence travels with the choice: a user Dockerfile using heredoc `RUN` or `RUN --mount`
builds under the BuildKit branch and fails under the classic fallback, so it is per-engine
behaviour the troubleshooting docs must own.

## Feature support / correctness

The render (`internal/sandbox/image/dockerfile.go`) is builder-neutral: per-tool multi-stage
stages, `COPY --from=<stage>`, `RUN`, `ENV`, `ARG`/build args, `LABEL`. All are supported by
BuildKit's Dockerfile frontend (Dockerfile reference). Specifically:

- **Multi-stage `COPY --from`** — supported by both builders. BuildKit differs on which stage
  config (labels/ENV) leaks: the render already accounts for this by emitting agent `ENV`/labels
  in the final stage (see the comments in `agentStage` / `agentImageConfigBlock`).
- **`RUN`, `ENV`, build args (`BuildArgs`)** — supported.
- **`--no-cache` (`NoCache` → `nocache=1`)** — supported.
- **`Remove`** — inert under BuildKit. The classic builder creates and removes intermediate
  containers; BuildKit "avoids side effects with rest of the API (intermediate images and
  containers)" (Docker BuildKit docs). The `rm`/`forcerm` params are accepted but have nothing
  to remove.
- **`COPY --chown=dev:dev`** — BuildKit is *more* lenient than the classic builder here: the
  classic builder fails on an unresolvable user/group name, BuildKit falls back to the user's
  primary group (noted in `agentToolTreeCopy`). A switch cannot regress this.
- **User BuildKit-only syntax** — heredoc `RUN` and `RUN --mount` are Dockerfile-*frontend*
  features, so the classic builder (which does not use the frontend) does not implement them;
  heredocs reached the stable frontend in `dockerfile/1.4.0` (2022-03-09). Switching to BuildKit
  fixes builds that today parse but fail. `# syntax=` directives likewise only matter under
  BuildKit.
- **podman under mechanism 1** still uses Buildah, not BuildKit. Buildah gained heredoc support
  in Podman 4.8 (release notes); broader BuildKit Dockerfile-frontend parity (`RUN --mount`
  forms, `# syntax` frontends, GHA-cache export) is not equivalent to running BuildKit.

## Unverified / could not confirm from primary sources

- **Docker Engine → bundled BuildKit version mapping.** The public-image fix is documented for
  BuildKit v0.31.0, but no first-party source maps a given Docker Engine/Desktop release to the
  BuildKit version it embeds, so "engine X is safe" cannot be asserted from docs. Do not assume
  an installed engine has the fix.
- **Whether BuildKit v0.30.0 (2026-05-13) shipped #6744.** #6744 merged 2026-05-06 and #6760
  says it "restores" that behavior, so v0.30.0's status is ambiguous; treat v0.31.0 as the first
  release with the documented behavior.
- **`features.buildkit=false` runtime behavior.** The source comment implies it only changes the
  advertised default and `version=2` would still be served, but no doc/test explicitly confirms
  the endpoint still works when BuildKit is disabled — treat as inferred.
- **buildx `docker-container` driver against a podman socket.** Individual reports and BuildKit's
  `podman-container://` integration exist, but no first-party buildx doc states the
  docker-container driver is supported on podman. Untested here.
- **Rancher Desktop default engine.** Current docs present containerd and dockerd (moby) as
  options without stating the default; older third-party posts say containerd. Only the moby
  mode exposes the Docker API.
- **Buildah `RUN --mount` parity** with BuildKit (beyond heredoc) was not verified.

## Sources

- moby Engine API swagger, `/build` `version` param (default `1` = classic, `2` = BuildKit):
  <https://github.com/moby/moby/blob/master/api/swagger.yaml>
- moby daemon `/build` option parsing (defaults to `BuilderV1`):
  <https://github.com/moby/moby/blob/master/daemon/server/router/build/build_routes.go>
- moby build dispatch (`useBuildKit := options.Version == BuilderBuildKit`):
  <https://github.com/moby/moby/blob/master/daemon/builder/backend/backend.go>
- moby advertised builder version (`features.buildkit` is a recommendation):
  <https://github.com/moby/moby/blob/master/daemon/server/router/build/build.go>
- moby client `ImageBuild` (sets `session` only when `SessionID` is set; always sets
  `X-Registry-Config`): <https://github.com/moby/moby/blob/master/client/image_build.go>
- moby `Ping` `Builder-Version` header (`client.PingResult.BuilderVersion`; `BuilderV1`/
  `BuilderBuildKit`): <https://github.com/moby/moby/blob/master/client/ping.go>,
  <https://github.com/moby/moby/blob/master/api/types/build/build.go>
- podman compat `/ping` (`Builder-Version: 1`, empty `BuildKit-Version`):
  <https://github.com/containers/podman/blob/main/pkg/api/handlers/compat/ping.go>
- moby issue #48112, "API `/build` doesn't pass AuthConfig to BuildKit" (open; session/auth
  comments through 2026-05-07):
  <https://github.com/moby/moby/issues/48112>
- moby PR #52565, "builder-next: pass build auth configs through BuildKit sessions"
  (open, updated 2026-11-02): <https://github.com/moby/moby/pull/52565>
- BuildKit issue #5309, "ERROR: no active sessions" (session is client's responsibility):
  <https://github.com/moby/buildkit/issues/5309>
- BuildKit PR #6744 (merged 2026-05-06) and PR #6760 (merged 2026-06-03):
  <https://github.com/moby/buildkit/pull/6744>, <https://github.com/moby/buildkit/pull/6760>
- BuildKit v0.31.0 release notes (anonymous registry-auth fallback):
  <https://github.com/moby/buildkit/releases/tag/v0.31.0>
- buildx docker driver (dials `/grpc` + `/session`):
  <https://github.com/docker/buildx/blob/master/driver/docker/driver.go>
- buildkit client library (`New`, `WithContextDialer`, `WithSessionDialer`, `Solve`):
  <https://github.com/moby/buildkit/blob/master/client/client.go>
- buildkit `gha` cache exporter and the GitHub env it reads:
  <https://github.com/moby/buildkit/blob/master/cache/remotecache/gha/gha.go>,
  <https://github.com/moby/buildkit/blob/master/cmd/buildctl/build/util.go>
- buildx drivers reference and docker driver docs ("BuildKit server components built directly
  into the Docker Engine"): <https://docs.docker.com/build/builders/drivers/>,
  <https://docs.docker.com/build/builders/drivers/docker/>
- Docker BuildKit docs (default builder; "avoids intermediate images and containers"):
  <https://docs.docker.com/build/buildkit/>
- Docker Engine deprecated features (classic Linux builder deprecated in v23.0, not yet
  removed; fallback/opt-out end soon): <https://docs.docker.com/engine/deprecated/>
- Docker Engine 23.0 release notes (BuildKit/Buildx default; `DOCKER_BUILDKIT=0` opt-out):
  <https://docs.docker.com/engine/release-notes/23.0/>
- Dockerfile reference (heredocs, `RUN --mount`, multi-stage `COPY --from`):
  <https://docs.docker.com/reference/dockerfile/>
- Dockerfile frontend `dockerfile/1.4.0` release (heredocs promoted from labs to stable):
  <https://github.com/moby/buildkit/releases/tag/dockerfile/1.4.0>
- podman compat build handler (no `version` parameter in `BuildQuery`):
  <https://github.com/containers/podman/blob/main/pkg/api/handlers/compat/images_build.go>
- podman issue #17836 (`docker build` with BuildKit hits `/session` 404; `buildx build` differs):
  <https://github.com/containers/podman/issues/17836>
- podman 4.8 release notes (heredoc support in `podman build`):
  <https://github.com/containers/podman/blob/main/RELEASE_NOTES.md>
- colima runtimes ("Docker is the default runtime"; containerd is opt-in):
  <https://colima.run/docs/runtimes/>
- Rancher Desktop container engine settings (containerd vs dockerd/moby):
  <https://docs.rancherdesktop.io/ui/preferences/container-engine/general>
- OrbStack container images ("uses the modern BuildKit backend"; ships buildx):
  <https://docs.orbstack.dev/docker/images>, <https://docs.orbstack.dev/install>
- Lima `docker` template (stock dockerd, rootless) and examples:
  <https://github.com/lima-vm/lima/blob/master/templates/docker.yaml>,
  <https://lima-vm.io/docs/examples/>
- BuildKit "Containerizing BuildKit → Podman" (`podman-container://`):
  <https://github.com/moby/buildkit>
