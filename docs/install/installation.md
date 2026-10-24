---
title: Installation
description: "Install agents-sandbox on Linux or macOS, then verify the host is ready to run your first sandbox."
layout: default
parent: Get started
nav_order: 20
---

# Installation

Install agents-sandbox via binary download or Homebrew.

## System requirements

agents-sandbox runs the agent inside a virtual machine, so the host must provide virtualization and a Docker daemon that
builds the sandbox image:

| Platform | Requirement                                                     |
|----------|-----------------------------------------------------------------|
| Linux    | KVM — `/dev/kvm` must exist, and your user must be in the `kvm` group |
| macOS    | Apple Silicon (arm64)                                           |

Both platforms need **Docker**, **Docker Desktop**, or **colima**. The launcher finds the Docker endpoint the same way
the `docker` CLI does: `DOCKER_HOST` first, then the active docker context (`docker context ls`), then the default
`/var/run/docker.sock`. A colima or Docker Desktop install that publishes its socket through a docker context needs no
extra configuration.

## Binary download

  **Linux (x86_64):**

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-linux-amd64
  ```

  **macOS (Apple Silicon):**

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-darwin-arm64
  ```

  **Linux (arm64):**

  ```console
  curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-linux-arm64
  ```

  To install, set permissions and move it onto your path:

  ```console
  chmod u+x agents-sandbox
  mv agents-sandbox ~/.local/bin # or any other directory in your PATH 
  ```

## Via Homebrew (macOS and Linux)

  ```console
  brew tap inoio/agents-sandbox https://github.com/inoio/agents-sandbox
  brew install agents-sandbox
  ```

Updates ship as ordinary releases; to get one:

  ```console
  brew update && brew upgrade agents-sandbox
  ```

Homebrew installs are detected automatically: the launcher disables its
[self-upgrade]({% link configuration/self-upgrade.md %}) so Homebrew's version bookkeeping stays accurate, and
`agents-sandbox upgrade` points you back to `brew upgrade`.

## Download canaries (pre-releases)

Canaries are not available via Homebrew, only as binary downloads.

Canary builds are GitHub prereleases, so they are not served by `/latest`. Download the asset from that prerelease's
tag URL instead, replacing `CANARY_VERSION` and the `<os>-<arch>` suffix with your values (`linux-amd64`, `darwin-arm64`,
or `linux-arm64`):

**Linux (x86_64):**

```console
CANARY_VERSION=v0.6.0-canary.1
curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/download/${CANARY_VERSION}/agents-sandbox-linux-amd64
```

**Linux (arm64):**

```console
CANARY_VERSION=v0.6.0-canary.1
curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/download/${CANARY_VERSION}/agents-sandbox-linux-arm64
```

**macOS (Apple Silicon):**

```console
CANARY_VERSION=v0.6.0-canary.1
curl -L -o agents-sandbox https://github.com/inoio/agents-sandbox/releases/download/${CANARY_VERSION}/agents-sandbox-darwin-arm64
```

To install, set permissions and move it onto your path:

```console
chmod u+x agents-sandbox
mv agents-sandbox ~/.local/bin # or any other directory in your PATH
```

Then set `upgrade.channel: canary` (or `AGENTS_SANDBOX_UPGRADE_CHANNEL=canary`) so later upgrades follow the canary line — see [self-upgrade]({% link configuration/self-upgrade.md %}).

## Verify your setup

`run` and `shell` check the host automatically before they start a sandbox. A failing check prints the
command that fixes it and links to the matching [troubleshooting]({% link troubleshooting.md %}) section, so you do not
have to check the host by hand.

To check it on its own, run:

```console
agents-sandbox doctor
```

Once installed, continue with one of the two onboarding paths:

- [Switch from your existing agent]({% link switch.md %}) — bring your existing agent config and credentials into the sandbox.
- [Your first sandbox]({% link tutorial.md %}) — run an agent in a sandbox for the first time.
