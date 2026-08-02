# Runner Image

opencode-msb builds a Docker image for each sandbox. The image contains opencode, Node.js, and common CLI tools. Projects can extend the image with their own tooling.

## Base Image

The default runner image is built from `debian:trixie-slim` and includes:

- **opencode** — symlinked to `/usr/local/bin/opencode`
- **Node.js 26.x** — for language server protocols (pyright, tsserver, etc.)
- **CLI tools** — `git`, `ripgrep`, `jq`, `yq`, `curl`, `wget`, `xz-utils`, `file`, `gawk`, `less`, `lz4`, `moreutils`, `net-tools`, `parallel`, `recode`, `uuid`
- **`dev` user** — UID 1000, GID 1000, with `HOME=/home/dev` and `/bin/bash` shell
- **`/workspace`** — working directory

```dockerfile
FROM debian:trixie-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl git ripgrep file gawk jq less lz4 moreutils net-tools parallel recode uuid wget xz-utils yq \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://opencode.ai/install | bash && cp /root/.opencode/bin/opencode /usr/local/bin

RUN curl -fsSL https://deb.nodesource.com/setup_26.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    rm -rf /var/lib/apt/lists/*

ARG USER_UID=1000
ARG USER_GID=1000
RUN groupadd -g "$USER_GID" dev && \
    useradd -m -u "$USER_UID" -g "$USER_GID" -s /bin/bash dev && \
    chown -R dev:dev /home/dev

USER dev
WORKDIR /workspace
```

## Custom Runner

To add project-specific tools, create `.opencode-msb/Dockerfile` starting from the base image:

```dockerfile
FROM opencode-msb/runner:base

USER root
# Install Go, Python, Rust toolchain, etc.
RUN curl -fsSL https://go.dev/dl/go1.21.0.linux-amd64.tar.gz | tar -C /usr/local -xzf -
RUN apt-get update && apt-get install -y python3 && rm -rf /var/lib/apt/lists/*

USER dev
```

### Important: User context

The project image **must** end with `USER dev`. If you need to run commands as root, switch back:

```dockerfile
FROM opencode-msb/runner:base

USER root
# root-only operations
RUN apt-get update && apt-get install -y some-package

USER dev
# everything else as dev
RUN pip3 install ...
```

### PATH configuration

To add tools to the VM's `PATH`, set `ENV PATH=...:` in your Dockerfile. The launcher reads the image's `Config.Env` at VM creation time and passes these to the sandbox via `msb.WithEnv()`, making them available to all processes including the init process (PID 1, which runs `opencode serve`).

For root-installed tools (Go, etc.):

```dockerfile
FROM opencode-msb/runner:base

USER root
ENV PATH="/usr/local/go/bin:$PATH"
# Install Go binaries
RUN curl -fsSL https://go.dev/dl/go1.23.0.linux-amd64.tar.gz | tar -C /usr/local -xzf -

USER dev
```

For user-installed tools (msb CLI, Node.js binaries):

```dockerfile
USER dev
RUN curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.7/install.sh | sh

USER root
ENV PATH="/home/dev/.microsandbox/bin:$PATH"
```

The `ENV` directives from all layers (base image + project overrides) are collected from the final image's OCI config and merged into the sandbox environment.

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

The image name includes the project slug and Dockerfile hash, so changes to the Dockerfile automatically trigger a rebuild.

## Image Lifecycle

Images are never auto-pruned by opencode-msb. They accumulate over time. Use the `prune` command to remove unused resources:

```console
opencode-msb prune --dry-run   # see what's stale
opencode-msb prune             # actually remove them
```

