---
description: Contract for how the per-project runner Dockerfile is composed into per-tool multistage stages so tool-owned layers (apt tools, node, agent, docker) cache independently of the user body, the base, and each other; includes when the node stage is emitted (agent node dependency), the native vs npm agent install methods, and the `dind` → `docker` rename with deprecated aliases.
read_if: Changing RenderDockerfile/stage composition, image layer caching, which agents need node, agent install methods (native installer vs npm), the dind/docker switch or marker, custom-base handling, the node pin/libatomic dependency (node-removal direction), tool install paths (/opt/agents-sandbox), login-shell PATH handling (AGENTS_SANDBOX_IMAGE_PATH, the /etc/profile.d merge script), or the dockerd vfs storage driver.
---

# Runner image composition and tool-layer caching

Tracking issue: [inoio/agents-sandbox#124](https://github.com/inoio/agents-sandbox/issues/124).

## Problem

The runner image is one flat stage chain: `base → dev user → user body → docker finalize → agent → finalize`. Classic-builder
and BuildKit both key a `RUN` on its parent snapshot plus the instruction, so an edit to the user body invalidates the
node, docker and agent installs that follow it — re-downloading the Node.js tarball, the Docker static binaries, and the
npm agent on every project-Dockerfile edit and on every project with a different body. An agent-version bump must not
re-run the user body, and a user-body edit must not re-run the agent; a linear chain cannot satisfy both.

## Contract

Tool-owned installs run in **earlier multistage stages with fixed parents** and are merged into the runner stage with
`COPY`, so the body's parent no longer depends on them.

Stages (deterministic reserved aliases; managed and custom differ only in the base stage):

```
[earlier user stages]                          # custom only; preserved
FROM <baseRoot> AS agents-sandbox-base         # managed: debian:trixie-slim + embedded apt tools
FROM agents-sandbox-base AS agents-sandbox-node           # only when the agent requires node
  ARG NODE_VERSION=<nodeVersion>               # node -> /opt/agents-sandbox, only if the base lacks node
FROM <agents-sandbox-node | agents-sandbox-base> AS agents-sandbox-agent
  ARG <AGENT>_VERSION=<resolvedVersion>        # <agent> -> /opt/agents-sandbox, only if the base lacks it
FROM agents-sandbox-base AS agents-sandbox-docker      # only when docker is enabled
  ARG DOCKER_VERSION=<dockerVersion>           # docker -> /opt/agents-sandbox/bin, only if the base lacks it
FROM agents-sandbox-base AS runner
  [dev user] [ENV PATH+=/opt/agents-sandbox/bin] [user body]
  COPY --from=agents-sandbox-agent /opt/agents-sandbox ...
  COPY --from=agents-sandbox-docker /opt/agents-sandbox/bin ...  # at the marker when present, else after the body
  [adoption] [finalize]
```

### Pinned versions are rendered into the content

Every tool stage states its pinned version as an `ARG` default in the rendered Dockerfile (node, agent, docker), and
each install `RUN` consumes it. The version is therefore part of the Dockerfile content, so a bump invalidates that
stage's install layer from the `ARG` line — independent of whether the builder folds the version build arg into its
cache key. (The agent version is also still passed as a build arg, and the agent ARG default is the resolved release.)
An empty agent version (e.g. a `build dockerfile` preview without `--agent-version`) renders the ARG without a default.

Resulting cache behaviour:

- Edit the user body → body and the cheap `COPY`/adopt layers re-run; no tool is re-downloaded.
- Bump the agent version → the agent layer re-runs; node, docker and the user body stay cached.
- Toggle docker → the docker stage appears/disappears; node, agent and the user body stay cached.
- Second project with the same base and versions → the tool stages are a full cache hit.

## Tool artifact contract

- Every tool is exposed from **`/opt/agents-sandbox/bin`**, which the runner appends to `PATH` (append, so a base-provided
  binary keeps precedence). Node/npm live under `/opt/agents-sandbox`.
- The image records its composed `PATH` in **`AGENTS_SANDBOX_IMAGE_PATH`** and installs
  **`/etc/profile.d/agents-sandbox-path.sh`**. The agent attach and the interactive shell run `/bin/bash -l`, and
  `/etc/profile` (Debian and most distributions) *resets* `PATH`, discarding the composed `PATH` (the appended
  `/opt/agents-sandbox/bin`, a project `ENV PATH` toolchain, base sbin dirs). `/etc/profile` sources `/etc/profile.d/*.sh`
  after the reset, so the script re-appends any missing `AGENTS_SANDBOX_IMAGE_PATH` entry; existing entries keep their
  position and nothing is duplicated. This **supersedes** the earlier `/usr/local/bin` symlink stopgap (removed): the merge
  restores the full composed `PATH` rather than only the tool binaries, with precedence governed by image `PATH` order. See
  [login-shell-path.md](login-shell-path.md).
- `ImageSpec.InstallCommand` is responsible for landing the agent in that prefix: npm agents (pi) use
  `npm install -g --prefix /opt/agents-sandbox <pkg>@$VERSION`; every other agent copies the binary produced by its
  upstream installer to `/opt/agents-sandbox/bin` (opencode v1 and opencode2 copy the native installer's binary;
  claude-code copies its launcher with `cp -L` because the launcher is a symlink into a versions directory). The agent
  stage must `mkdir -p /opt/agents-sandbox/bin` itself so the `COPY` source always exists, independent of the node
  stage.
- An agent is installed into `/opt` only when `command -v <binary>` finds nothing in the base; npm agents install into
  `/opt` using whichever `npm` is on `PATH` (base or tool), so a base-provided node needs no redundant download.

## Node dependency

- The `agents-sandbox-node` stage is emitted only for agents whose `ImageSpec.RequiresNode` is true. Today only `pi`
  sets it: its published package is a `#!/usr/bin/env node` bundle installed through npm, so it needs node at install and
  at runtime. opencode v1, opencode2, and claude-code ship self-contained native binaries and need neither node nor npm.
- When the node stage is emitted, `agents-sandbox-agent` starts from it; otherwise it starts from `agents-sandbox-base`.
- Version pinning and resolution are unaffected: the pinned version is still passed to the upstream installer (npm
  dependency specifier, opencode `--version`, or claude positional version), still travels as a build `ARG` referenced by
  the install `RUN`, and is still folded into the `dockerfile-id`. Agent version discovery stays a host-side HTTP query
  (npm registry / pi.dev); it never runs in the image.
- JS-based LSP servers rely on the agent's embedded JS runtime (Bun) or on node that a project installs in its own
  Dockerfile body; agents-sandbox does not ship node to serve them.

## User-provided detection and provenance

Detection ("leave alone if the base already provides node/agent/dockerd") and the `agent-source`/`docker-source`
provenance files are resolved in the **adoption step after the user body**, not at install time. Because the runner's
`PATH` prefers base binaries, `command -v` still sees a body-installed tool; the tool just avoids the download when the
basis already had it. `user-provided` provenance semantics are unchanged.

## Docker marker

The heavy docker install lives in the `agents-sandbox-docker` stage. A `# agents-sandbox:docker` marker in the final-stage
body injects only a thin `COPY --from=agents-sandbox-docker` plus adopt step, so steps after the marker see `docker`;
without a marker that copy+adopt is appended after the body, matching the previous default. The legacy
`# agents-sandbox:dind` marker is still recognized. The marker retains its documented purpose (place post-engine steps
correctly) while the download itself stays cacheable.

The docker **runtime** prerequisite check (`iptables git ps xz curl tar`, the Docker binary-install requirements) lives in
the adoption block in the final stage, not in the install stage. The install stage starts from the raw base before the
user body, so a base/project body that installs the runtime prerequisites (or an earlier project stage) must still build;
only `curl` and `tar`, needed by the download itself, remain base-image requirements for the tool stages.

## Naming: `docker` (renamed from `dind`)

The capability installs and runs the Docker engine inside the microsandbox VM, which is not literal Docker-in-Docker, so
the canonical token is **`docker`** everywhere: CLI flag `--docker` (on `build`, `run`, `shell`, and `build dockerfile`),
config key `docker`, env `AGENTS_SANDBOX_DOCKER`, and the Go names `BuildOptions.Docker`, `options.Docker`,
`Resolver.Docker()`, `RenderDockerfile(..., docker bool)`, `dockerBlock`, `injectDockerBlock`, `dockerFinalizationBlock`.
The provenance file `docker-source` already uses this token.

Deprecated aliases stay accepted at lower precedence with a one-time warning, mirroring the `OPENCODE_SANDBOX_` →
`AGENTS_SANDBOX_` env-prefix deprecation: `--dind`, the `dind` config key, and `AGENTS_SANDBOX_DIND`. User-authored
inputs with no warning: the `# agents-sandbox:dind` marker and a `FROM agents-sandbox/runner-base-dind` (both still
imply docker); `agents-sandbox/runner-base-docker` is also recognized as the new-name variant.

## vfs storage driver

`vfs` is forced by adding `--storage-driver=vfs` to the dockerd start command in `vm/dockerd.go`, **not** by writing
`/etc/docker/daemon.json` in the image. A CLI flag overrides the config file, so a user's edited `daemon.json` is left
intact. The old finalize-time `daemon.json` overwrite is removed.

## Invariants

- Stage aliases are deterministic and reserved (`agents-sandbox-base|node|agent|docker`); a user Dockerfile declaring one
  is a hard error. They must not contain a random uuid/hash — the `dockerfile-id` is a content hash of the rendered
  Dockerfile, and nondeterminism would defeat the skip-build check in `specs/runner-image-build.md`.
- `baseImageRef`/`resolveBaseDigest` resolve the `agents-sandbox-base` stage's underlying image (the embedded
  `debian:trixie-slim` for managed, the user's image for custom), never the `runner` stage alias.
- `BuildOptions.Force`, `NoCache`, and the `dockerfile-id` skip-build contract are unchanged.
- The body still runs without node/agent/docker available (they are `COPY`ed after it), preserving prior ordering.

## Node dependency

The node stage exists to install/run npm-based agents; opencode v1 is a native Bun binary that does not need it. Facts
that constrain the stage:

- The pin is the **v24 LTS line (`v24.21.0`)**, not v26, because the v26 official tarballs gained a hard
  `DT_NEEDED` on `libatomic.so.1`; the managed base (and most custom bases) does not provide it, so on the managed base
  the npm agent's install `RUN` (which invokes `node` via npm's shebang) failed the build, and a native-agent image left a
  `node`/`npx` that could not start ([#132](https://github.com/inoio/agents-sandbox/issues/132)). v24 has no libatomic
  dependency on x86_64 or arm64. Every npm agent satisfies it (`pi` `>=22.19.0`, `claude-code` `>=22.0.0`; `opencode2`
  unconstrained) — the earlier `v22.14.0` pin failed only because it was below pi's `>=22.19.0`.
- Regression guard: `TestRenderDockerfileNpmAgentNodeRuns` (integration) builds an npm agent on the managed base and runs
  `node --version`/`npm --version` in the image; `TestRenderDockerfileBuilds` builds only the native `opencode`, so it
  cannot catch a broken node. A future pin that reintroduces libatomic must add `libatomic1` to the embedded base (a
  documented custom-base requirement) or the test fails.
- Direction: don't depend on node where possible, for image size/build speed
  ([#133](https://github.com/inoio/agents-sandbox/issues/133)). opencode v1, opencode2, and claude-code each ship a native
  installer (`curl | bash`) needing no node at install or runtime; **pi still needs node** (its installer calls
  `npm install`, its node-bootstrap path is interactive, and its package is a `#!/usr/bin/env node` bundle). Native
  binaries embed Bun, not node, so JS-based LSP servers launched via `node`/`npx` need another route.

## Integration-test cache

The `integration` docker-build tests rely on the **classic builder's** layer cache (they use `client.ImageBuild`, whose
cache is separate from BuildKit's) and must not prune it. `ImageRemove` with `PruneChildren: true` selects Docker's
"prune" default, which also deletes the untagged parent images that *are* the cache. Cleanup therefore removes the test
tag with `PruneChildren: false` (the `removeTestImage` helper). With pruning, every subtest — and every
`make test-integration` rerun — re-ran the node/agent/docker installs (the uncached managed composition costs ~13s);
without it, later builds reuse those layers (~0.2s).

`TestRealDockerClientImagePrune` must prune with a **no-match filter**, not empty options. `go test ./...` runs the
`docker` and `image` integration binaries **in parallel** against one daemon, and an empty-option `ImagePrune` deletes
the dangling cache images the image tests are actively reusing, causing `No such image`/missing-layer failures. An
empty-option prune is also destructive to a developer's unrelated dangling images and build cache, so it is wrong
independently of the tests.

## Non-goals

- No separately persisted/tagged toolchain image (the intermediate stages are the cache).
- No BuildKit cache mounts / `docker buildx` migration.
