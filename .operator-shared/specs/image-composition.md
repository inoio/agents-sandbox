---
description: Contract for how the per-project runner Dockerfile is composed into per-tool multistage stages so tool-owned layers (apt tools, node, agent, docker) cache independently of the user body, the base, and each other; includes when the node stage is emitted (agent node dependency), the native vs npm agent install methods, and the `dind` → `docker` rename with deprecated aliases.
read_if: Changing RenderDockerfile/stage composition, image layer caching, which agents need node, agent install methods (native installer vs npm), the `ImageSpec.OwnedToolPaths` ownership handover or `agentOwnershipBlock`, the dind/docker switch or marker, the Dockerfile-content docker enablement, its explicit-config precedence and the shared docker/dind source precedence (`viperconfig.EffectiveDockerSource`), the unified dockerfile diagnostics, custom-base handling, the node pin/libatomic dependency (node-removal direction), tool install paths (/opt/agents-sandbox), login-shell PATH handling (AGENTS_SANDBOX_IMAGE_PATH, the /etc/profile.d merge script), or the dockerd vfs storage driver.
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
FROM debian:trixie-slim AS agents-sandbox-toolbase     # tool-owned; + curl ca-certificates tar
FROM agents-sandbox-toolbase AS agents-sandbox-node    # only when the agent requires node
  ARG NODE_VERSION=<nodeVersion>               # node -> /opt/agents-sandbox
FROM <agents-sandbox-node | agents-sandbox-toolbase> AS agents-sandbox-agent
  ARG <AGENT>_VERSION=<resolvedVersion>        # <agent> -> /opt/agents-sandbox
FROM agents-sandbox-toolbase AS agents-sandbox-docker  # only when docker is enabled
  ARG DOCKER_VERSION=<dockerVersion>           # docker -> /opt/agents-sandbox/bin
FROM <baseRoot> AS agents-sandbox-base         # managed: debian:trixie-slim + embedded apt tools; custom: user image
FROM agents-sandbox-base AS runner
  [dev user] [ENV PATH+=/opt/agents-sandbox/bin] [user body]
  COPY --from=agents-sandbox-agent /opt/agents-sandbox ...
  COPY --from=agents-sandbox-docker /opt/agents-sandbox/bin ...  # at the marker when present, else after the body
  [adoption] [finalize]
```

The tool stages build on the tool-owned `agents-sandbox-toolbase` (a pinned `debian:trixie-slim` plus `curl`,
`ca-certificates`, and `tar`), not on the user's base, so a custom base needs neither `curl` nor `tar` inside the image
and the tool layers cache across bases. The tool base is never part of the runner's lineage; the runner still builds on
`agents-sandbox-base` (the managed block or the custom base).

**Tool-tree ownership handover.** A `COPY --from=agents-sandbox-agent /opt/agents-sandbox` preserves the source stage's
root ownership. An agent whose install self-writes at runtime (Hermes keeps its dependency ledger and bytecode markers in
its checkout) declares the affected paths in `ImageSpec.OwnedToolPaths`; the runner emits `agentOwnershipBlock` — a
`USER root` + `RUN chown -R dev:$(id -g dev) <paths>` — after the `COPY` and before `finalizationBlock` switches to
`USER dev`. It resolves the group from the `dev` user's own entry rather than assuming a `dev` group or the
`USER_UID`/`USER_GID` build args: `devUserBlock` skips creation entirely when a custom base already provides `dev`
(which need not come with a `dev` group, so `dev:dev` would fail the build), and `groupadd -f` can leave the `dev` group
on a GID other than a pinned host GID. Agents that declare no paths emit nothing.

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
- Managed base → the tool base and the managed base block share one `debian:trixie-slim` layer, so it is pulled once.

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
- The build context is the rendered Dockerfile plus a set of **build-context files** (`buildContextFiles()`), each
  installed by an invariant `COPY` in the final stage. The list is the single source of truth for both the context tar
  and `computeDockerfileID`, which hashes every file's name, mode, and content alongside the rendered Dockerfile and the
  agent version — so adding a file both ships it and invalidates an existing image. The files are
  `/etc/profile.d/agents-sandbox-path.sh` (mode 644), **`/usr/local/bin/msb-trust`** (mode 755: the executable bit
  travels in the context entry rather than a `chmod` `RUN`), and its license at
  **`/usr/share/licenses/msb-trust/LICENSE`** (mode 644), which Apache-2.0 section 4(a) requires to travel with the
  vendored helper. See [ca-trust-helper.md](ca-trust-helper.md).
- `ImageSpec.InstallCommand` is responsible for landing the agent in that prefix: npm agents (pi) use
  `npm install -g --prefix /opt/agents-sandbox <pkg>@$VERSION`; every other agent copies the binary produced by its
  upstream installer to `/opt/agents-sandbox/bin` (opencode v1 and opencode2 copy the native installer's binary;
  claude-code copies its launcher with `cp -L` because the launcher is a symlink into a versions directory). The agent
  stage must `mkdir -p /opt/agents-sandbox/bin` itself so the `COPY` source always exists, independent of the node
  stage.
- Every tool is installed into `/opt` unconditionally: the tool stages build on the tool base, which carries no tool, so
  there is nothing to probe at install time. Whether the base already provides a tool is decided after the user body by the
  adoption step's provenance files, and the runner's `PATH` (base entries first) keeps a base-provided tool winning. npm
  agents install into `/opt` using the node the node stage provides.

## Node dependency

The node stage exists to install/run npm-based agents.

- The `agents-sandbox-node` stage is emitted only for agents whose `ImageSpec.RequiresNode` is true. Today only `pi`
  sets it: its published package is a `#!/usr/bin/env node` bundle installed through npm, so it needs node at install and
  at runtime. opencode v1, opencode2, and claude-code ship self-contained native binaries and need neither node nor npm;
  opencode v1 is a native Bun binary.
