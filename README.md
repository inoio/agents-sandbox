# opencode-msb

> Run opencode inside an isolated microsandbox VM.

opencode-msb launches [opencode](https://github.com/anthropics/opencode) inside an isolated Linux VM backed by [microsandbox](https://github.com/superradcompany/microsandbox). Each project gets a persistent project VM — shared across sessions with a 30s idle timeout. The VM has your project mounted as `/workspace`, a persistent home directory volume, and access to a curated toolchain (Node.js, ripgrep, jq, yq, curl, etc.).

## Quick Start

Start a session in your current project:

```console
opencode-msb
```

Start an isolated session for a specific branch:

```console
opencode-msb -b feature/my-feature
```

## Installation

Requires **Linux (KVM)**. macOS is not yet supported.

1. Download the latest binary:

   ```console
   curl -L -o opencode-msb https://gitlab.inoio.de/inoio/opencode-msb/-/releases/permalink/latest/downloads/opencode-msb-linux-amd64
   chmod +x opencode-msb
   mv opencode-msb ~/.local/bin/
   ```

2. Check prerequisites:

   ```console
   opencode-msb doctor
   ```

See [Getting Started](/docs/getting-started.md) for prerequisites and a full setup guide.

## Usage

| Command      | Aliases              | Purpose                      |
|--------------|----------------------|------------------------------|
| `run`        | `sandbox run`        | Run opencode in a sandbox    |
| `shell`      | `sandbox shell`      | Open a debug shell           |
| `build`      | `image build`        | Build / rebuild the runner   |
| `list` / `ls`| `sandbox list`       | List sandboxes               |
| `stop`       | `sandbox stop`       | Stop the project VM          |
| `kill`       | `sandbox kill`       | Force-kill the project VM    |
| `prune`      | —                    | Prune stale resources        |
| `doctor`     | —                    | Check prerequisites          |
| `config show`| —                    | Inspect merged config        |
| `image list` | `image ls`           | List cached images           |
| `volume list`| `volume ls`          | List managed volumes         |

`opencode-msb` with no subcommand runs `run`. Arguments after `--` are forwarded to opencode.

Full [Commands Reference](/docs/commands.md).

## Documentation

| Topic                          | Description                              |
|--------------------------------|------------------------------------------|
| [Getting Started](/docs/getting-started.md) | Installation, prerequisites, first run |
| [Commands](/docs/commands.md)  | Complete CLI reference                    |
| [Configuration](/docs/configuration.md) | Launcher config, env, secrets |
| [Runner Image](/docs/runner-image.md)    | Base image, custom tooling |
| [Branch Sessions](/docs/branch-sessions.md) | Isolated workflow for branches |
| [Sandboxes](/docs/sandboxes.md)          | VM lifecycle, volumes, pruning |
| [Troubleshooting](/docs/troubleshooting.md) | Common issues and fixes |

## Key Concepts

- **Image** — A Docker image (base + optional project overrides) that defines the toolchain available in the VM.
- **Home volume** — Persistent `$HOME` storage per project, surviving sessions.
- **Project VM** — A per-project VM that stays alive between sessions (30s idle timeout before stopping).
- **Branch session** — An isolated opencode-managed worktree inside the VM for development without touching the parent workspace.
