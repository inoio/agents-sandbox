# Getting Started

## What is opencode-msb?

opencode-msb is a launcher that runs [opencode](https://github.com/anthropics/opencode) inside an isolated microsandbox
VM powered by [microsandbox](https://github.com/superradcompany/microsandbox). Each project gets a persistent VM
identified by its git remote URL — first boot creates a fresh VM, subsequent runs connect to or start the existing one.
The VM has your project bound as `/workspace`, a persistent home directory volume, and access to a curated toolchain.

## Prerequisites

opencode-msb requires platform-specific prerequisites depending on your operating system:

### Linux (KVM)

- **Docker** running with rootless or rootful daemon
- **KVM** available (check with `kvm-ok` or `/dev/kvm` existence)


### macOS (Apple Silicon)

- **Apple Silicon (arm64)** — Intel (x86_64) Macs are not supported.
- **Docker Desktop** or **colima** — ensure the Docker socket is accessible (`docker info` succeeds)

### OS independent

- **`msb`** CLI — the [microsandbox](https://github.com/superradcompany/microsandbox) runtime — opencode-msb tries to install it automatically
- **Git** — for branch sessions

If you're unsure your system fulfills the prerequisites, you can verify your setup:

```shell
opencode-msb doctor
```

## Installation

* Download the latest binary:

  **Linux (x86_64):**

  ```console
  curl -L -o opencode-msb https://gitlab.inoio.de/inoio/opencode-msb/-/releases/permalink/latest/downloads/opencode-msb-linux-amd64
  ```

  **macOS (Apple Silicon):**

  ```console
  curl -L -o opencode-msb https://gitlab.inoio.de/inoio/opencode-msb/-/releases/permalink/latest/downloads/opencode-msb-darwin-arm64
  ```

  **Linux (arm64):**

  ```console
  curl -L -o opencode-msb https://gitlab.inoio.de/inoio/opencode-msb/-/releases/permalink/latest/downloads/opencode-msb-linux-arm64
  ```
* Install:
  ```console
  chmod u+x opencode-msb
  mv opencode-msb ~/.local/bin # or any other directory in your PATH 
  ```

## Quick Start

Navigate to any directory and run:

```shell
opencode-msb
```

This starts a microsandbox VM (or connects to the existing project VM), mounts the working directory into the VM , and
launches opencode.

To start an isolated session for a different branch (only works in git repositories):

```shell
opencode-msb -w bugfix-my-fix
```

## How It Works

1. **Image build** — Builds a Docker image from `.opencode-msb/Dockerfile` if present, or uses the base image. The image
   contains opencode, Node.js 26, and common CLI tools.
2. **Volume setup** — Creates a persistent home volume (managed by msb, name: `opencode-msb-home-<project-slug>-<timestamp>`) for the
   project, preserving editor state, caches, and config across sessions.
3. **VM creation or reuse** — Creates a new project VM on first boot; subsequent runs connect to the existing VM (or
   restart it if it stopped).
4. **Provisioning** — Provisions the VM filesystem, syncs opencode config files into the VM.
5. **Opencode** — Runs `opencode attach` inside the VM, forwarding any arguments after `--` to the AI agent.
6. **Cleanup** — On exit, the session detaches. The VM-internal worktree is managed by opencode; on subsequent runs it is reused. The host repo is untouched.

See the [Commands](/docs/commands.md) reference for the full API and [Configuration](/docs/configuration.md) for tuning
behavior.

## Agent Context (AGENTS.md)

An `AGENTS.md` in the working directory can orient the agent on being in a sandbox VM, available tooling etc. Here is a minimal, self-explanatory example you can copy and adapt:

```dotenv
## Environment

You are running inside a sandbox VM, not on the host. Filesystem layout:

- `/workspace` bind mount of the host CWD, mounted rw.
- `~/.local/share/opencode/worktree/` git worktrees of `/workspace`, created by opencode

## Toolchain

Common CLI tools are preinstalled: git, node, npm, jq, yq etc. Don't install additional tools yourself without permission.
```

## Next Steps

- Read the [Commands](/docs/commands.md) reference for the complete CLI API.
- Learn about [Configuration](/docs/configuration.md) for setting defaults and secrets.
- See how to [Extend the Runner Image](/docs/runner-image.md) with project-specific tooling.
- Explore [Worktree Sessions](/docs/branch-sessions.md) for isolated worktree sessions.
- Check [Troubleshooting](/docs/troubleshooting.md) for common issues.