- When the node stage is emitted, `agents-sandbox-agent` starts from it; otherwise it starts from
  `agents-sandbox-toolbase`.
- Version pinning and resolution are unaffected: the pinned version is still passed to the upstream installer (npm
  dependency specifier, opencode `--version`, or claude positional version), still travels as a build `ARG` referenced by
  the install `RUN`, and is still folded into the `dockerfile-id`. Agent version discovery stays a host-side HTTP query
  (npm registry / pi.dev); it never runs in the image.
- Keep the pin **below v25**; it sits on the **v24 LTS line (`v24.22.1`)**. Node v25 and v26 official tarballs carry a
  hard `DT_NEEDED` on `libatomic.so.1`, which the managed base (and most custom bases) does not provide, so on the managed
  base the npm agent's install `RUN` (which invokes `node` via npm's shebang) failed the build, and a native-agent image
  left a `node`/`npx` that could not start ([#132](https://github.com/inoio/agents-sandbox/issues/132)). v24 has no
  libatomic dependency on x86_64 or arm64. There is **no solution yet for custom bases** that need node >= 25. Every npm
  agent satisfies v24 (`pi` `>=22.19.0`, `claude-code` `>=22.0.0`; `opencode2` unconstrained) — the earlier `v22.14.0`
  pin failed only because it was below pi's `>=22.19.0`.
- Regression guard: `TestRenderDockerfileNodeAgentNodeRuns` (integration) builds an npm agent on the managed base and runs
  `node --version`/`npm --version` in the image; `TestRenderDockerfileBuilds` builds only the native `opencode`, so it
  cannot catch a broken node. A future pin that reintroduces libatomic must add `libatomic1` to the embedded base (a
  documented custom-base requirement) or the test fails.
- Direction: don't depend on node where possible, for image size/build speed
  ([#133](https://github.com/inoio/agents-sandbox/issues/133)). opencode v1, opencode2, and claude-code each ship a native
  installer (`curl | bash`) needing no node at install or runtime; **pi still needs node** (its installer calls
  `npm install`, its node-bootstrap path is interactive, and its package is a `#!/usr/bin/env node` bundle). Native
  binaries embed Bun, not node, so JS-based LSP servers launched via `node`/`npx` need another route; agents-sandbox does
  not ship node to serve them.

## User-provided detection and provenance

Detection ("leave alone if the base already provides node/agent/dockerd") and the
`agent-source`/`docker-source` provenance files are resolved in the **adoption step after the user body**. The install
stages do not probe the base: they build on the tool base, which cannot see it, so they always install into `/opt`. Because
the runner's `PATH` prefers base binaries, `command -v` in the adoption step still sees a base-provided tool, and the
tool's copy is left unused. `user-provided` provenance semantics are unchanged. This relies on `PATH` order, so a
base-provided tool must live **outside** `/opt/agents-sandbox` (the tool-owned prefix): the runner's tool `COPY` merges
into that tree and overwrites any same-named file a base placed inside it, which `PATH` precedence cannot protect.

## Docker marker

The heavy docker install lives in the `agents-sandbox-docker` stage. The final-stage `# agents-sandbox:docker` marker
(and the legacy `# agents-sandbox:dind`) inject a thin `COPY --from=agents-sandbox-docker` plus adopt step at the marker,
so steps after it see `docker`; without a marker that copy+adopt is appended after the body, matching the previous
default. The download itself stays cacheable.

The marker is also an **enable** signal, not only a position. `effectiveDocker(projectDockerfile, DockerSetting)` is the
single decision site: the capability is on when the final-stage body carries either marker **or** the final stage's base
resolves to the managed `runner-base-docker`/`runner-base-dind` alias (`referencesImage`). Both content signals work for
a custom base and a managed base alike. Only the final-stage body is scanned, so a marker in an earlier stage neither
enables nor positions. Markers are read only from top-level lines (`topLevelLineIndices`): a marker inside a heredoc body
— e.g. a `COPY <<EOF` copying a sample Dockerfile — is inert for enablement, positioning, and diagnostics, so heredoc
text is never rewritten by the injection.

**Precedence.** Explicit launcher config wins in both directions:
`explicit config/flag/env (either value) > Dockerfile content > off`. "Explicit" is the canonical `docker` source (config
key present, `--docker` changed, `AGENTS_SANDBOX_DOCKER` or the legacy `OPENCODE_SANDBOX_DOCKER` non-empty) **or** any
deprecated `dind` source present (`dind`
config key, `--dind` changed, `AGENTS_SANDBOX_DIND` non-empty), including a present-but-`false` value, so a project
configured `dind: false` suppresses a content signal rather than having it re-enabled by a marker. This is a deliberate
inversion of the earlier OR: a `runner-base-docker` base no longer beats an explicit `docker: false`.

Among the explicit sources the ranking is one rule, implemented once: `viperconfig.EffectiveDockerSource` over the
`DockerSource` enum ranks the canonical docker flag > env > config above the deprecated dind flag > env > config. Every
canonical source therefore outranks every deprecated alias **regardless of layer**, which is the deprecation policy's
"replacement wins" applied across layers too: a `docker: false` config beats a true `--dind`, and
`AGENTS_SANDBOX_DOCKER=true` beats a false `--dind`. The config/env/`--docker` layers are folded in `NewResolver` (which
owns the viper instance) and the changed `--docker`/`--dind` flags are folded over that resolved config by
`runoptions.ResolveDockerSetting`, which both the run/shell path (`runoptions.Build`) and the build command
(`resolveBuildDockerSetting`) call — so the build path cannot drift from the run path.

**Case.** Enablement is case-sensitive (`dockerMarkerKind` matches the exact token); near-miss detection is deliberately
case-insensitive (`nearMissDockerMarker`) so a case variant is still surfaced and told the exact spelling. Typos inside
the token (`dockr`) are not caught.

**Diagnostics.** One pure classifier and one emitter, both in `internal/sandbox/image`
(`DiagnoseDockerfile`/`DockerfileDiagnostics` and `EmitDockerfileDiagnostics`), serve every emission site:
`EnsureImageWithClient` (every ensure, cached reuse included), the dry-run preview, and `build dockerfile` (through
`EmitProjectDockerfileDiagnostics`, which reads the file before diagnosing). Messages are the capability line
(`docker: on (project Dockerfile marker)` / `docker: on (project Dockerfile base)`) or the suppression notice
(`docker: off (config); project Dockerfile asks for docker`); the legacy base and marker deprecation warnings; exactly
one ignored-marker message per earlier-stage marker, which replaces that line's deprecation warning; and one near-miss
message per offending line. `build dockerfile` writes every message to stderr, keeping stdout a pasteable Dockerfile.
`RenderDockerfile` stays pure.

The docker **runtime** prerequisite check (`iptables git ps xz`) lives in the adoption block in the final stage, not in
the install stage. The install stage runs on the tool base, which carries `curl` and `tar`, so the download imposes no
requirement on the user's base; the runtime prerequisites may be installed in the body or an earlier project stage.

The agent's **guest runtime tools** are declared per agent (`agent.GuestToolRequirer`, implemented by the daemon profiles)
and rendered into the same final stage after the user body. For `opencode`/`opencode2` they are `curl` only (health probe,
session and worktree calls, notifications) — the runtime-owned detachment job owns process start/stop, so no
`nohup`/`pkill` is required; `pi` and `claude-code` run no daemon
and declare none. The check fails the build naming the tool, so a custom base missing one fails at build time rather than
at the daemon health probe. This is the honest-contract counterpart to #143: the *build's* downloads need no `curl`/`tar`
from the base (they run on the tool base), but a daemon agent's *runtime* still does. Removing the `curl` requirement
altogether — moving the daemon HTTP/SSE calls host-side — is #263.

On top of the agent's tools, the launcher declares its own in-VM tools (`launcherGuestTools`), rendered as a second check
in the same final stage. Today that is `curl`: the host opener replays a browser-login callback inside the VM with it
(#273). Because the opener is on by default, this check runs for every agent, including the daemonless ones, so a custom
base without `curl` fails the image build naming the tool instead of failing at login time.

## Naming: `docker` (renamed from `dind`)

The capability installs and runs the Docker engine inside the microsandbox VM, which is not literal Docker-in-Docker, so
the canonical token is **`docker`** everywhere: CLI flag `--docker` (on `build`, `run`, `shell`, and `build dockerfile`),
config key `docker`, env `AGENTS_SANDBOX_DOCKER`, and the Go names `BuildOptions.Docker`, `options.Docker`,
`Config.Docker`/`Config.DockerSource()` (the resolver exposes them through `Resolver.Config()`, since a Resolver
wrapper for the same value would be reachable only from tests),
`RenderDockerfile(a, agentVersion, projectDockerfile []byte, docker options.DockerSetting)`,
`dockerBlock`, `injectDockerBlock`, `dockerFinalizationBlock`.
The provenance file `docker-source` already uses this token.

Deprecated aliases stay accepted at lower precedence with a one-time warning, mirroring the `OPENCODE_SANDBOX_` →
`AGENTS_SANDBOX_` env-prefix deprecation: `--dind`, the `dind` config key, and `AGENTS_SANDBOX_DIND`. User-authored
inputs that keep working: the `# agents-sandbox:dind` marker and a `FROM agents-sandbox/runner-base-dind` both still
enable the engine, and each warns on every ensure; `agents-sandbox/runner-base-docker` is the new-name variant.

## vfs storage driver

`vfs` is required because the microsandbox filesystem does not support overlayfs. The launcher forces it from
`vm/dockerd.go` at VM start and never writes `/etc/docker/daemon.json`, so a user's edits to it are preserved. Before
choosing a start command it reads the image's `/etc/docker/daemon.json` through the msb FS API (a read error is fatal;
absent or unparseable JSON counts as no entry, and a malformed file is left for dockerd to reject):

- no `storage-driver` entry → start with `--storage-driver=vfs`.
- `storage-driver` already `vfs` → start **without** the flag, because dockerd rejects a directive set both as a flag
  and in the configuration file even when the values agree (#287). This is the case that broke images carrying the
  `daemon.json` the pre-0.6 runner wrote.
- any other `storage-driver` → hard error naming the value, rather than silently overriding the user's file or letting
  the conflict surface as the 30s readiness timeout.

The old build-time `daemon.json` overwrite is removed; the build-time invariant still holds
(`TestRenderDockerfileDockerStage` asserts no `daemon.json` is written).

## Invariants

- Stage aliases are deterministic and reserved (`agents-sandbox-base|toolbase|node|agent|docker|runner`); a user Dockerfile
  declaring one is a hard error, and the `node` alias stays reserved even when no node stage is emitted. They must not
  contain a random uuid/hash — the `dockerfile-id` is a content hash of the rendered Dockerfile, and nondeterminism would
  defeat the skip-build check in `specs/runner-image-build.md`.
- `baseImageRef`/`resolveBaseDigest` resolve the `agents-sandbox-base` stage's underlying image (the embedded
  `debian:trixie-slim` for managed, the user's image for custom), never the `runner` stage alias or the tool base.
- `BuildOptions.Force`, `NoCache`, and the `dockerfile-id` skip-build contract are unchanged.
- The body still runs without node/agent/docker available (they are `COPY`ed after it), preserving prior ordering.
- The project Dockerfile is scanned for stage headers by a dependency-free walker aligned with BuildKit's
  `frontend/dockerfile/parser`: instruction keywords match case-insensitively (`FROMAGE` is not `FROM`), a line's
  trailing whitespace is trimmed (CRLF is handled), and blank lines, comments, continuation lines (honoring a
  leading `# escape=` directive), and heredoc bodies are skipped. Only `RUN`/`COPY`/`ADD`/`ONBUILD` may declare a
  heredoc; the operator is recognised per whitespace/quoted token with BuildKit's `^(\d*)<<(-?)\s*([^<]*)$`, so
  `$((1<<2))`, a quoted operator (`"<<EOF"`), and a glued operator (`cat<<EOF`) are not heredocs. Words are split the
  way BuildKit's heredoc scan does, with quotes and escapes kept raw and a backslash as the escape token regardless of a
  `# escape=` directive: inside a double-quoted run a backslash escapes only `"`, `$`, or `\`, so an escaped quote does
  not close the run and cannot expose a phantom delimiter (`"a \" <<EOF"` declares nothing), while outside quotes an
  escaped quote stays literal. An unterminated quote yields no heredocs, mirroring BuildKit's lexer error that its
  heredoc caller ignores. Continued lines are joined with the continuation escape dropped, and a delimiter word is
  unquoted the way BuildKit's shell lexer does — a quote inside a differently quoted run stays literal (`<<'E"OF'`
  names `E"OF`) — so neither a delimiter glued to a continuation escape nor such inner quoting hides a later stage.

## Integration-test cache

The `integration` docker-build tests rely on the **classic builder's** layer cache (they use `client.ImageBuild`, whose
cache is separate from BuildKit's) and must not prune it. `ImageRemove` with `PruneChildren: true` selects Docker's
"prune" default, which also deletes the untagged parent images that *are* the cache. Cleanup therefore removes the test
tag with `PruneChildren: false` (the `removeTestImage` helper). With pruning, every subtest — and every
`make test-integration` rerun — re-ran the node/agent/docker installs (the uncached managed composition costs ~13s);
without it, later builds reuse those layers (~0.2s).

A docker-build integration test must not assert that a layer **missed** the cache. The classic builder's untagged
intermediate layers persist across runs and even survive `ImageRemove`, so a "the install must re-run" assertion passes
only on a cold cache and fails on every rerun once the layer is primed (this made
`TestRenderDockerfileAgentVersionRebuildsInstall` fail from the second run onward). Assert the built **outcome** instead —
e.g. the agent version the built image reports via its `VersionProvider` — which is stable across runs and still catches
a render that fails to fold the version into the agent stage (it would reuse the previous release's layer and report the
old version).

`TestRealDockerClientImagePrune` must prune with a **no-match filter**, not empty options. `go test ./...` runs the
`docker` and `image` integration binaries **in parallel** against one daemon, and an empty-option `ImagePrune` deletes
the dangling cache images the image tests are actively reusing, causing `No such image`/missing-layer failures. An
empty-option prune is also destructive to a developer's unrelated dangling images and build cache, so it is wrong
independently of the tests.

## Non-goals

- No separately persisted/tagged toolchain image (the intermediate stages are the cache).
- No BuildKit cache mounts / `docker buildx` migration.
