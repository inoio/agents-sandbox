# opencode-sandbox

> opencode, supercharged — safely. Launch opencode inside a nearly instant microsandbox VM with your project mounted at /workspace. Unleash your agents' full permissions in a disposable cage, so they can do their best work without ever roaming your host or ever learning your secrets.

[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](https://github.com/inoio/opencode-sandbox/blob/main/LICENSE.md)
[![CI](https://github.com/inoio/opencode-sandbox/actions/workflows/ci.yml/badge.svg)](https://github.com/inoio/opencode-sandbox/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/inoio/opencode-sandbox)](https://go.dev/)
[![Release](https://img.shields.io/github/v/release/inoio/opencode-sandbox)](https://github.com/inoio/opencode-sandbox/releases)
[![Coverage](https://codecov.io/gh/inoio/opencode-sandbox/branch/main/graph/badge.svg)](https://codecov.io/gh/inoio/opencode-sandbox)
[![Security](https://img.shields.io/badge/security-policy-purple.svg)](SECURITY.md)
[![Docs](https://img.shields.io/badge/docs-github--pages-blue)](https://inoio.github.io/opencode-sandbox/)

opencode-sandbox launches [opencode](https://github.com/anthropics/opencode) inside an isolated Linux VM backed
by [microsandbox](https://github.com/superradcompany/microsandbox). Each project gets a persistent project VM — shared
across sessions with a configurable auto-stop policy (see [Configuration](/docs/configuration.md) and
[Sandboxes](/docs/sandboxes.md)). The VM has your project mounted as `/workspace`, a persistent home directory
volume, and access to a curated toolchain (Node.js, ripgrep, jq, yq, curl, etc.). Network egress/ingress is
config-driven (profiles + allow/deny lists, incl. an allowlist-only `profile: none`), and per-project defaults can be set with a
per-slug user config at `~/.config/opencode-sandbox/<slug>/`.

## Quick Start

After installation, start a session in your current project:

```console
opencode-sandbox
```

Start an isolated session with a worktree:

```console
opencode-sandbox -w bugfix-my-fix
```

Get an overview over commands via `opencode-sandbox tree`.

### Serve opencode to a host client (e.g. Opencode Desktop)

`run --serve-only` (or `-s`) starts the project VM with the opencode port
published on the host, prints the URL for Opencode Desktop, and stays running
(no in-VM TUI) until you press `Ctrl-D`:

````console
opencode-sandbox run --serve-only
````

Connect Opencode Desktop to the printed `http://127.0.0.1:4096` URL. The host
port is bound only to the host loopback (never exposed on the LAN); the in-VM
`opencode serve` daemon listens on all interfaces so the published port is
reachable. To add basic auth, set `OPENCODE_SERVER_PASSWORD` (and optionally
`OPENCODE_SERVER_USERNAME`) in the project or user env before starting.

To connect Opencode Desktop to the sandbox:

1. Start the sandbox with `opencode-sandbox run --serve-only`.
2. In Opencode Desktop: **File → Settings → Servers → Add server** (defaults).
3. On the host (as root), symlink the directory you want to work in to
   `/workspace`.
4. **New project** for that server, pointing at `/workspace` — the path
   Opencode Desktop sends must match the in-VM path (`/workspace`).

## Installation

Requires **Linux (KVM) or macOS (Apple Silicon)**.

* Download the latest binary:

  **Linux (x86_64):**

  ```console
  curl -L -o opencode-sandbox https://github.com/inoio/opencode-sandbox/releases/latest/download/opencode-sandbox-linux-amd64
  ```

  **macOS (Apple Silicon):**

  ```console
  curl -L -o opencode-sandbox https://github.com/inoio/opencode-sandbox/releases/latest/download/opencode-sandbox-darwin-arm64
  ```

  **Linux (arm64):**

  ```console
  curl -L -o opencode-sandbox https://github.com/inoio/opencode-sandbox/releases/latest/download/opencode-sandbox-linux-arm64
  ```
* Install:
  ```console
  chmod u+x opencode-sandbox
  mv opencode-sandbox ~/.local/bin # or any other directory in your PATH 
  ```
* Check prerequisites:

   ```console
   opencode-sandbox doctor
   ```

See [Getting Started](/docs/getting-started.md) for prerequisites and a full setup guide.

## Usage

Full [Commands Reference](/docs/commands.md).

> **Breaking change:** the global `-q/--quiet` flag was renamed to `--error`. `sandbox list`
> now supports `--label`, `--limit`, `--running`, `--stopped`, `-q/--quiet` (names only),
> and `--format json`.

opencode is pinned into the runner image at build time and does not auto-update inside sandboxes; rebuild the image
with `opencode-sandbox build` to upgrade (optionally pinning a specific version with `--opencode-version`).

## Documentation

The docs are also published to [GitHub Pages](https://inoio.github.io/opencode-sandbox/).

| Topic                                         | Description                                            |
|-----------------------------------------------|--------------------------------------------------------|
| [Getting Started](/docs/getting-started.md)   | Installation, prerequisites, configuration, first run  |
| [Commands](/docs/commands.md)                 | Complete CLI reference                                 |
| [Configuration](/docs/configuration.md)       | Launcher config, env, secrets, opencode snippet merge, `home.yaml` (incl. startup hooks) |
| [Runner Image](/docs/runner-image.md)         | Base image, custom tooling                             |
| [Worktree Sessions](/docs/branch-sessions.md) | Isolated worktree sessions for per-feature development |
| [Sandboxes](/docs/sandboxes.md)               | VM lifecycle, volumes, pruning                         |
| [Troubleshooting](/docs/troubleshooting.md)   | Common issues and fixes                                |
| [Roadmap](/ROADMAP.md)                        | Public, forward-looking project roadmap                |
