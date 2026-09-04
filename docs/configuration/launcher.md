---
title: Launcher config file
layout: default
parent: Configuration
nav_order: 10
---

# Launcher config file

opencode-sandbox supports configuration at two levels: user-level defaults and project-level overrides. Both can set CLI
flags and environment variables. This page covers where config lives, the config-file fields, and how values are resolved.

## User-level defaults

Place files under `~/.config/opencode-sandbox/` to set defaults for all projects:

The tool follows
the [XDG base directory spec](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html):
`~/.config/opencode-sandbox`, `~/.cache/opencode-sandbox`, and `~/.local/state/opencode-sandbox` are the defaults, but the
`XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, and `XDG_STATE_HOME` environment variables override them when set (and absolute).

| File                                                       | Purpose                                                                               |
|------------------------------------------------------------|---------------------------------------------------------------------------------------|
| `~/.config/opencode-sandbox/env`                           | Environment variables forwarded to every sandbox                                      |
| `~/.config/opencode-sandbox/env.secret`                    | Secret environment variables (legacy, see [Secrets]({% link configuration/secrets.md %}))                        |
| `~/.config/opencode-sandbox/env.secret.yaml`               | Secret environment variables (YAML/JSON, see [Secrets]({% link configuration/secrets.md %}))                     |
| `~/.config/opencode-sandbox/config.(y[a]ml\|json[(c\|5)])` | Configuration file (incl. the `home:` key; see [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %}))                                                                    |
| `~/.config/opencode-sandbox/<agent>/*`                    | Agent config snippets, one subdir per agent (e.g. `opencode/`; see [Agent configuration]({% link configuration/agent.md %})) |
| `~/.config/opencode-sandbox/home.yaml`                     | User home-file mappings (legacy; see [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %}))                               |
| `~/.config/opencode-sandbox/<slug>/config.(y[a]ml\|json[(c\|5)])` | Per-project (per-slug) configuration file (see [Per-slug configuration](#per-slug-configuration)) |

`env` uses `KEY=value` format. `env.secret` uses `KEY=value@host` (see [Secrets]({% link configuration/secrets.md %})).

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
| `.opencode-sandbox/config.(y[a]ml\|json[(c\|5)])` | Project-specific configuration file (incl. the `home:` key; see [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %}))                                                               |            
| `.opencode-sandbox/<agent>/*`                    | Project-specific agent config snippets (see [Agent configuration]({% link configuration/agent.md %}))          |
| `.opencode-sandbox/home.yaml`                     | Project-specific home-file mappings (legacy; see [Home provisioning & startup hooks]({% link configuration/home-provisioning.md %}))                               |

### Custom base images

A `.opencode-sandbox/Dockerfile` whose `FROM` is a specific image is treated as a **custom base** and the agent (and
optional dind) blocks are layered on top of it. See [Runner Image]({% link runner-image.md %}) for the contract: the base
must provide `curl` and `bash`, the dind prerequisites when dind runs, and idempotency for an existing docker/node/agent.

## Precedence

Configuration is resolved in this order (later entries override earlier ones):

![Configuration precedence diagram]({% link diagrams/config-precedence.svg %})

1. **Built-in / flag defaults** — compiled-in values and CLI flag defaults
2. **User-level** — `~/.config/opencode-sandbox/`
3. **User per-slug** — `~/.config/opencode-sandbox/<slug>/`
4. **Project-level** — `.opencode-sandbox/`
5. **Environment variables** — `OPENCODE_SANDBOX_<KEY>`
6. **CLI flags** — always win when explicitly passed

## Configuration file

| Field                           | Corresponding CLI flag   | Description                                                                                                                                                                                                               |
|---------------------------------|--------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `yes`                           | `--yes` / `-y`           | Assume yes to all prompts                                                                                                                                                                                                 |
| `quiet`                         | `--quiet` / `-q`         | Suppress stdout output                                                                                                                                                                                                    |
| `log-level`                     | `--log-level` / `-l`     | Minimum log level to show on the console: `error`, `warning`, `info`, `verbose` (default `info`)                                                                                                                          |
| `cpus`                          | `--cpus` / `-c`          | Number of vCPUs for the VM                                                                                                                                                                                                |
| `memory`                        | `--memory` / `-m`        | Memory limit (e.g. `8G`)                                                                                                                                                                                                  |
| `disk-size`                     | `--disk-size`            | Project VM root disk size (e.g. `16G`). Empty = microsandbox runtime default (~4 GiB). Applied at VM creation; a change triggers recreation. An invalid value is rejected with an error.                                  |
| `tmp-size`                      | `--tmp-size`             | Size of `/tmp` tmpfs in the sandbox. An invalid value is rejected with an error.                                                                                                                                          |
| `workspace-quota`               | `--workspace-quota`      | Guest-write quota for the `/workspace` bind mount (e.g. `32G`), bounding writes on top of the host repo. Default `16G`. Applied at VM creation; a change triggers recreation. An invalid value is rejected with an error. |
| `auto-prune-age`                | —                        | Auto-prune threshold, runs before every command (default: 30d, only in config). Applies to VMs, volumes, and images alike.                                                                                                |
| `manual-prune-age`              | `--age`                  | Default prune age threshold for `prune`, `image prune`, `volume prune`, and `sandbox prune`                                                                                                                               |
| `auto-stop-on-active-sessions`  | —                        | Stop VM immediately on client detach without waiting for active sessions (default: false, only in config; `busy` sessions are never cut off)                                                                              |
| `auto-stop-timeout`             | —                        | Idle timeout after last client detaches (default: 10s, only in config)                                                                                                                                                    |
| `auto-stop-max-session-retries` | —                        | Retries to tolerate for a session stuck in `retry` before stopping (default: 10, only in config)                                                                                                                          |
| `network.profile`               | `--network`              | Network profile: `public`, `private`, `host`, or `none` (see [Networking]({% link configuration/networking.md %}))                                                                                                                                   |
| `network.egress-allow`          | —                        | Egress destinations to allow: `host`, a CIDR, or a `.suffix` (see [Networking]({% link configuration/networking.md %}))                                                                                                                              |
| `network.egress-deny`           | —                        | Egress carve-outs, emitted before allow rules (see [Networking]({% link configuration/networking.md %}))                                                                                                                                             
| `mounts`                        | —                        | Additional host directories mounted into the VM (see [Host mounts]({% link configuration/mounts.md %}))                                                                                                                               |
| `agent`                         | `--agent`                | Agent profile name to run, build, and provision (default `opencode`, see [Agent configuration]({% link configuration/agent.md %}))                                                                                                        |
| `provision-host-config`         | —                        | Copy the agent's host config + credentials into the VM by default (default: true; set false to opt out, see [Default drop-in provisioning]({% link configuration/agent.md %}#default-drop-in-provisioning))                                               |
| `dind`                          | `--dind`                 | Append the Docker-in-Docker block to the runner image (overridable with `--dind`)                                                                                                                                             |
| `upgrade.mode`                   | —                        | How to handle a newer release when one is found: `prompt`, `notify`, `auto`, or `auto-exit` (default `prompt`, see [Self-upgrade]({% link configuration/self-upgrade.md %}))                                                            |
| `upgrade.interval`               | —                        | How often to check for a newer release (default `1d`, minimum `1h`, see [Self-upgrade]({% link configuration/self-upgrade.md %}))                                                                                                                        |
| `notify.desktop`                 | —                        | Show desktop notifications via `notify-send` (Linux) / `osascript` (macOS) (default false, see [Notifications]({% link configuration/notifications.md %}))                                                                                                |
| `notify.audio`                   | —                        | Audio notification channel: `system`, `bell`, or `off` (default `off`, see [Notifications]({% link configuration/notifications.md %}))                                                                                                                  |
| `notify.on-input`                | —                        | Notify when the agent is waiting on user input (default false)                                                                                                                                                               |
| `notify.on-done`                 | —                        | Notify when a `busy` session returns to `idle` (default false)                                                                                                                                                               |
| `notify.on-error`                | —                        | Notify on a `session.error` event (default false)                                                                                                                                                                            |

Example `~/.config/opencode-sandbox/config.yaml`:

```yaml
log-level: verbose
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
mounts:
  /home/dev/.m2: ~/.m2
notify:
  desktop: true      # notify-send (Linux) / osascript (macOS)
  audio: system      # system | bell | off
  on-input: true     # agent waiting on input
  on-done: true      # busy -> idle
  on-error: true     # session.error
upgrade:
  mode: notify
  interval: "7d"
```

### Duration fields

The `auto-prune-age` field (for `run`/`shell` auto-pruning), `manual-prune-age` field (for `prune`/`image prune`/
`volume prune`/`sandbox prune` default), `auto-stop-timeout` field (for post-detach idle timeout), and `upgrade.interval`
field (for the self-upgrade check) accept:

- Go duration: `"7200000000000ns"`, `"2h"`, `"24h"`
- Days shorthand: `"7d"`, `"14d"`

### Validation

The launcher validates:

- `cpus` must be between 0 and 255
- `upgrade.mode` must be one of `prompt`, `notify`, `auto`, `auto-exit`
- `upgrade.interval` must be at least `1h` (a floor guarding against GitHub rate limits)

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
| `mounts`          | VM recreate    | Host bind mounts are baked in at VM creation.                                                           |

When **no other client** is attached, config changes apply immediately.

Opencode/home config files are provisioned into the VM on every startup, so a change is picked up by the next daemon start
even when the current daemon is kept running (see below). Only the `opencode config` change prompts for a daemon restart;
`home:` file changes are applied on the next startup without any prompt, since they do not require the daemon to restart.

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
| `quiet`                         | `OPENCODE_SANDBOX_QUIET`                                          |
| `log-level`                     | `OPENCODE_SANDBOX_LOG_LEVEL`                                      |
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
| `agent`                         | `OPENCODE_SANDBOX_AGENT`                                          |
| `provision-host-config`         | `OPENCODE_SANDBOX_PROVISION_HOST_CONFIG`                          |
| `dind`                          | `OPENCODE_SANDBOX_DIND`                                           |
| `upgrade.mode`                   | `OPENCODE_SANDBOX_UPGRADE_MODE`                                    |
| `upgrade.interval`               | `OPENCODE_SANDBOX_UPGRADE_INTERVAL`                                |
| `notify` (override)              | `OPENCODE_SANDBOX_NOTIFY`                                          |

Action toggles (`--rebuild`, `--dry-run`, `--force`, ...) are CLI-only and cannot be set via
config file or env var.

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
