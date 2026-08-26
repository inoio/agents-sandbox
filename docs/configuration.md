---
title: Configuration
layout: default
nav_order: 2
---
# Configuration

opencode-sandbox supports configuration at two levels: user-level defaults and project-level overrides. Both can set CLI
flags and environment variables.

## User-level defaults

Place files under `~/.config/opencode-sandbox/` to set defaults for all projects:

The tool follows
the [XDG base directory spec](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html):
`~/.config/opencode-sandbox`, `~/.cache/opencode-sandbox`, and `~/.local/state/opencode-sandbox` are the defaults, but the
`XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, and `XDG_STATE_HOME` environment variables override them when set (and absolute).

| File                                                       | Purpose                                                                               |
|------------------------------------------------------------|---------------------------------------------------------------------------------------|
| `~/.config/opencode-sandbox/env`                           | Environment variables forwarded to every sandbox                                      |
| `~/.config/opencode-sandbox/env.secret`                    | Secret environment variables (legacy, see [Secrets](#secrets))                        |
| `~/.config/opencode-sandbox/env.secret.yaml`               | Secret environment variables (YAML/JSON, see [Secrets](#secrets))                     |
| `~/.config/opencode-sandbox/config.(y[a]ml\|json[(c\|5)])` | Configuration file                                                                    |
| `~/.config/opencode-sandbox/opencode/*`                    | User opencode config snippets (see [Opencode configuration](#opencode-configuration)) |
| `~/.config/opencode-sandbox/home.yaml`                     | User home-file mappings (see [Home files](#home-files))                               |
| `~/.config/opencode-sandbox/<slug>/config.(y[a]ml\|json[(c\|5)])` | Per-project (per-slug) configuration file (see [Per-slug configuration](#per-slug-configuration)) |

`env` uses `KEY=value` format. `env.secret` uses `KEY=value@host` (see [Secrets](#secrets)).

Supported configuration formats/filenames: YAML (`config.yaml`, `config.yml`), JSON(`config.json`, `config.jsonc`, `config.json5`). The
first one found is used.

## Project-level configuration

Place files under `.opencode-sandbox/` in your project directory. These override user-level defaults.

| File                                              | Purpose                                                                                           |
|---------------------------------------------------|---------------------------------------------------------------------------------------------------|
| `.opencode-sandbox/Dockerfile`                    | Custom runner image layers                                                                        |
| `.opencode-sandbox/env`                           | Project-specific environment variables                                                            |
| `.opencode-sandbox/env.secret`                    | Project-specific secrets (legacy)                                                                 |
| `.opencode-sandbox/env.secret.yaml`               | Project-specific secrets (YAML/JSON)                                                              |
| `.opencode-sandbox/config.(y[a]ml\|json[(c\|5)])` | Project-specific configuration file                                                               |            
| `.opencode-sandbox/opencode/*`                    | Project-specific opencode config snippets (see [Opencode configuration](#opencode-configuration)) |
| `.opencode-sandbox/home.yaml`                     | Project-specific home-file mappings (see [Home files](#home-files))                               |

## Precedence

Configuration is resolved in this order (later entries override earlier ones):

1. **Built-in / flag defaults** — compiled-in values and CLI flag defaults
2. **User-level** — `~/.config/opencode-sandbox/`
3. **User per-slug** — `~/.config/opencode-sandbox/<slug>/`
4. **Project-level** — `.opencode-sandbox/`
5. **Environment variables** — `OPENCODE_SANDBOX_<KEY>`
6. **CLI flags** — always win when explicitly passed

## Configuration file

| Field                           | Corresponding CLI flag | Description                                                                                                                                                                              |
|---------------------------------|------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `yes`                           | `--yes` / `-y`         | Assume yes to all prompts                                                                                                                                                                |
| `verbose`                       | `--verbose` / `-v`     | Show debug-level output                                                                                                                                                                  |
| `error`                         | `--error`             | Only show error output                                                                                                                                                                 |
| `cpus`                          | `--cpus` / `-c`        | Number of vCPUs for the VM                                                                                                                                                               |
| `memory`                        | `--memory` / `-m`      | Memory limit (e.g. `8G`)                                                                                                                                                                 |
| `disk-size`                     | `--disk-size`          | Project VM root disk size (e.g. `16G`). Empty = microsandbox runtime default (~4 GiB). Applied at VM creation; a change triggers recreation. An invalid value is rejected with an error. |
| `tmp-size`                      | `--tmp-size`           | Size of `/tmp` tmpfs in the sandbox. An invalid value is rejected with an error.                                                                                                         |
| `workspace-quota`               | `--workspace-quota`    | Guest-write quota for the `/workspace` bind mount (e.g. `32G`), bounding writes on top of the host repo. Default `16G`. Applied at VM creation; a change triggers recreation. An invalid value is rejected with an error. |
| `auto-prune-age`                | —                      | Auto-prune threshold, runs before every command (default: 30d, only in config). Applies to VMs, volumes, and images alike.                                                               |
| `manual-prune-age`              | `--age`                | Default prune age threshold for `prune`, `image prune`, `volume prune`, and `sandbox prune`                                                                                             |
| `auto-stop-on-active-sessions`  | —                      | Stop VM immediately on client detach without waiting for active sessions (default: false, only in config; `busy` sessions are never cut off)                                             |
| `auto-stop-timeout`             | —                      | Idle timeout after last client detaches (default: 10s, only in config)                                                                                                                   |
| `auto-stop-max-session-retries` | —                      | Retries to tolerate for a session stuck in `retry` before stopping (default: 10, only in config)                                                                                         |
| `network.profile`               | `--network`            | Network profile: `public`, `private`, `host`, or `none` (see [Networking](#networking))                                                                                                  |
| `network.egress-allow`          | —                      | Egress destinations to allow: `host`, a CIDR, or a `.suffix` (see [Networking](#networking))                                                                                             |
| `network.egress-deny`           | —                      | Egress carve-outs, emitted before allow rules (see [Networking](#networking))                                                                                                            |

Example `~/.config/opencode-sandbox/config.yaml`:

```yaml
verbose: true
cpus: 4
memory: 8G
disk-size: 16G
workspace-quota: 32G
auto-prune-age: "7d"
manual-prune-age: "7d"
auto-stop-on-active-sessions: false
auto-stop-timeout: "10s"
auto-stop-max-session-retries: 10
network:
  profile: public
  egress-allow: []
  egress-deny: []
```

### Duration fields

The `auto-prune-age` field (for `run`/`shell` auto-pruning), `manual-prune-age` field (for `prune`/`image prune`/
`volume prune`/`sandbox prune` default), and `auto-stop-timeout` field (for post-detach idle timeout) accept:

- Go duration: `"7200000000000ns"`, `"2h"`, `"24h"`
- Days shorthand: `"7d"`, `"14d"`

### Validation

The launcher validates:

- `cpus` must be between 0 and 255

Invalid config files prevent the launcher from starting.

### Resource Config Application

When a session is started against a VM, resource config changes need to be applied before taking effect in the new
session. The change type determines the mechanism used to apply the new settings:

| Resource          | Change type    | Behavior                                                                                                 |
|-------------------|----------------|----------------------------------------------------------------------------------------------------------|
| `cpus`            | Live Modify    | Applied live via SDK Modify (hotplug)                                                                    |
| `memory`          | Live Modify    | Applied live via SDK Modify (hotplug)                                                                    |
| `env`             | VM recreate    | microsandbox cannot apply env live or on a daemon restart, so the VM is rebuilt; env is baked in at creation |
| `secrets`         | VM recreate    | microsandbox cannot apply secrets live or on a daemon restart, so the VM is rebuilt; secrets are baked in at creation |
| `opencode config` | Daemon restart | Files are always copied into the VM (provisioning); the opencode daemon is restarted in-place to pick them up   |
| `tmp-size`        | VM recreate    | VM is stopped, removed, and rebuilt with new tmpfs size. Home volume is preserved.                       |
| `disk-size`       | VM recreate    | VM is stopped, removed, and rebuilt with new disk size. Home volume is preserved.                        |
| `workspace-quota` | VM recreate    | VM is stopped, removed, and rebuilt with new workspace write quota. Home volume is preserved.            |
| `image`           | VM recreate    | VM is recreated with the new root image. Home volume is preserved.                                       |
| `home volume`     | VM recreate    | After `volume migrate`/`reset`, the new home volume is mounted by recreating the VM; the mount is baked in at creation. |
| `network`         | VM recreate    | Network policy is baked in at VM creation, so a change recreates the VM. Home volume is preserved.        |

When **no other client** is attached, config changes apply immediately.

Opencode/home config files are provisioned into the VM on every startup, so a change is picked up by the next daemon start
even when the current daemon is kept running (see below). Only the `opencode config` change prompts for a daemon restart;
`home.yaml` file changes are applied on the next startup without any prompt, since they do not require the daemon to restart.

#### Parallel Sessions

When multiple `opencode-sandbox` sessions are actively connected to a VM, applying a resource change by recreating the VM
may disrupt active sessions. In this case, the launcher will prompt you whether to keep the current VM (defer),
recreate, or quit to abort the change. The default is to keep/defer.

### Launcher configuration environment variables

Every config-file field above can also be set with an environment variable. Env vars take
precedence over config files but lose to an explicitly passed CLI flag. The prefix is
`OPENCODE_SANDBOX_`; dashes in the field name become underscores.

| Field                           | Environment variable                                              |
|---------------------------------|-------------------------------------------------------------------|
| `yes`                           | `OPENCODE_SANDBOX_YES`                                            |
| `verbose`                       | `OPENCODE_SANDBOX_VERBOSE`                                        |
| `error`                         | `OPENCODE_SANDBOX_ERROR`                                          |
| `cpus`                          | `OPENCODE_SANDBOX_CPUS`                                           |
| `memory`                        | `OPENCODE_SANDBOX_MEMORY`                                         |
| `disk-size`                     | `OPENCODE_SANDBOX_DISK_SIZE`                                      |
| `tmp-size`                      | `OPENCODE_SANDBOX_TMP_SIZE`                                       |
| `workspace-quota`               | `OPENCODE_SANDBOX_WORKSPACE_QUOTA`                                |
| `auto-prune-age`                | `OPENCODE_SANDBOX_AUTO_PRUNE_AGE`                                 |
| `manual-prune-age`              | `OPENCODE_SANDBOX_MANUAL_PRUNE_AGE`                               |
| `auto-stop-on-active-sessions`  | `OPENCODE_SANDBOX_AUTO_STOP_ON_ACTIVE_SESSIONS`                   |
| `auto-stop-timeout`             | `OPENCODE_SANDBOX_AUTO_STOP_TIMEOUT`                              |
| `auto-stop-max-session-retries` | `OPENCODE_SANDBOX_AUTO_STOP_MAX_SESSION_RETRIES`                  |
| `network.profile`               | `OPENCODE_SANDBOX_NETWORK_PROFILE`                                |

Action toggles (`--rebuild`, `--dry-run`, `--force`, ...) are CLI-only and cannot be set via
config file or env var.

## Networking

The `network:` block controls the VM's network policy. It is baked in at VM creation, so changing it recreates the VM
(see [Resource Config Application](#resource-config-application)). When the whole `network:` block is absent, the VM gets
microsandbox's default (public) — no behavior change for existing users.

| Field               | Type     | Description                                                                                                          |
|---------------------|----------|----------------------------------------------------------------------------------------------------------------------|
| `profile`           | string   | `public`, `private`, `host`, or `none`. Defaults to `public` (microsandbox's default) when unset.                    |
| `egress-allow`      | []string | Egress destinations to allow: `host`, a CIDR (e.g. `123.123.0.0/16`), or a `.suffix` (e.g. `.internal`).              |
| `egress-deny`       | []string | Egress carve-outs, same destination forms as `egress-allow`. Emitted **before** allow rules (deny-before-allow).     |

- `profile: none` is an **allowlist-only** profile: egress is deny-by-default, ingress is allowed, and only the
  gateway-DNS rule plus your explicit `egress-allow`/`egress-deny` lists apply. This is how you restrict the VM to a
  specific set of hosts. The `public`/`private`/`host` profiles additionally allow their whole destination class.
- Rule order in the generated firewall: profile rules (including gateway DNS), then `egress-deny`, then `egress-allow`.
  So `egress-allow: [123.123.0.0/16]` together with `egress-deny: [123.123.123.0/24]` denies `123.123.123.5` while
  allowing `123.123.200.5` (a carve-out).

For example, to allow only a single API host:

```yaml
network:
  profile: none
  egress-allow:
    - api.example.com
```

Profile and lists can be combined, e.g. a `private` profile with an `egress-allow: [.internal]` exception.

The profile is also configurable via the `OPENCODE_SANDBOX_NETWORK_PROFILE` environment variable and the `--network`
flag on `run`/`shell` (e.g. `opencode-sandbox run --network none`). Precedence: **flag > env > config > default**. The
`egress-allow`/`egress-deny` lists are config-file-only and have no env var or flag.

```yaml
network:
  profile: public
  egress-allow: []          # host, CIDR, or .suffix
  egress-deny: []           # carve-outs; emitted before allow rules
```

## Per-slug configuration

Beyond the generic user-level config, you can provide config for a **specific project** at
`~/.config/opencode-sandbox/<slug>/config.yaml` (slug = the project's git project slug). This sits between the generic
user-level config and the project-level config in precedence:

1. **Built-in / flag defaults**
2. **User-level** — `~/.config/opencode-sandbox/config.yaml`
3. **User per-slug** — `~/.config/opencode-sandbox/<slug>/config.yaml`
4. **Project-level** — `.opencode-sandbox/config.yaml`
5. **Environment variables** — `OPENCODE_SANDBOX_*`
6. **CLI flags** — always win when explicitly passed

The same formats/filenames as the generic user config are supported (`config.yaml`, `config.yml`, `config.json`,
`config.jsonc`, `config.json5`).

## Environment Variables

Two files define environment variables passed to the sandbox:

- `~/.config/opencode-sandbox/env` — user-level, every project
- `.opencode-sandbox/env` — project-level, current project only

Format: one `KEY=value` per line. Comments (lines starting with `#`) and blank lines are ignored.

```shell
# .opencode-sandbox/env
FOO=bar
DATABASE_URL=postgres://localhost/mydb
```

These are available to opencode and any child processes inside the sandbox.

## Secrets

Secrets are environment variables whose values are stored host-side only and delivered to the VM via the microsandbox
secret mechanism. They never appear in Docker images or environment dumps inside the VM.

### Format

Two file formats are supported: legacy text and structured YAML. YAML files take precedence over legacy files for
the same key.

**Legacy format** — `env.secret`

One `KEY=value@host` per line. The part after the **last** `@` is a policy tag restricting which microsandbox runtime
hosts can access the secret. Values may contain `@` — everything before the last `@` is the value. Each entry must
define a host explicitly; omitting the host part drops the secret with a warning.

```shell
# .opencode-sandbox/env.secret
GITHUB_TOKEN=ghp_xxxxxxxxxxxx@github.com
ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxx@anthropic.com
```

**YAML format** — `env.secret.yaml`

A YAML object map from env-var name to `{ value, host?, hosts?, allow_any_host_dangerous? }`. Values may contain **any
characters** including `@`. `host` and `hosts` are optional when `allow_any_host_dangerous` is set, but otherwise
required — entries with neither hosts nor a dangerous flag are dropped with a warning. JSON is also accepted (YAML 1.2
is a JSON superset).

```yaml
# .opencode-sandbox/env.secret.yaml
GITHUB_TOKEN:
  value: "ghp_xxx@corp"
  host: microsandbox
ANTHROPIC_API_KEY:
  value: sk-ant-xxxxxxxx
  hosts: [gw-a.internal, gw-b.internal]
# No hosts defined — this entry is dropped with a warning
DROPPED_KEY:
  value: secret-value
TRUSTED_KEY:
  value: secret-value
  allow_any_host_dangerous: true
```

An empty `value` is valid and will be passed through unchanged.

**Precedence**

Files are merged from lowest to highest precedence per key, in this order:

1. user-level `env.secret` (legacy `KEY=value@host`)
2. project-level `env.secret` (legacy)
3. user-level `env.secret.yaml`
4. project-level `env.secret.yaml`

A YAML entry always wins over a legacy entry for the same key, even across levels — a user-level
`env.secret.yaml` overrides a project-level `env.secret`. The YAML entry **fully replaces** the legacy entry's hosts —
if a YAML entry omits `host`, `hosts`, and `allow_any_host_dangerous`, the resulting entry has no hosts and is dropped
with a warning.

**Supported files**

- `~/.config/opencode-sandbox/env.secret` — user-level, legacy text format
- `~/.config/opencode-sandbox/env.secret.yaml` — user-level, structured YAML (or JSON)
- `.opencode-sandbox/env.secret` — project-level, legacy text format
- `.opencode-sandbox/env.secret.yaml` — project-level, structured YAML (or JSON)

### Accessing secrets inside the VM

Once set as a secret, the variable is available like any environment variable:

```shell
# Inside the sandbox (shell or opencode)
echo $GITHUB_TOKEN
```

## Opencode configuration

opencode-sandbox provisions a single opencode config into the VM at `/home/dev/.config/opencode/opencode.json`. No embedded
provider or permission config is shipped with opencode-sandbox. Instead, opencode config is assembled from snippet files
under `~/.config/opencode-sandbox/opencode/` (user) and `.opencode-sandbox/opencode/` (project):

- All `.json`, `.jsonc`, and `.json5` files are parsed and **deep-merged** into one `opencode.json`.
- The user directory is merged first, then the project directory; within each directory files are merged in
  alphabetical order, so later files override earlier ones.
- If no snippet files exist, no `opencode.json` is provisioned.

See the [permissions example]({% link getting-started.md %}#example-permissions) for a concrete snippet.

Run `opencode-sandbox config show` to print the merged config that would be provisioned into the VM.

## Home files

In addition to the opencode config, `home.yaml` provisions arbitrary files into the VM home directory (`/home/dev`). A
manifest is an optional YAML map from a **VM-home-relative target path** to a **host source string**:

| Manifest location                      | Purpose                          |
|----------------------------------------|----------------------------------|
| `~/.config/opencode-sandbox/home.yaml` | User-level home-file mappings    |
| `.opencode-sandbox/home.yaml`          | Project-level home-file mappings |

Keys (targets) are relative paths within the VM home, e.g. `.config/opencode/opencode.json`. The host source value is
resolved as follows:

- **empty** — read host `$HOME/<target>`
- **`/`-prefixed** — an absolute host path
- **`~/`-prefixed** — host `$HOME/<rest>`
- **otherwise** — relative to the manifest file that declares it

Layering: the project manifest overrides the user manifest **per target**. Targets must stay within the VM home
(`..` traversal, absolute paths, and `~`-prefixed targets are rejected — targets are already relative to the home
directory, so `~/fdsa` should simply be written as `fdsa`), and `.config/opencode/opencode.json` is reserved for the
merged opencode config — it cannot be provisioned via `home.yaml`.

Example `.opencode-sandbox/home.yaml`:

```yaml
# Relative source resolves against .opencode-sandbox/
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

````yaml
# provision AND run at startup, as root
.vpn/connect.sh:
  source: vpn/connect.sh   # resolved exactly like the plain string form
  hook: startup            # optional; the only supported value is `startup`
  root: true               # optional; true runs as root, the default (dev) otherwise
````

Rules:

- `hook: startup` runs the provisioned script after home files are provisioned and before the opencode server daemon
  starts, using the interpreter named by the script's shebang (`#!/bin/sh`, `#!/bin/bash`, `#!/usr/bin/env python3`, ...).
  A script with **no shebang** falls back to `/bin/sh`. Any other non-empty `hook` value is rejected as a parse error.
- The hook runs only when the VM is **started** — freshly created, recreated, or booted from a stopped/crashed state. It
  is **not** re-run when you attach to an already-running VM.
- The hook runs interactively: it may read from user input (e.g. prompt for passwords or MFA), and opencode blocks
  startup until the script finishes. A hook that must keep running for the VM's lifetime (e.g. a VPN client) must
  daemonize itself (e.g. `nohup openfortivpn ... &`) so it survives the attach; it stops when the VM stops.
- The script runs as the sandbox user (`dev`) by default; set `root: true` to run it as root.

Example: bring up a VPN with a vpn client (installed via your `.opencode-sandbox/Dockerfile`), with its
config (host, port, username, trusted cert) provisioned as a plain entry:

````yaml
.vpn/connect.sh:
  source: vpn/connect.sh
  hook: startup
  root: true
.vpn/config: .vpn/config
````

Any credentials (passwords, MFA) the VPN needs should be interactively read from user input by the script.

Run `opencode-sandbox config home` to list the resolved VM target → host source mappings.
