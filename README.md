# opencode-msb

> Run opencode inside an isolated microsandbox VM.

opencode-msb launches [opencode](https://github.com/anthropics/opencode) inside an isolated Linux VM backed
by [microsandbox](https://github.com/superradcompany/microsandbox). Each project gets a persistent project VM — shared
across sessions with a 30s idle timeout. The VM has your project mounted as `/workspace`, a persistent home directory
volume, and access to a curated toolchain (Node.js, ripgrep, jq, yq, curl, etc.).

## Quick Start

After installation, start a session in your current project:

```console
opencode-msb
```

Start an isolated session for a specific branch:

```console
opencode-msb -b feature/my-feature
```

Get an overview over commands via `opencode-msb tree`.

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

## Home volumes

Home volumes persist across image changes. When you update your Dockerfile,
one home volume is created per project and reused. The tool tracks the active
volume in `~/.local/state/opencode-msb/{slug}/state.yaml`.

When you run `opencode-msb run` and an image change is detected, you'll be prompted:

  1) keep      - continue with existing home volume (default)
  2) migrate   - create fresh volume, copy all files from old volume on top (docker image changed /home/dev significantly, but you want to keep your opencode session history)
  3) reset     - replace with fresh volume from image (docker image changed /home/dev significantly, fresh start, you lose opencode session history)
  4) quit      - exit without starting a session

The choice is applied automatically and the old volume is always kept. The state file records the new image digest only
after the chosen action actually executes, so you are only prompted again after the next image change. `volume
migrate|reset|edit` remain available for manual management.

Home volumes are named `opencode-msb-home-{slug}-{timestamp}`.

## Usage

Full [Commands Reference](/docs/commands.md).

## Documentation

| Topic                                       | Description                            |
|---------------------------------------------|----------------------------------------|
| [Getting Started](/docs/getting-started.md) | Installation, prerequisites, first run |
| [Commands](/docs/commands.md)               | Complete CLI reference                 |
| [Configuration](/docs/configuration.md)     | Launcher config, env, secrets          |
| [Runner Image](/docs/runner-image.md)       | Base image, custom tooling             |
| [Branch Sessions](/docs/branch-sessions.md) | Isolated workflow for branches         |
| [Sandboxes](/docs/sandboxes.md)             | VM lifecycle, volumes, pruning         |
| [Troubleshooting](/docs/troubleshooting.md) | Common issues and fixes                |
