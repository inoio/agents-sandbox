# Getting Started

## What is opencode-msb?

opencode-msb is a launcher that runs [opencode](https://github.com/anthropics/opencode) inside an isolated microsandbox VM powered by [microsandbox](https://github.com/superradcompany/microsandbox). Each project gets a persistent VM identified by its git remote URL — first boot creates a fresh VM, subsequent runs connect to or start the existing one. The VM has your project bound as `/workspace`, a persistent home directory volume, and access to a curated toolchain.

## Prerequisites

opencode-msb requires:

- **Linux (KVM)** — macOS is not yet supported for hosting
- **Docker** running with rootless or rootful daemon
- **KVM** available (check with `kvm-ok` or `/dev/kvm` existence)
- **`msb`** CLI — the microsandbox runtime, installed via the [microsandbox install script](https://github.com/superradcompany/microsandbox)
- **Git** — for branch sessions and worktrees

Verify your setup:

```shell
opencode-msb doctor
```

## Installation

1. Download the latest Linux binary:

   ```shell
   curl -L -o opencode-msb https://gitlab.inoio.de/inoio/opencode-msb/-/releases/permalink/latest/downloads/opencode-msb-linux-amd64
   chmod +x opencode-msb
   mv opencode-msb ~/.local/bin/
   ```

2. Verify:

   ```shell
   opencode-msb --version
   ```

3. Run the doctor to check prerequisites:

   ```shell
   opencode-msb doctor
   ```

## Quick Start

Navigate to any git repository and run:

```shell
opencode-msb
```

This starts a microsandbox VM (or connects to the existing project VM), mounts your project at `/workspace`, and launches opencode. When the session ends, the VM remains for 30 seconds if no other session connects.

To start an isolated session for a different branch:

```shell
opencode-msb -b feature/my-branch
```

## How It Works

1. **Image build** — Builds a Docker image from `.opencode-msb/Dockerfile` if present, or uses the base image. The image contains opencode, Node.js 26, and common CLI tools.
2. **Volume setup** — Creates a persistent home volume (managed by msb, name: `opencode-msb-home-<slug>-<hash>`) for the project, preserving editor state, caches, and config across sessions.
3. **VM creation or reuse** — Creates a new project VM on first boot; subsequent runs connect to the existing VM (or start it if it stopped after the 30s idle timeout).
4. **Provisioning** — Provisions opencode config files into the VM and removes `.envrc` files.
5. **Opencode** — Runs `opencode attach` inside the VM, forwarding any arguments to the AI agent.
6. **Cleanup** — On exit, the session cleans up worktrees and prunes stale state.

See the [Commands](/docs/commands.md) reference for the full API and [Configuration](/docs/configuration.md) for tuning behavior.

## Next Steps

- Read the [Commands](/docs/commands.md) reference for the complete CLI API.
- Learn about [Configuration](/docs/configuration.md) for setting defaults and secrets.
- See how to [Extend the Runner Image](/docs/runner-image.md) with project-specific tooling.
- Explore [Branch Sessions](/docs/branch-sessions.md) for isolated development workflows.
- Check [Troubleshooting](/docs/troubleshooting.md) for common issues.
