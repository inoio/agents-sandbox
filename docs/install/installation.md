---
title: Installation
description: "Install agents-sandbox on Linux or macOS, then verify the host is ready to run your first sandbox."
layout: default
parent: Get started
nav_order: 20
---

# Installation

Install agents-sandbox via binary download or Homebrew.

## Prerequisites

agents-sandbox verifies the host for you when you run it: before a sandbox starts, it checks the prerequisites and, if
one is missing, prints information and links to the matching [troubleshooting]({% link troubleshooting.md %}) section.

To run that check on its own, after installing agents-sandbox, run:

```console
agents-sandbox doctor
```

| Platform | Requirement                                                     |
|----------|-----------------------------------------------------------------|
| Linux    | KVM — `/dev/kvm` exists, and your user is in the `kvm` group     |
| macOS    | Apple Silicon (arm64)                                           |

Both platforms also need a container engine that serves the Docker API to build the sandbox image. **Docker Engine**,
**Docker Desktop**, **colima**, and **podman** are the tested engines; others (Rancher Desktop, OrbStack, ...) are
expected to work, but untested. [Troubleshooting]({% link troubleshooting.md %}) covers starting an engine and pointing
agents-sandbox at its socket.

## Binary download

Download and install the release for your platform:

```console
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64)   platform=linux-amd64 ;;
  Linux/aarch64)  platform=linux-arm64 ;;
  Darwin/*)       platform=darwin-arm64 ;; # only Apple Silicon is supported
  *) echo "unsupported platform: $(uname -s) $(uname -m)" >&2; exit 1 ;;
esac
curl -L -o agents-sandbox "https://github.com/inoio/agents-sandbox/releases/latest/download/agents-sandbox-${platform}"
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
tag URL instead, replacing `CANARY_VERSION` with the canary you want:

```console
CANARY_VERSION=v0.6.0-canary.1
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64)   platform=linux-amd64 ;;
  Linux/aarch64)  platform=linux-arm64 ;;
  Darwin/*)       platform=darwin-arm64 ;; # only Apple Silicon is supported
  *) echo "unsupported platform: $(uname -s) $(uname -m)" >&2; exit 1 ;;
esac
curl -L -o agents-sandbox "https://github.com/inoio/agents-sandbox/releases/download/${CANARY_VERSION}/agents-sandbox-${platform}"
```

To install, set permissions and move it onto your path:

```console
chmod u+x agents-sandbox
mv agents-sandbox ~/.local/bin # or any other directory in your PATH
```

Then set `upgrade.channel: canary` (or `AGENTS_SANDBOX_UPGRADE_CHANNEL=canary`) so later upgrades follow the canary line — see [self-upgrade]({% link configuration/self-upgrade.md %}).

## Next steps

Verify the host setup, then start agents-sandbox:

```console
agents-sandbox doctor
agents-sandbox
```

If the selected agent already has native configuration, the first interactive start offers a safe migration before the VM starts.
See [Manage config in the sandbox]({% link manage-config.md %}) for migration, manual configuration, secrets, and file provisioning.

Once installed, continue with one of the two onboarding paths:

- [Switch from your existing agent]({% link switch.md %}) — bring your existing agent config and credentials into the sandbox.
- [Your first sandbox]({% link tutorial.md %}) — run an agent in a sandbox for the first time.
