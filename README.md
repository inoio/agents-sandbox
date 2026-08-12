# opencode-msb

> Run opencode inside an isolated microsandbox VM.

opencode-msb launches [opencode](https://github.com/anthropics/opencode) inside an isolated Linux VM backed
by [microsandbox](https://github.com/superradcompany/microsandbox). Each project gets a persistent project VM — shared
across sessions with a configurable auto-stop policy (see [Configuration](/docs/configuration.md) and
[Sandboxes](/docs/sandboxes.md)). The VM has your project mounted as `/workspace`, a persistent home directory
volume, and access to a curated toolchain (Node.js, ripgrep, jq, yq, curl, etc.).

## Quick Start

After installation, start a session in your current project:

```console
opencode-msb
```

Start an isolated session with a worktree:

```console
opencode-msb -w bugfix-my-fix
```

Get an overview over commands via `opencode-msb tree`.

### Serve opencode to a host client (e.g. Opencode Desktop)

`run --serve-only` (or `-s`) starts the project VM with the opencode port
published on the host, prints the URL for Opencode Desktop, and stays running
(no in-VM TUI) until you press `Ctrl-D`:

````console
opencode-msb run --serve-only
````

Connect Opencode Desktop to the printed `http://127.0.0.1:4096` URL. The server
is only bound to the host loopback. To add basic auth, set
`OPENCODE_SERVER_PASSWORD` (and optionally `OPENCODE_SERVER_USERNAME`) in the
project or user env before starting.

## Installation

Requires **Linux (KVM) or macOS (Apple Silicon)**.

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
* Check prerequisites:

   ```console
   opencode-msb doctor
   ```

See [Getting Started](/docs/getting-started.md) for prerequisites and a full setup guide.

## Usage

Full [Commands Reference](/docs/commands.md).

## Documentation

| Topic                                       | Description                            |
|---------------------------------------------|----------------------------------------|
| [Getting Started](/docs/getting-started.md) | Installation, prerequisites, first run |
| [Commands](/docs/commands.md)               | Complete CLI reference                 |
| [Configuration](/docs/configuration.md)     | Launcher config, env, secrets          |
| [Runner Image](/docs/runner-image.md)       | Base image, custom tooling             |
| [Worktree Sessions](/docs/branch-sessions.md) | Isolated worktree sessions for per-feature development |
| [Sandboxes](/docs/sandboxes.md)             | VM lifecycle, volumes, pruning         |
| [Troubleshooting](/docs/troubleshooting.md) | Common issues and fixes                |
