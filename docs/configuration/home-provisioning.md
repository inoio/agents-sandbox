---
title: Home provisioning & startup hooks
layout: default
parent: Configuration
nav_order: 50
---

# Home provisioning & startup hooks

In addition to the agent config, the `home:` key provisions arbitrary files into the VM home directory (`/home/dev`). It
is an optional YAML map from a **VM-home-relative target path** to a **host source string**, nested under the `home:` key
in a config file:

| Config location                        | Purpose                          |
|----------------------------------------|----------------------------------|
| `~/.config/agents-sandbox/config.yaml` (`home:`) | User-level home-file mappings    |
| `.agents-sandbox/config.yaml` (`home:`)          | Project-level home-file mappings |

Keys (targets) are relative paths within the VM home, e.g. `.config/opencode/opencode.json`. The host source value is
resolved as follows:

- **empty** — read host `$HOME/<target>`
- **`/`-prefixed** — an absolute host path
- **`~/`-prefixed** — host `$HOME/<rest>`
- **otherwise** — relative to the config file that declares it

Layering: the project config overrides the user config **per target**. Targets must stay within the VM home
(`..` traversal, absolute paths, and `~`-prefixed targets are rejected — targets are already relative to the home
directory, so `~/fdsa` should simply be written as `fdsa`), and the active agent's merged-config path is reserved (for
opencode, `.config/opencode/opencode.jsonc`) — it cannot be provisioned via `home:`.

Example project-level `home:` block in `.agents-sandbox/config.yaml`:

```yaml
home:
  # Relative source resolves against .agents-sandbox/
  .ssh/config: ssh_config
  # Absolute host path
  .config/tooling/rc: /abs/path/to/rc
  # Host $HOME
  .gitconfig: ~/.gitconfig
  # Empty source reads host $HOME/.inputrc
  .inputrc:
```

In addition to the plain string form, a value may be a mapping that provisions the file and optionally runs it at VM
startup as a startup hook:

```yaml
home:
  # provision AND run at startup, as root
  .vpn/connect.sh:
    source: vpn/connect.sh   # resolved exactly like the plain string form
    hook: startup            # optional; the only supported value is `startup`
    root: true               # optional; true runs as root, the default (dev) otherwise
```

Rules:

- `hook: startup` runs the provisioned script after home files are provisioned and before the agent session starts,
  using the interpreter named by the script's shebang (`#!/bin/sh`, `#!/bin/bash`, `#!/usr/bin/env python3`, ...).
  A script with **no shebang** falls back to `/bin/sh`. Any other non-empty `hook` value is rejected as a parse error.
- The hook runs only when the VM is **started** — freshly created, recreated, or booted from a stopped/crashed state. It
  is **not** re-run when you attach to an already-running VM.
- The hook runs interactively: it may read from user input (e.g. prompt for passwords or MFA), and startup blocks
  until the script finishes. A hook that must keep running for the VM's lifetime (e.g. a VPN client) must
  daemonize itself (e.g. `nohup openfortivpn ... &`) so it survives the attach; it stops when the VM stops.
- The script runs as the sandbox user (`dev`) by default; set `root: true` to run it as root.

Example: bring up a VPN with a vpn client (installed via your `.agents-sandbox/Dockerfile`), with its
config (host, port, username, trusted cert) provisioned as a plain entry:

```yaml
home:
  .vpn/connect.sh:
    source: vpn/connect.sh
    hook: startup
    root: true
  .vpn/config: .vpn/config
```

Any credentials (passwords, MFA) the VPN needs should be interactively read from user input by the script.

Run `agents-sandbox config home` to list the resolved VM target → host source mappings.
