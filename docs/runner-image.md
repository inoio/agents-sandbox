---
title: Runner Image
description: "The Docker image agents-sandbox builds for each sandbox, and how to extend it with your project's tooling."
layout: default
parent: Guides
nav_order: 20
---

# Runner Image

agents-sandbox builds a Docker image for each sandbox. The image contains the selected coding agent, Node.js, and common
CLI tools.
Projects can extend the image with their own tooling.

The image is built to rebuild fast and stay lean. Its tools — the base CLI tools, Node, the selected agent, and the
optional Docker engine — live in separate build stages whose layers are reused unchanged, so editing your project's
Dockerfile body does not re-run the tool installs, and the same tool layers are shared across every project on the machine. Node
is present only for the agent that needs it, and the image build's downloads need no `curl` or `tar` from your base — they
run on the tool base.

## Building and managing images

Build the image:

```console
agents-sandbox build        # builds if image is missing or base changed
agents-sandbox build -r     # force rebuild
```

Preview the exact Dockerfile that would be built (without invoking docker):

```console
agents-sandbox build dockerfile                        # default agent, no docker
agents-sandbox build dockerfile --docker               # with the Docker engine block
agents-sandbox build dockerfile --agent-version 1.2.3  # pin the agent version in the preview
```

List cached images:

```console
agents-sandbox image list
```

The image name includes the checkout slug and Docker image hash, so changes to the Dockerfile automatically trigger a
rebuild. When the resulting image digest differs from the image the existing project VM was booted from, the VM is
recreated on the next run so it uses the new image (the home volume is preserved).

Runner images are tagged **per agent**. The `-latest` tag is namespaced by the agent, so each agent's image can be
identified and updated independently. The Docker tag keeps the `agents-sandbox/runner-` prefix so it stays
distinguishable in the system Docker daemon, while the microsandbox image reference is prefix-free and keyed by the
image content:

```text
Docker tag:               agents-sandbox/runner-<slug>:<agent>-latest
microsandbox name ref:    <slug>:<agent>-latest
microsandbox content ref: <slug>:<agent>.<contentid>
```

The content reference is what keeps a project's variants apart. Its `<contentid>` is the first 16 characters of the
image's content ID, so each distinct image content is imported into microsandbox once, under its own reference. A
project's variants — with and without the Docker engine, a different agent version, an edited project Dockerfile —
therefore coexist in the cache, and switching between them reimports nothing after the first load. The
`<slug>:<agent>-latest` name reference is the older prefix-free form (a 0.6.0 cache); it is still read, never produced.

For example, with a checkout slug of `my-project`, the opencode image is tagged
`agents-sandbox/runner-my-project:opencode-latest` and referenced in microsandbox as
`my-project:opencode.9f2c1a7b4d6e0f31`; the pi image is tagged `agents-sandbox/runner-my-project:pi-latest` and
referenced as `my-project:pi.4b8d0e6f2a1c3b57`.

An image loaded by an earlier version sits under the old prefixed microsandbox reference
(`agents-sandbox/runner-<slug>:<agent>-latest`). agents-sandbox reuses that reference whenever it still holds the
current content, so an upgrade never reimports an unchanged image; changed content is imported under a new content
reference, silently migrating the project.

### Skip-build when unchanged

The Docker build is skipped when the baked `org.agents-sandbox.dockerfile-id` label matches the current content. This
label is a hash of the rendered Dockerfile, the build-context files it installs (the `/etc/profile.d` `PATH` merge
script and the CA-trust helper), and the agent version, so an image already built from the exact same inputs is reused
instead of being rebuilt.

Each tool stage pins its version as an `ARG` default in the rendered Dockerfile (`NODE_VERSION`, the agent's
`<AGENT>_VERSION`, `DOCKER_VERSION`). The version is therefore part of the build content, so bumping it changes the
build identity and invalidates that stage's install layer — the agent layer re-runs on an agent upgrade while the node
and Docker layers stay cached — regardless of how the builder tracks version build arguments.

With `--log-level verbose`, the launcher reports why a build happens on every rebuild path. Before building, each image
also records its individual inputs as `org.agents-sandbox.identity.*` labels (project Dockerfile hash, agent version, and
Docker-in-Docker), so the verbose line names the changed input — for example `rebuilding runner image
…: project Dockerfile changed; agent version changed` — or `forced by --rebuild` / `runner image not present locally`.
A reuse logs `reusing runner image …: content identity unchanged`; an image without these identity labels (built by an
older version) is reported as a generic content change.

