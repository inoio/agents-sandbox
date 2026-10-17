---
title: Runner Image
description: "The Docker image agents-sandbox builds for each sandbox, and how to extend it with your project's tooling."
layout: default
nav_order: 80
---

# Runner Image

agents-sandbox builds a Docker image for each sandbox. The image contains the selected coding agent, Node.js, and common
CLI tools.
Projects can extend the image with their own tooling.

## Home directory

When rebuilding an image, all state in the VM's home directory (`/home/dev/`) would get lost. Therefore, the home
directory is copied to a volume and mounted to the running VM.

If the image changes between runs and the VM is rebuilt onto the new image, you will be presented with a prompt to keep,
migrate, or reset your home volume (the prompt is skipped when the rebuild is deferred and the VM keeps running on the
current image). In non-interactive mode the existing home volume is kept and you are told the image changed. The chosen
action is applied automatically and the old volume is always kept. The state file is only updated once the action has
actually executed. `volume migrate`, `volume reset` and `volume edit` remain available for manual management.

When a rebuilt image uses the same runner-image tag, agents-sandbox refreshes the microsandbox cache by loading the new
Docker image directly under that tag. It does not remove the old cached manifest first, because a stopped project VM can
still reference it. Unused image data is reclaimed later by image pruning.

## One image per project

agents-sandbox builds a single runner image per project. The rendered Dockerfile is composed of per-tool multistage
stages with fixed parents, merged into the final stage with `COPY` **after** the user body:

- **`agents-sandbox-base`** — the embedded `debian:trixie-slim` tools block for a managed base, or your whole custom base
  (`FROM` image) for a custom base. For a managed base (`FROM .../runner-base...`), the final stage's `FROM` is replaced
  **in place** with the embedded tools block; for a custom base, any earlier build stages in your project Dockerfile are
  preserved above it — so multi-stage project Dockerfiles are supported.
- **`agents-sandbox-toolbase`** — the internal stage the node, agent, and docker stages build on: a pinned
  `debian:trixie-slim` plus `curl`, `ca-certificates`, and `tar`. It is **not** part of the runner's lineage, so your base
  needs none of those binaries and the tool layers cache across bases.
- **`agents-sandbox-node`** *(only for agents that need node)* — installs Node.js into `/opt/agents-sandbox`. It is emitted
  only when the selected agent requires node (today only `pi`, an npm-installed node bundle); `opencode`, `opencode2`, and
  `claude-code` install native binaries and their images contain no node/npm.
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

## Base starting point

By default the base tools block starts from `debian:trixie-slim` and installs the recommended CLI tools: `git`,
`ripgrep`, `jq`, `yq`, `curl`, `wget`, `xz-utils`, `file`, `gawk`, `less`, `lz4`, `moreutils`, `net-tools`, `parallel`,
`recode`, `uuid`, and `iptables`.

A project Dockerfile whose `FROM` is any other image is treated as a **custom base**: your final `FROM` becomes the base
stage the runner builds on, while the node, agent, and optional docker stages build on the tool base, independent of your
image. For a custom base, the earlier build stages in your project Dockerfile are kept above the base stage. The custom
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
- The recommended CLI tools above are documented for your convenience — as a custom base you install your own.
- A base that already provides docker, node, or the agent is left alone (idempotency), and a pre-created `dev` user is
  tolerated.

Bases that do not provide the above **out of the box** are not supported without a project Dockerfile that installs
them — for example **Alpine** (busybox `adduser`/`addgroup`, no shadow-utils or bash) and **distroless**/ **scratch**
(no shell or package manager).

When a base image already contains the selected agent, the image records the agent as **user-provided** and
agents-sandbox does not manage its upgrades. This works by `PATH` precedence, so a base-provided tool must live
**outside `/opt/agents-sandbox`**: that prefix is the tool-owned one, and the runner's tool `COPY` overwrites any
same-named file a base placed inside it. If that provenance has no recorded version and a later rebuild no longer
contains the agent, the image builder resolves a real release version before installing it; `user-provided` is
provenance
metadata, not an agent release version. A matching user-provided image is reused without resolving a release first.

### Important: User context

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

Enable the Docker engine (Docker-in-Docker) in the runner image with the `--docker` flag (on `build`, `run`, `shell`, or
`build dockerfile`) or the `docker: true` config key, or the `AGENTS_SANDBOX_DOCKER` environment variable. The
deprecated aliases `--dind`, the `dind` config key, and `AGENTS_SANDBOX_DIND` still work with lower precedence and print a
one-time warning. A project Dockerfile still starting `FROM .../runner-base-dind:latest` keeps working and implies it, but
warns on every run that ensures the runner image (including cached reuse); the newer `FROM .../runner-base-docker:latest`
implies it silently.

The Docker stage installs the engine from a docker static binary tarball. The `vfs` storage driver is always forced by
adding `--storage-driver=vfs` to the dockerd start command **inside the VM** — the image no longer writes
`/etc/docker/daemon.json`, so a user's edits to it are preserved. `buildx` and `docker compose` are **not** installed —
install them in your project Dockerfile if you need them. The static tarball's platform is selected by `uname -m`
(`x86_64`/`aarch64`).

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

