---
title: Home provisioning & startup hooks
description: "Provision files into the sandbox's home directory and run startup hooks when the sandbox starts."
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
| `~/.config/agents-sandbox/config.yaml` (`home:`) | Generic user-level home-file mappings |
| `~/.config/agents-sandbox/<slug>/config.yaml` (`home:`) | Per-slug user-level home-file mappings (repo slug, shared by every clone) |
| `.agents-sandbox/config.yaml` (`home:`)          | Project-level home-file mappings |

Keys (targets) are relative paths within the VM home, e.g. `.config/opencode/opencode.json`. The host source value is
resolved as follows:

- **empty** — read host `$HOME/<target>`
- **`/`-prefixed** — an absolute host path
- **`~/`-prefixed** — host `$HOME/<rest>`
- **otherwise** — relative to the config file that declares it

Layering: the project config overrides the per-slug user config, which overrides the generic user config, **per target**.
Targets must stay within the VM home
(`..` traversal, absolute paths, and `~`-prefixed targets are rejected — targets are already relative to the home
directory, so `~/fdsa` should be written as `fdsa`), and the active agent's merged-config path is reserved (for
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

- Home mappings preserve ordinary Unix permission bits from the host source,
  including executable bits for files that are intended to be run directly in
  the VM.
- `hook: startup` runs the provisioned script after home files are provisioned and before the agent session starts.
  The script is executed directly in the VM, so the VM kernel resolves the interpreter named by its shebang
  (`#!/bin/sh`, `#!/bin/bash`, `#!/usr/bin/env python3`, ...) — including portable `env`-style shebangs and
  interpreter flags (`#!/bin/bash -e`). The launcher marks hook scripts executable during provisioning, so the host
  source does not need the executable bit. A script with **no shebang** falls back to `/bin/sh`. Any other non-empty
  `hook` value is rejected as a parse error.
- The hook runs only when the VM is **started** — freshly created, recreated, or booted from a stopped/crashed state. It
  is **not** re-run when you attach to an already-running VM.
- The hook runs non-interactively: it cannot read from user input (stdin is closed), so it cannot prompt for passwords
  or MFA and never waits on input — startup blocks until the script finishes, and its stdout and stderr stream to the
  session as it runs, so a hook may print unbounded output (progress, build logs, `curl -v`). The launcher waits for
  the hook's process to finish and does not terminate processes the hook leaves behind, so background work a hook
  starts may outlive it until the VM stops, and interrupting the run can leave the hook's process running. A hook that
  must keep running for the VM's lifetime (e.g. a VPN client) or wait for something that starts after the hooks (e.g.
  the agent's daemon) must detach into its own session with
  `setsid -f openfortivpn ... </dev/null >/tmp/vpn.log 2>&1`; it stops when the VM stops. `setsid` is part of
  util-linux, which the managed base provides; a custom base must install it.
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

The hook runs without a terminal, so a VPN that needs a password or MFA must get it from a provisioned file or
environment variable, not from a prompt.

Run `agents-sandbox config home` to list the resolved VM target → host source mappings.