### Content-verified load

Before skipping a load, `EnsureLoaded` verifies the microsandbox cache content by comparing its config digest against the
Docker image ID. If the cached content no longer matches, the image is reloaded so the VM always boots from the correct
image. A cached image that records no digest is treated as unknown content and reloaded rather than matched by the
`dockerfile-id` label, which is a source signature that can match while the bytes differ; the reload happens once,
because a load records a digest. At verbose level the load states whether the image was absent from the cache or its
cached content was stale.

The load tries three candidate references and reuses the first that already holds the current content: the new content
reference, the prefix-free name reference (`<slug>:<agent>-latest`, a 0.6.0 cache), and the legacy prefixed reference
(`agents-sandbox/runner-<slug>:<agent>-latest`, a pre-0.6 cache). When none matches, the Docker image is loaded under
its content reference. Every following VM creation, label read and log line uses the resolved reference.

### Pruning images

Images can be pruned via the `prune` command or, more targeted, the `image prune` subcommand:

```console
agents-sandbox image prune --dry-run   # see what's stale
agents-sandbox image prune             # actually remove them (prompts; or pass --yes)
```

See [Pruning]({% link sandboxes.md %}#pruning) for the confirmation prompt, `--yes`, and `--dry-run` behavior.

Pruning retains any image a kept VM still references, the image the current `run`/`shell` is about to use, and any
project's runner-image variants that were used within the prune age. It reclaims the rest — a pruned key's refs,
aged-out variants, pre-redesign digest refs no sandbox uses, and orphaned surplus images. It also runs microsandbox's
native image-data prune so manifests and layers orphaned by a runner-image refresh are reclaimed after no VM references
them; because that native prune reclaims any image no sandbox references, a `run`/`shell`'s startup auto-prune defers it
to keep the image it is about to load, and only an explicit `image prune` runs it. See
[Commands]({% link commands.md %}) for details on the prune command.

agents-sandbox also auto-prunes all resources that are ephemeral, unused or haven't been in use for more than 30 days by
default. Cached runner images and home volumes are only pruned once they are older than the threshold, so a recently
used project keeps its image and home state across restarts. See [Sandboxes]({% link sandboxes.md %}) for more
information
and [Configuration]({% link configuration/index.md %}) for how to configure auto-pruning.

## Home directory

When rebuilding an image, all state in the VM's home directory (`/home/dev/`) would get lost. Therefore, the home
directory is copied to a volume and mounted to the running VM, so it survives a rebuild.

When an image change actually leads to a rebuild, agents-sandbox prompts you to keep, migrate, or reset that volume;
see [Home volumes]({% link sandboxes.md %}#home-volumes) for the choices and the non-interactive default. `volume
migrate`, `volume reset`, and `volume edit` remain available for manual management.

When a rebuilt image changes content, its new content reference does not collide with the previous one, so
agents-sandbox refreshes the microsandbox cache by loading the new Docker image under the new reference. It does not
remove the old cached manifest first, because a stopped project VM can still reference it. Unused image data is
reclaimed later by image pruning.

## One image per checkout

agents-sandbox builds a single runner image per checkout. The rendered Dockerfile is composed of per-tool multistage
stages with fixed parents, merged into the final stage with `COPY` **after** the user body:

- **`agents-sandbox-base`** — the embedded `debian:trixie-slim` tools block for a managed base, or your whole custom base
  (`FROM` image) for a custom base. For a managed base (`FROM .../runner-base...`), the final stage's `FROM` is replaced
  **in place** with the embedded tools block; for a custom base, any earlier build stages in your project Dockerfile are
  preserved before it — so multi-stage project Dockerfiles are supported.
- **`agents-sandbox-toolbase`** — the internal stage the node, agent, and docker stages build on: a pinned
  `debian:trixie-slim` plus `curl`, `ca-certificates`, and `tar`. It is **not** part of the runner's lineage, so your base
  needs none of those binaries and the tool layers cache across bases.
- **`agents-sandbox-node`** *(only for agents that need node)* — installs Node.js into `/opt/agents-sandbox`. It is emitted
  only when the selected agent requires node (today only `pi`, an npm-installed node bundle); `opencode`, `opencode2`,
  `claude-code`, `codex`, and `hermes` install native binaries or provision their own runtime, and their images carry no
  tool-installed node.
- **`agents-sandbox-agent`** *(conditional)* — installs the selected agent into `/opt/agents-sandbox`. It starts from the
  node stage when one was emitted, otherwise from the tool base, and creates `/opt/agents-sandbox/bin` itself. npm agents
  (`pi`) install with `npm install -g --prefix /opt/agents-sandbox <pkg>@$VERSION`; the rest copy the binary produced by
  their upstream installer into `/opt/agents-sandbox/bin`.
- **`agents-sandbox-docker`** *(optional)* — installs the Docker static binaries into `/opt/agents-sandbox/bin`.
- **`agents-sandbox-runner`** — `FROM agents-sandbox-base`, creates the `dev` user, runs your Dockerfile body **without**
  the tool trees available, then `COPY`s the tool trees in.

Each tool layer caches independently of the user body, the base, and each other. Editing the user body no longer re-runs
the Node/Docker/agent installs, the same tool layers are reused across projects on one machine, and a base that already
provides a tool is left to that base (the runner's `PATH` prefers base binaries).

Every tool lands under `/opt/agents-sandbox`; its binaries are exposed from `/opt/agents-sandbox/bin`, which the runner
**appends** to `PATH` (append, so a base-provided binary keeps precedence). The agent attach and the interactive shell run
`/bin/bash -l`, and `/etc/profile` on Debian and most distributions **resets** `PATH`, so the appended
`/opt/agents-sandbox/bin` (and any project `ENV PATH` entry) would not survive a login shell. To keep the composed `PATH`
effective there, the image records its composed `PATH` in the `AGENTS_SANDBOX_IMAGE_PATH` environment variable and installs
`/etc/profile.d/agents-sandbox-path.sh`. `/etc/profile` sources `/etc/profile.d/*.sh` after its `PATH` reset, so the script
re-appends any `AGENTS_SANDBOX_IMAGE_PATH` entry that is missing from the reset `PATH` (existing entries keep their
position; nothing is duplicated). `mkdir -p /opt/agents-sandbox/bin` always runs so the `COPY` source exists. The image
always ends with `USER dev` and `WORKDIR /workspace`.

The runner also installs microsandbox's application certificate trust helper as `/usr/local/bin/msb-trust`. It is for
applications whose TLS trust is separate from the guest's system store — a JDK's `cacerts`, Chrome/Chromium's NSS
store — which matters as soon as a secret turns on TLS interception. The helper is Apache-2.0 work, so its license is
installed at `/usr/share/licenses/msb-trust/LICENSE`. See
[Secrets]({% link configuration/secrets.md %}#trusting-the-ca-in-applications) for which applications already work, and
the [Java recipe]({% link recipes/java-ca-trust.md %}) for a worked example.

The runner installs an `xdg-open` replacement at `/usr/local/bin/xdg-open`, links `x-www-browser` and
`sensible-browser` to it, and sets `BROWSER=xdg-open`. It hands links to agents-sandbox on the host instead of opening
them in the sandbox. `/usr/local/bin` comes before `/usr/bin` on `PATH`, so it also replaces an `xdg-open` your base
image installs. See [Opening links]({% link configuration/opening-links.md %}).

## The base image contract

By default the base tools block starts from `debian:trixie-slim` and installs the recommended CLI tools: `git`,
`ripgrep`, `jq`, `yq`, `curl`, `wget`, `xz-utils`, `file`, `gawk`, `less`, `lz4`, `moreutils`, `net-tools`, `parallel`,
`recode`, `uuid`, and `iptables`.

A project Dockerfile whose `FROM` is any other image is treated as a **custom base**: your final `FROM` becomes the base
stage the runner builds on, while the node, agent, and optional docker stages build on the tool base, independent of your
image. For a custom base, the earlier build stages in your project Dockerfile are kept before the base stage. The custom
base must meet these requirements:

- **shadow-utils** providing `groupadd`, `useradd`, and `usermod` (used to create the `dev` user and docker group).
- **A POSIX shell** — the `dev` user's login shell is set to the first of `bash`, `zsh`, `sh` found, falling back to
  `/bin/sh`.
- **A login profile that sources `/etc/profile.d/*.sh`** — the runner installs `/etc/profile.d/agents-sandbox-path.sh`
  to restore the composed `PATH` in login shells, so a base whose profile does not source `/etc/profile.d` will not get
  the merged `PATH`.
- **For Docker only**: the runtime prerequisites `iptables`, `git`, `ps`, and `xz`. These are checked after your
  Dockerfile body runs, so they may be installed in the body or an earlier stage. If one is missing, the build fails and
  names the missing package. The engine download itself runs on the tool base, so your image needs no `curl` or `tar`.
- **For the daemon agents (`opencode`, `opencode2`)**: the in-VM tool the launcher uses — `curl`. It reaches the daemon
  (the health probe, session and worktree calls, and notifications); the daemon is launched and stopped as a
  runtime-owned job, so no process-control tool is needed. `curl` is checked after your Dockerfile body runs, so it may
  be installed in the body or an earlier stage; if it is missing, the build fails and names the tool. `pi`,
  `claude-code`, `codex`, and `hermes` run no daemon and need none.
- **For the host opener**: `curl`. The launcher replays a browser-login callback inside the VM with it, so every custom
  base needs `curl` even when the agent runs no daemon (the daemon agents' `curl` requirement covers the same tool). It
  is checked after your Dockerfile body runs, and a missing `curl` fails the build naming the tool. See
  [Opening links]({% link configuration/opening-links.md %}#logging-in-to-an-agent).
- The recommended CLI tools listed here are documented for your convenience, not as requirements — as a custom base you install
  your own.
- A base that already provides docker, node, or the agent is left alone (idempotency), and a pre-created `dev` user is
  tolerated.

Bases that do not provide these **out of the box** are not supported without a project Dockerfile that installs
them — for example **Alpine** (busybox `adduser`/`addgroup`, no shadow-utils or bash) and **distroless**/ **scratch**
(no shell or package manager).

When a base image already contains the selected agent, the image records the agent as **user-provided** and
agents-sandbox does not manage its upgrades. This works by `PATH` precedence, so a base-provided tool must live
**outside `/opt/agents-sandbox`**: that prefix is the tool-owned one, and the runner's tool `COPY` overwrites any
same-named file a base placed inside it. If that provenance has no recorded version and a later rebuild no longer
contains the agent, the image builder resolves a real release version before installing it; `user-provided` is
provenance
metadata, not an agent release version. A matching user-provided image is reused without resolving a release first.

### User context

The `dev` user (host UID/GID) is created as the first instruction of the final stage, and the image always ends with
`USER dev` active. If the host GID is already taken by another group in the base image (e.g. macOS `staff`/20 vs
`dialout`), the `dev` group is created with the next free GID. Your Dockerfile body runs in the final stage, so it may
switch to `USER dev` — but earlier build stages in a multi-stage Dockerfile run as root:

```dockerfile
FROM debian:trixie-slim

USER root
# Install your project's toolchain as root, e.g. via apt install
RUN apt-get update && apt-get install -y python3 && rm -rf /var/lib/apt/lists/*
```

### ENV configuration

ENV definitions in Dockerfiles are applied to running sandboxes. If you need to configure e.g. `PATH`, just set
`ENV PATH=...:` in your Dockerfile. The image's composed `PATH` is restored into login shells by the
`/etc/profile.d/agents-sandbox-path.sh` merge, so a project `ENV PATH` entry stays effective in `shell` and in `!`
commands, not only in non-login agent-executed commands.

## Docker-in-Docker

Your project Dockerfile declares the Docker engine on its own. The engine is enabled when the final stage starts `FROM
agents-sandbox/runner-base-docker:latest` (or the deprecated `runner-base-dind`) or when the final-stage body carries a
`# agents-sandbox:docker` comment line. This holds for a custom base and a managed base alike, so a Dockerfile that asks
for Docker needs no launcher config.

Explicit config overrides that declaration **in both directions**. The `--docker` flag (on `build`, `run`, `shell`, or
`build dockerfile`), the `docker` config key, and the `AGENTS_SANDBOX_DOCKER` environment variable — or a `dind`
spelling — turn the engine off even when the Dockerfile asks for it, and on with no Dockerfile signal. The precedence is
explicit config > Dockerfile content > off. A present-but-`false` value counts as explicit, so `docker: false`
suppresses a Dockerfile signal rather than letting it re-enable the engine.

Only a marker in the final stage (after the last `FROM`) enables Docker and places the injection block. A marker in an
earlier stage is ignored, with a warning telling you to move it into the final stage. Enablement is case-sensitive:
`# agents-sandbox:docker` enables and a case variant such as `# Agents-Sandbox:docker` does not, though it is reported as
a near-miss warning so a case variant of the marker is caught. A typo inside the token (`# agents-sandbox:dockr`) is not
caught and leaves the engine off without a diagnostic.

The deprecated aliases `--dind`, the `dind` config key, and `AGENTS_SANDBOX_DIND` still work with lower precedence and
print a one-time warning. A project Dockerfile using the legacy `runner-base-dind` base or the `# agents-sandbox:dind`
marker keeps working and enables the engine, but warns on every run that ensures the runner image (including cached
reuse).

The Docker stage installs the engine from a docker static binary tarball. The `vfs` storage driver is forced **inside
the VM** at start-up — the image never writes `/etc/docker/daemon.json`, so your edits to it are preserved. If your
`daemon.json` already sets `"storage-driver": "vfs"`, agents-sandbox leaves the file alone and starts dockerd without
the flag (passing both makes dockerd refuse to start); if it sets any other driver, start-up fails with an error, because
the microsandbox filesystem requires `vfs`. The launcher starts dockerd (and the agent's `serve` daemon) as a
microsandbox runtime-owned detached job inside the VM: startup is confirmed by health probe, and if the job fails the
launcher reports its exit code and a log tail (there is no `dockerd.log`). `buildx` and `docker compose` are **not**
installed — install them in your project Dockerfile if you need them. The static tarball's platform is selected by
`uname -m` (`x86_64`/`aarch64`).

With a custom base, a thin `COPY --from=agents-sandbox-docker` plus an adopt step is appended after your Dockerfile body
by default, so steps after it see `docker` (the heavy download stays in the cacheable Docker stage). To run your own steps
immediately after the engine is installed, place a `# agents-sandbox:docker` comment line in the final stage where the
block should be injected (the legacy `# agents-sandbox:dind` marker is still recognized, but warns on every ensure):

```dockerfile
FROM ubuntu:24.04

# The Docker runtime prerequisites are checked after this body runs, so install
# them here. The engine download runs on the tool base, so curl/tar are not
# needed in your image.
RUN apt-get update && apt-get install -y iptables git procps xz-utils && rm -rf /var/lib/apt/lists/*

# agents-sandbox:docker

# Steps that need docker, e.g. buildx and docker compose
RUN install -Dm755 /usr/local/bin/docker-buildx /usr/libexec/docker/cli-plugins/docker-buildx
```

## Node and the agent

The `agents-sandbox-node` stage installs Node.js (`v24.22.1`, official tarball) into `/opt/agents-sandbox`, and it is
emitted only when the selected agent requires node — today only `pi`. The `agents-sandbox-agent` stage installs the
selected agent into `/opt/agents-sandbox`. Both build on the tool base, so they do not depend on your base; whether the
base already provides node, the agent, or `dockerd` is resolved after the user body by the `/etc/agents-sandbox/agent-source`
and `/etc/agents-sandbox/docker-source` provenance files (`tool` | `user`), and a base-provided binary wins over the tool's
copy (the runner's `PATH` prefers base binaries). The pinned node stays on the v24 line:
the v26 tarballs gained a hard `libatomic.so.1` runtime dependency the managed base does not provide, while v24 has no
such dependency on either target architecture, so a custom base needs no extra library to run the tool-installed node.

Six agents are built in: `opencode`, `opencode2` (default, the opencode 2 beta), `pi`, `claude-code`, `codex`, and
`hermes`. Only `pi` needs node: its published package is an npm-installed `#!/usr/bin/env node` bundle, so it installs
through `npm install -g --prefix /opt/agents-sandbox @earendil-works/pi-coding-agent@$PI_VERSION`. The other five ship
self-contained native binaries or provision their own runtime — `opencode` and `opencode2` via their upstream
installers `https://opencode.ai/install` and `https://opencode.ai/v2/install`, `claude-code` via
`https://claude.ai/install.sh`, copying the binary into `/opt/agents-sandbox/bin`, and `codex` from the static
`codex-package-<arch>-unknown-linux-musl` asset of its `openai/codex` GitHub release, unpacked into
`/opt/agents-sandbox/codex` (codex runs commands through helpers in that package) and linked into
`/opt/agents-sandbox/bin`; their images contain no node/npm. `hermes` is the one whose installer needs more than a
binary copy: it clones the pinned release tag into `/opt/agents-sandbox/hermes` and provisions its own Python (and
Node, for the TUI/web product build) into `/opt/agents-sandbox/hermes-home`, so the tool stage needs no node of its
own, publishes its launcher into `/opt/agents-sandbox/bin`, and keeps its tree outside `/home/dev` — which the sandbox
home volume covers at runtime — handed to the runner's `dev` user after it is copied in (`ImageSpec.OwnedToolPaths`: a
`chown -R dev:dev` block emitted for agents that write into their own installation), because Hermes keeps its
dependency ledger and bytecode markers there. The launcher the stage publishes is therefore a small shim: it keeps user state in
`$HERMES_HOME` (`/home/dev/.hermes`, so config, sessions, and memories persist on the home volume) and links the tool
store, uv cache, and dependency environments from the image into it, so a fresh home volume starts Hermes without
reinstalling its dependencies. All six resolve their latest version for an unpinned build — opencode and hermes via the
GitHub releases endpoint, opencode2 via the opencode update API (`/update/api/latest/cli/npm`, the installer's own
source), pi via `pi.dev`, claude-code via the npm registry's `latest` dist-tag, and codex via its GitHub releases
endpoint — as a host-side HTTP query, never inside the image. JS-based LSP servers are expected to run on the agent's
embedded JS runtime (Bun) or on node that a project installs in its own Dockerfile.

## Labels and provenance

Each runner image carries these labels:

- `org.agents-sandbox.managed=true`
- `org.agents-sandbox.agent=<name>`
- `org.agents-sandbox.base=<ref>@sha256:<digest>`
- `org.agents-sandbox.identity.agent-version=<release>`

The `agent-version` label records the agent release baked into the image. The Upgrades section reads it to compare that
release against the new releases it finds.

## Upgrades

The agent version is pinned at image build time and the agent's runtime autoupdate is disabled per agent, so the agent
binary in a sandbox is stable across runs. The release baked into an image is recorded as the project's upgrade baseline
on every run and build, whether or not the image was rebuilt. Later runs therefore compare new releases against the
version your image actually holds: an accepted upgrade sticks instead of reverting to the previous release, and a
`--agent-version` pin survives unpinned runs. The recorded version comes from the image's `agent-version` label, which is
read locally and adds no network call to the prompt path.

By default the latest release available at build time is used for agents installed by agents-sandbox. Pin an explicit
version on the `build` command with `--agent-version`:

```console
agents-sandbox build --agent-version 0.5.0
agents-sandbox build          # uses the latest release
```

**How to upgrade:** Rebuild the runner image with `agents-sandbox build`, or pass `--agent-version <version>` to pin a
release. On `run`/`shell`, when a newer agent release exists than the version baked into the image, the launcher offers to
rebuild the image (interactive) or prints a notice advising `agents-sandbox build` (non-interactive). The prompt names both
the available release and the version currently baked into this project's image, so you can tell exactly what a rebuild
would change. The recorded version is only a cache of that label: whenever the image is present, the launcher reconciles
it with the label before deciding whether to build, so a recorded version never pulls the image backwards and an offline
check keeps the existing image instead of rebuilding. Only when the image itself is missing (for example after pruning)
does the recorded version stand on its own, so an unpinned run still sees the version last used and asks before changing
the image. `--rebuild` forces a clean rebuild but still offers the upgrade; it only bypasses the build cache, it does not
pin the version. When `agent-source=user`, the tool never checks for upgrades.

`--agent-version` applies when agents-sandbox installs the selected agent. It does not replace an agent already supplied
by a custom base or project Dockerfile — for a user-provided agent the pin is ignored. If a user-provided agent has no recorded
version, a rebuild that needs to install the missing binary resolves a normal release version rather than passing the internal
provenance value to the installer.

`--agent-version` is available on `build`, `run`, and `shell`. On `run`/`shell` it pins the version baked into the image and
skips the upgrade check. The deprecated `--opencode-version` alias remains `build`-only.