The `agents-sandbox-node` stage installs Node.js (`v24.21.0`, official tarball) into `/opt/agents-sandbox`, and it is
emitted only when the selected agent requires node — today only `pi`. The `agents-sandbox-agent` stage installs the
selected agent into `/opt/agents-sandbox`. Both build on the tool base, so they do not depend on your base; whether the
base already provides node, the agent, or `dockerd` is resolved after the user body by the `/etc/agents-sandbox/agent-source`
and `/etc/agents-sandbox/docker-source` provenance files (`tool` | `user`), and a base-provided binary wins over the tool's
copy (the runner's `PATH` prefers base binaries). The pinned node stays on the v24 line:
the v26 tarballs gained a hard `libatomic.so.1` runtime dependency the managed base does not provide, while v24 has no
such dependency on either target architecture, so a custom base needs no extra library to run the tool-installed node.

Four agents are built in: `opencode` (default), `opencode2` (the opencode 2 beta), `pi`, and `claude-code`. Only `pi`
needs node: its published package is an npm-installed `#!/usr/bin/env node` bundle, so it installs through
`npm install -g --prefix /opt/agents-sandbox @earendil-works/pi-coding-agent@$PI_VERSION`. The other three ship
self-contained native binaries and install through their upstream installers — `opencode` and `opencode2` via
`https://opencode.ai/install` and `https://opencode.ai/v2/install`, `claude-code` via `https://claude.ai/install.sh` —
copying the binary into `/opt/agents-sandbox/bin`; their images contain no node/npm. All four resolve their latest
version for an unpinned build — opencode via its GitHub releases endpoint, opencode2 via the npm registry's `beta`
dist-tag, pi via `pi.dev`, and claude-code via the npm registry's `latest` dist-tag — as a host-side HTTP query, never
inside the image. JS-based LSP servers are expected to run on the agent's embedded JS runtime (Bun) or on node that a
project installs in its own Dockerfile.

## Labels & provenance

Each runner image carries these labels:

- `org.agents-sandbox.managed=true`
- `org.agents-sandbox.agent=<name>`
- `org.agents-sandbox.base=<ref>@sha256:<digest>`

There is no tool-version label.

## Upgrades

The agent version is pinned at image build time and the agent's runtime autoupdate is disabled per agent, so the agent
binary in a sandbox is stable across runs. Instead of reading the version from an image label, the version is detected
on first boot and recorded for upgrade checks.

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
would change. When no version has been recorded for the project yet (for example after upgrading agents-sandbox), the
launcher reads it once from the project's existing image, so an unpinned run still asks before changing the image and an
offline check keeps the existing image instead of rebuilding. `--rebuild` forces a clean
rebuild but still offers the upgrade; it only bypasses the build cache, it does not pin the version. When `agent-source=user`, the
tool never checks for upgrades.

`--agent-version` applies when agents-sandbox installs the selected agent. It does not replace an agent already supplied
by a custom base or project Dockerfile — for a user-provided agent the pin is ignored. If a user-provided agent has no recorded
version, a rebuild that needs to install the missing binary resolves a normal release version rather than passing the internal
provenance value to the installer.

`--agent-version` is available on `build`, `run`, and `shell`. On `run`/`shell` it pins the version baked into the image and
skips the upgrade check. The deprecated `--opencode-version` alias remains `build`-only.

## Building and Managing Images

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

The image name includes the project slug and Docker image hash, so changes to the Dockerfile automatically trigger a
rebuild. When the resulting image digest differs from the image the existing project VM was booted from, the VM is
recreated on the next run so it uses the new image (the home volume is preserved).

Runner images are tagged **per agent** on both Docker and microsandbox. The `-latest` tag is namespaced by the agent,
so each agent's image can be identified and updated independently:

```
agents-sandbox/runner-<slug>:<agent>-latest
```

For example, with a project slug of `my-project`, the opencode image is
`agents-sandbox/runner-my-project:opencode-latest`
and the pi image is `agents-sandbox/runner-my-project:pi-latest`.

### Skip-build when unchanged

The Docker build is skipped when the baked `org.agents-sandbox.dockerfile-id` label matches the current content. This
label is a hash of the rendered Dockerfile and the agent version, so an image already built from the exact same
Dockerfile and agent version is reused instead of being rebuilt.

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

Before skipping a load, `EnsureLoaded` verifies the microsandbox cache content — the config digest (compared against
the Docker image ID) or the `dockerfile-id` label. If the cached content no longer matches, the image is reloaded so the
VM always boots from the correct image. At verbose level the load states whether the image was absent from the cache or
its cached content was stale.

## Image Lifecycle

Images can be pruned via the `prune` command or, more targeted, the `image prune` subcommand:

```console
agents-sandbox image prune --dry-run   # see what's stale
agents-sandbox image prune             # actually remove them (prompts; or pass --yes)
```

Prune prompts for confirmation before removing anything (defaulting to no). Pass `--yes` to skip the prompt;
non-interactively, `--yes` is required. `--dry-run` only previews and never prompts.

Pruning retains the `-latest` images **per agent** per live project and any image a kept VM still references, reclaiming
every other ref — pre-redesign digest refs no sandbox uses, orphaned surplus images, and every ref of projects (slugs)
that no longer have a live VM. It also runs microsandbox's native image-data prune so manifests and layers orphaned by a
runner-image refresh are reclaimed after no VM references them. See [Commands]({% link commands.md %}) for details on
the
prune command.

agents-sandbox also auto-prunes all resources that are ephemeral, unused or haven't been in use for more than 30 days by
default. Cached runner images and home volumes are only pruned once they are older than the threshold, so a recently
used project keeps its image and home state across restarts. See [Sandboxes]({% link sandboxes.md %}) for more
information
and [Configuration]({% link configuration/index.md %}) for how to configure auto-pruning.
