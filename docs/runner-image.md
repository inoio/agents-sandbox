# Runner Image

opencode-msb builds a Docker image for each sandbox. The image contains opencode, Node.js, and common CLI tools.
Projects can extend the image with their own tooling.

## Home directory

The runner image provides default files under `/home/dev/` — shell configs,
default prompts, and pre-installed tools. When opencode-msb first starts, it
copies these defaults into your home volume.

Subsequent runs reuse your existing home volume, preserving your installed tools,
config files, and session history. Your home directory survives Dockerfile changes.

If the image changes between runs, you will be presented with a prompt to keep,
migrate, or reset your home volume. See `volume migrate` and `volume reset` for
manual management.

## Base Image

The default runner image `opencode-msb/runner-base` is built from `debian:trixie-slim` and includes:

- **opencode** — symlinked to `/usr/local/bin/opencode`
- **Node.js 26.x** — for opencode [LSP servers](https://opencode.ai/docs/lsp/)
- **CLI tools** — `git`, `ripgrep`, `jq`, `yq`, `curl`, `wget`, `xz-utils`, `file`, `gawk`, `less`, `lz4`, `moreutils`,
  `net-tools`, `parallel`, `recode`, `uuid`

It creates and switches to the `dev` user and sets the workdir to `/workspace` — the mount point for the working
directories.

## Docker-in-Docker Base Image

On top of the base image, opencode-msb provides an image with Docker-in-Docker (dind) enabled,
`opencode-msb/runner-base-dind`.

## Custom Runner

To add project-specific tools, create `.opencode-msb/Dockerfile` in your project, starting from one of the base images:

```dockerfile
FROM opencode-msb/runner-base:latest
# or FROM opencode-msb/runner-base-dind:latest

# Install your project's toolchain into the dev user directory
RUN curl -fsSL https://pyenv.run | bash
```

### Important: User context

The project image **must** end with `USER dev` active. If you need to run commands as root, switch back:

```dockerfile
FROM opencode-msb/runner:base

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
opencode-msb build        # builds if image is missing or base changed
opencode-msb build -r     # force rebuild
```

List cached images:

```console
opencode-msb image list
```

The image name includes the project slug and Docker image hash, so changes to the Dockerfile automatically trigger a
rebuild.

## Image Lifecycle

Images can be pruned via the `prune` command:

```console
opencode-msb prune --dry-run   # see what's stale
opencode-msb prune             # actually remove them
```

See [Commands](/docs/commands.md) for details on the prune command.

opencode-msb also auto-prunes all resources that are ephemeral, unused or haven't been in use for more than 30 days by
default. See [Sandboxes](/docs/sandboxes.md) for more information and [Configuration](/docs/configuration.md) for how to
configure auto-pruning.