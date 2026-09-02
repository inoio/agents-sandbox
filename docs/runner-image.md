---
title: Runner Image
layout: default
nav_order: 50
---
# Runner Image

opencode-sandbox builds a Docker image for each sandbox. The image contains opencode, Node.js, and common CLI tools.
Projects can extend the image with their own tooling.

## Home directory

When rebuilding an image, all state in the VM's home directory (`/home/dev/`) would get lost. Therefore, the home
directory is copied to a volume and mounted to the running VM.

If the image changes between runs and the VM is rebuilt onto the new image, you will be presented with a prompt to keep,
migrate, or reset your home volume (the prompt is skipped when the rebuild is deferred and the VM keeps running on the
current image). In non-interactive mode the existing home volume is kept and you are told the image changed. The chosen
action is applied automatically and the old volume is always kept. The state file is only updated once the action has
actually executed. `volume migrate`, `volume reset` and `volume edit` remain available for manual management.

## One image per project

opencode-sandbox builds a single runner image per project. The rendered Dockerfile is assembled from your project
Dockerfile (if any) plus tool-owned blocks:

- **Base** — the embedded `debian:trixie-slim` tools block, or your whole custom base. For a managed base
  (`FROM .../runner-base...`), the final stage's `FROM` is replaced **in place** with the embedded tools block, keeping
  any earlier build stages above it — so multi-stage project Dockerfiles are supported.
- **Dev user block** — the first instruction of the final stage, inserted right after the final `FROM`: creates the
  `dev` user (host UID/GID), reserving its identity before anything else in the stage runs.
- **Docker-in-Docker block** *(optional)* — only when dind is enabled.
- **Agent block** — Node.js and the coding agent.
- **Finalize block** — adds `dev` to the docker group, switches to `USER dev`, and sets `WORKDIR /workspace`.

Every tool-owned block is `USER root`-prefixed so agent/dind installs always run as root regardless of what user your
Dockerfile leaves active. The image always ends with `USER dev` and `WORKDIR /workspace`.

## Base starting point

By default the base tools block starts from `debian:trixie-slim` and installs the recommended CLI tools: `git`,
`ripgrep`, `jq`, `yq`, `curl`, `wget`, `xz-utils`, `file`, `gawk`, `less`, `lz4`, `moreutils`, `net-tools`, `parallel`,
`recode`, `uuid`, and `iptables`.

A project Dockerfile whose `FROM` is any other image is treated as a **custom base**, and the agent (and optional dind)
blocks are layered on top of it:

- The custom base must provide `curl` and `bash`; the `pi`/`claude-code` agents install Node.js themselves if it is
  absent.
- The recommended CLI tools above are documented for your convenience — as a custom base you install your own.
- `iptables`, `git`, `ps`, `xz`, `curl`, and `tar` are required only when dind runs; if one is missing, the dind build
  fails and names the missing package.
- A base that already provides docker, node, or the agent is left alone (idempotency), and a pre-created `dev` user is
  tolerated.

### Important: User context

The `dev` user (host UID/GID) is created as the first instruction of the final stage, and the image always ends with
`USER dev` active. Your Dockerfile body runs in the final stage, so it may switch to `USER dev` — but earlier build
stages in a multi-stage Dockerfile run as root:

```dockerfile
FROM debian:trixie-slim

USER root
# Install your project's toolchain as root, e.g. via apt install
RUN apt-get update && apt-get install -y python3 && rm -rf /var/lib/apt/lists/*
```

### ENV configuration

ENV definitions in Dockerfiles are applied to running sandboxes. If you need to configure e.g. `PATH`, just set
`ENV PATH=...:` in your Dockerfile.

## Docker-in-Docker

Enable Docker-in-Docker (dind) in the runner image with the `--dind` flag (on `build`, `run`, or `shell`) or the `dind:
true` config key. A project Dockerfile still starting `FROM .../runner-base-dind:latest` keeps working and implies it.

The dind block installs the engine from the docker static tarball, pinned to `27.5.1`. The `vfs` storage driver is always
forced for microsandbox compatibility. `buildx` and `docker compose` are **not** installed — install them in your project
Dockerfile if you need them. The static tarball is selected by `uname -m` (`x86_64`/`aarch64`).

## Node and the agent

The agent block installs Node.js (`v22.14.0`, official tarball) only if it is absent, and installs the selected agent
only if its binary is absent — so an existing install is left alone (idempotency). What the block actually did is
recorded in `/etc/opencode-sandbox/agent-source` and `/etc/opencode-sandbox/docker-source`.

Three agents are built in: `opencode` (default), `pi` (installed via `npm i -g @earendil-works/pi-coding-agent`), and
`claude-code` (installed via `npm i -g @anthropic-ai/claude-code`).

## Labels & provenance

Each runner image carries these labels:

- `org.opencode-sandbox.managed=true`
- `org.opencode-sandbox.agent=<name>`
- `org.opencode-sandbox.base=<ref>@sha256:<digest>`

There is no tool-version label.

## Upgrades

The agent version is pinned at image build time and the agent's runtime autoupdate is disabled per agent, so the agent
binary in a sandbox is stable across runs. Instead of reading the version from an image label, the version is detected
on first boot and recorded for upgrade checks.

By default the latest release available at build time is used. Pin an explicit version on the `build` command with
`--agent-version`:

```console
opencode-sandbox build --agent-version 0.5.0
opencode-sandbox build          # uses the latest release
```

**How to upgrade:** Rebuild the runner image with `opencode-sandbox build`. To pin a specific version, use
`--agent-version`. On `run`/`shell`, when a newer agent release exists than the version baked into the image, the
launcher offers to rebuild the image (interactive) or prints a notice advising `opencode-sandbox build`
(non-interactive). When `agent-source=user`, the tool never checks for upgrades.

> `--agent-version` is only available on the `build` command — it is not supported on `run` or `shell` (which pin the
> version baked into the image). The deprecated `--opencode-version` alias is likewise `build`-only.

## Building and Managing Images

Build the image:

```console
opencode-sandbox build        # builds if image is missing or base changed
opencode-sandbox build -r     # force rebuild
```

List cached images:

```console
opencode-sandbox image list
```

The image name includes the project slug and Docker image hash, so changes to the Dockerfile automatically trigger a
rebuild. When the resulting image digest differs from the image the existing project VM was booted from, the VM is
recreated on the next run so it uses the new image (the home volume is preserved).

## Image Lifecycle

Images can be pruned via the `prune` command or, more targeted, the `image prune` subcommand:

```console
opencode-sandbox image prune --dry-run   # see what's stale
opencode-sandbox image prune             # actually remove them
```

See [Commands]({% link commands.md %}) for details on the prune command.

opencode-sandbox also auto-prunes all resources that are ephemeral, unused or haven't been in use for more than 30 days by
default. Cached runner images and home volumes are only pruned once they are older than the threshold, so a recently
used project keeps its image and home state across restarts. See [Sandboxes]({% link sandboxes.md %}) for more information
and [Configuration]({% link configuration.md %}) for how to configure auto-pruning.