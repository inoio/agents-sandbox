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

## Base Image

The default runner image `opencode-sandbox/runner-base` is built from `debian:trixie-slim` and includes:

- **opencode** — symlinked to `/usr/local/bin/opencode`
- **Node.js 26.x** — for opencode [LSP servers](https://opencode.ai/docs/lsp/)
- **CLI tools** — `git`, `ripgrep`, `jq`, `yq`, `curl`, `wget`, `xz-utils`, `file`, `gawk`, `less`, `lz4`, `moreutils`,
  `net-tools`, `parallel`, `recode`, `uuid`

It creates and switches to the `dev` user and sets the workdir to `/workspace` — the mount point for the working
directories.

## opencode autoupdate and upgrades

opencode is pinned to a specific version at image build time and its runtime autoupdate is disabled via
`OPENCODE_DISABLE_AUTOUPDATE=true` in the image, so the opencode binary in a sandbox is stable across runs. The
pinned version is recorded as a Docker label (`org.opencode-sandbox.opencode-version`) on the image.

By default the latest release available at build time is used. Pin an explicit version on the `build` command with
`--agent-version` (the older `--opencode-version` remains as a deprecated alias):

```console
opencode-sandbox build --agent-version 0.5.0
opencode-sandbox build          # uses the latest release
```

**How to upgrade:** Rebuild the runner image with `opencode-sandbox build`. To pin a specific version, use
`--agent-version`.

The `--agent <name>` flag selects the coding-agent profile to build. Three agents are built in: `opencode` (default),
`pi` (installed via `npm i -g @earendil-works/pi-coding-agent`), and `claude-code` (installed via `npm i -g
@anthropic-ai/claude-code`). All three resolve their latest version for an unpinned build — opencode via its GitHub
releases endpoint, pi via `pi.dev`, and claude-code via the npm registry's `latest` dist-tag.

On `run`/`shell`, when a newer agent release exists than the version baked into the image, the launcher offers to rebuild
the image (interactive) or prints a notice advising `opencode-sandbox build` (non-interactive). Images that predated the
version label are force-rebuilt to pin a version.

The update check is rate-limited to keep it quiet:

- **Once per day:** the agent's release endpoint (opencode's GitHub releases, pi's `pi.dev`, claude-code's npm registry)
  is queried at most once per 24 hours, machine-wide. The last successful check is recorded in the tool's state directory
  (`~/.local/state/opencode-sandbox/updater.yaml`). A failed or offline check does not start the window, so the next
  online session retries.
- **Once per version:** each agent version is offered for a rebuild at most once. Once the prompt for a version has
  been shown, that version is not offered again even on later days.

The agent version baked into the image is also recorded in `updater.yaml` and reused on `run`/`shell`. This keeps the
image identity (and therefore the cached microsandbox image) stable across runs: the version is only re-resolved from the
network when an upgrade is actually performed (via the upgrade prompt or `build`), rather than on every invocation.

> `--agent-version` is only available on the `build` command — it is not supported on `run` or `shell` (which pin the
> version baked into the image). The deprecated `--opencode-version` alias is likewise `build`-only.

## Docker-in-Docker Base Image

On top of the base image, opencode-sandbox provides an image with Docker-in-Docker (dind) enabled,
`opencode-sandbox/runner-base-dind`.

## Custom Runner

To add project-specific tools, create `.opencode-sandbox/Dockerfile` in your project, starting from one of the base images:

```dockerfile
FROM opencode-sandbox/runner-base:latest
# or FROM opencode-sandbox/runner-base-dind:latest

# Install your project's toolchain into the dev user directory
RUN curl -fsSL https://pyenv.run | bash
```

### Important: User context

The project image **must** end with `USER dev` active. If you need to run commands as root, switch back:

```dockerfile
FROM opencode-sandbox/runner-base:latest

USER root
# Install your project's toolchain as root, e.g. via apt install
RUN apt-get update && apt-get install -y python3 && rm -rf /var/lib/apt/lists/*

USER dev
# everything else as dev
```

### ENV configuration

ENV definitions in Dockerfiles are applied to running sandboxes. If you need to configure e.g. `PATH`, just set
`ENV PATH=...:` in your Dockerfile.

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