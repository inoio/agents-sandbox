---
description: Contract for how the per-project runner Dockerfile is composed into per-tool multistage stages so tool-owned layers (apt tools, node, agent, docker) cache independently of the user body, the base, and each other; includes the `dind` → `docker` rename with deprecated aliases.
read_if: Changing RenderDockerfile/stage composition, image layer caching, the dind/docker switch or marker, custom-base handling, tool install paths (/opt/agents-sandbox), or the dockerd vfs storage driver.
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
FROM agents-sandbox-base AS agents-sandbox-node
  [node -> /opt/agents-sandbox, only if the base lacks node]
FROM agents-sandbox-node AS agents-sandbox-agent
  [<agent> -> /opt/agents-sandbox, only if the base lacks it]
FROM agents-sandbox-base AS agents-sandbox-docker      # only when docker is enabled
  [docker -> /opt/agents-sandbox/bin, only if the base lacks it]
FROM agents-sandbox-base AS runner
  [dev user] [ENV PATH+=/opt/agents-sandbox/bin] [user body]
  COPY --from=agents-sandbox-agent /opt/agents-sandbox ...
  COPY --from=agents-sandbox-docker /opt/agents-sandbox/bin ...  # at the marker when present, else after the body
  [adoption] [finalize]
```

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
- `ImageSpec.InstallCommand` is responsible for landing the agent in that prefix: npm agents use
  `npm install -g --prefix /opt/agents-sandbox <pkg>@$VERSION`; opencode v1 copies its installer binary to
  `/opt/agents-sandbox/bin`. The toolchain must `mkdir -p /opt/agents-sandbox/bin` so the `COPY` source always exists.
- An agent is installed into `/opt` only when `command -v <binary>` finds nothing in the base; npm agents install into
  `/opt` using whichever `npm` is on `PATH` (base or tool), so a base-provided node needs no redundant download.

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

## Non-goals

- No separately persisted/tagged toolchain image (the intermediate stages are the cache).
- No BuildKit cache mounts / `docker buildx` migration.
