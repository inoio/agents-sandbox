---
title: Configuration
layout: default
nav_order: 30
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
| `~/.config/opencode-sandbox/<agent>/*`                    | Agent config snippets, one subdir per agent (e.g. `opencode/`; see [Agent configuration](#agent-configuration)) |
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
| `.opencode-sandbox/<agent>/*`                    | Project-specific agent config snippets (see [Agent configuration](#agent-configuration))          |
| `.opencode-sandbox/home.yaml`                     | Project-specific home-file mappings (see [Home files](#home-files))                               |

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
| `network.profile`               | `--network`              | Network profile: `public`, `private`, `host`, or `none` (see [Networking](#networking))                                                                                                                                   |
| `network.egress-allow`          | —                        | Egress destinations to allow: `host`, a CIDR, or a `.suffix` (see [Networking](#networking))                                                                                                                              |
| `network.egress-deny`           | —                        | Egress carve-outs, emitted before allow rules (see [Networking](#networking))                                                                                                                                             
| `mounts`                        | —                        | Additional host directories mounted into the VM (see [Host bind mounts](#host-bind-mounts))                                                                                                                               |
| `agent`                         | `--agent`                | Agent profile name to run, build, and provision (default `opencode`, see [Agent configuration](#agent-configuration))                                                                                                        |
| `provision-host-config`         | —                        | Copy the agent's host config + credentials into the VM by default (default: true; set false to opt out, see [Default drop-in provisioning](#default-drop-in-provisioning))                                               |
| `dind`                          | `--dind`                 | Append the Docker-in-Docker block to the runner image (overridable with `--dind`)                                                                                                                                             |
| `upgrade.mode`                   | —                        | How to handle a newer release when one is found: `prompt`, `notify`, `auto`, or `auto-exit` (default `prompt`, see [Self-upgrade](#self-upgrade))                                                            |
| `upgrade.interval`               | —                        | How often to check for a newer release (default `1d`, minimum `1h`, see [Self-upgrade](#self-upgrade))                                                                                                                        |
| `notify.desktop`                 | —                        | Show desktop notifications via `notify-send` (Linux) / `osascript` (macOS) (default false, see [Notifications](#notifications))                                                                                                |
| `notify.audio`                   | —                        | Audio notification channel: `system`, `bell`, or `off` (default `off`, see [Notifications](#notifications))                                                                                                                  |
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

## Notifications

opencode-sandbox can alert you when the opencode session status changes (input needed, done, error) via desktop and/or
audio notifications. Only daemon-based agents with an event stream support this; for interactive agents (e.g. `pi`,
`claude-code`) the notify config is ignored.

The `notify:` block controls the channels and the triggers:

| Field             | Type    | Description                                                                                              |
|-------------------|---------|----------------------------------------------------------------------------------------------------------|
| `desktop`         | boolean | Show a desktop notification (`notify-send` on Linux, `osascript` on macOS). Default `false`.             |
| `audio`           | string  | Audio channel: `system` (bundled sound via `afplay`/`paplay`/`pw-play`/`aplay`), `bell` (terminal BEL), or `off`. Default `off`. |
| `on-input`        | boolean | Notify when the agent is waiting on user input. Default `false`.                                         |
| `on-done`         | boolean | Notify when a `busy` session returns to `idle`. Default `false`.                                         |
| `on-error`        | boolean | Notify on a `session.error` event. Default `false`.                                                      |

Notifications are inactive unless at least one channel is enabled (`desktop` true or `audio` not `off`). When inactive,
the `on-input`/`on-done`/`on-error` trigger toggles have no effect.

When multiple clients (`run` instances) are attached to the same project VM,
notifications are delivered at most once per opencode session: the first client
to observe a transition claims a shared per-project token, and other clients
skip it. A later transition for the same session notifies again.

```yaml
notify:
  desktop: true      # notify-send (Linux) / osascript (macOS)
  audio: system      # system | bell | off
  on-input: true     # agent waiting on input
  on-done: true      # busy -> idle
  on-error: true     # session.error
```

The whole thing can be overridden with the `--notify` flag (`on`, `off`, `desktop`, or `audio`; bare `--notify` = `on`)
or the `OPENCODE_SANDBOX_NOTIFY` environment variable. The override sets the **channels only**; it leaves the
`on-input`/`on-done`/`on-error` trigger toggles from the config file unchanged. Precedence: **flag > env > config**.

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

## Host bind mounts

The `mounts` map exposes additional host directories inside the sandbox. Each key is an absolute guest target and its
value is either the host source directory or a mapping with `source` and optional `readonly`. Sources may be absolute or
start with `~/`. Mounts are writable by default; set `readonly: true` when the sandbox must not modify the host directory.

```yaml
mounts:
  /home/dev/.m2: ~/.m2
  /home/dev/reference:
    source: /opt/company/reference
    readonly: true
```

Configured source directories must already exist on the host and must be directories. The managed mount targets
`/home/dev`, `/workspace`, and `/tmp` cannot be replaced, and a target may not shadow a parent of them. Nesting a mount
inside `/workspace` or `/tmp` is rejected because it would hide managed content; nesting inside `/home/dev` is allowed
and is the common case. A mount configuration change recreates the project VM. Writable mounts let sandbox processes
modify host files directly, so only mount directories whose contents may be changed by sandboxed tools.

## Self-upgrade

opencode-sandbox checks GitHub for a newer release when you start `run`/`shell`. The check is throttled to at most once
per `upgrade.interval` (default `1d`, minimum `1h`) and is skipped entirely for local `dev` builds and when a check is
already within the interval. Transient network failures are ignored so an offline start is never blocked. When a newer
release is found, the `upgrade.mode` decides what happens:

| Mode                 | Behavior                                                                                                            |
|----------------------|---------------------------------------------------------------------------------------------------------------------|
| `prompt` (default)   | Ask what to do: continue, don't ask again for this version, upgrade & continue, or upgrade & exit. Falls back to a plain notice when not interactive. |
| `notify`             | Print a notice that a newer release exists; never installs anything.                                                |
| `auto`       | Silently download and replace the binary, then continue running the current version.                                |
| `auto-exit`  | Silently download and replace the binary, then exit so the next invocation uses the new version.                    |

The `upgrade` command (`opencode-sandbox upgrade`) checks for and installs the latest release at any time, independent of
`upgrade.mode`/`upgrade.interval`. Upgrading replaces the running executable with the release binary for your platform
(`opencode-sandbox-<os>-<arch>` from the GitHub release assets); because a running process cannot swap its own binary, an
upgrade (or `auto-exit`) takes effect on the next invocation.

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

## Agent configuration

opencode-sandbox is agent-aware. A `--agent <name>` flag on `run`, `build`, and the `volume` subcommands selects the
coding-agent profile to run, build, and provision. The agent can also be selected via the `agent` config key or the
`OPENCODE_SANDBOX_AGENT` environment variable. Four agents ship as built-in profiles:

- **`opencode`** (default) — daemon-based, with serve/attach, worktree sessions, and GitHub-release upgrade checks.
- **`opencode2`** — opencode 2 (beta), installed from `@opencode-ai/cli@beta` on npm; daemon-based with serve/attach,
  worktree sessions, and npm beta-tag upgrade checks. It shares the `opencode` config directory (v2 reads the same files
  as v1).
- **`pi`** — the pi coding agent, run interactively; upgrade checks via `pi.dev`.
- **`claude-code`** — Anthropic's Claude Code, run interactively; upgrade checks via the npm registry.

`--worktree` and `--serve-only` are rejected for pi and claude-code (they have no daemon); they run through the
interactive TUI instead. Passing an unsupported `--agent` name reports the valid names in its error message.

Each agent owns its config directories, one subdir per agent under the tool's config base:

- **User:** `~/.config/opencode-sandbox/<agent>/` (e.g. `~/.config/opencode-sandbox/opencode/`)
- **Project:** `.opencode-sandbox/<agent>/` (e.g. `.opencode-sandbox/opencode/`)

### Config snippet merge

opencode-sandbox provisions a single agent config into the VM. No embedded provider or permission config is shipped.
Instead, the agent config is assembled from **snippet files** that match the agent's snippet pattern, collected from the
user and project directories, and written to the agent's VM config path:

- **opencode** — snippets match `opencode-*.json*` (e.g. `opencode-model.json`, `opencode-permissions.jsonc`); merged to
  `/home/dev/.config/opencode/opencode.jsonc`. A file named exactly `opencode.json` no longer merges by default.
- **opencode2** — same snippet pattern and merged config path as `opencode` (v2 reads the same files as v1).
- **pi** — snippets match `settings*.json*` in the `pi/` subdir; merged to `/home/dev/.pi/agent/settings.json`.
- **claude-code** — snippets match `settings*.json*` in the `claude/` subdir; merged to `/home/dev/.claude/settings.json`.

Matching files are parsed and **deep-merged** into one config document. The user directory is merged first, then the
project directory; within each directory files are merged in alphabetical order, so later files override earlier ones.
Snippet parsing supports JSON, JSONC, JSON5, and YAML (agents whose pattern includes YAML extensions, e.g. a pattern
like `pi-*.{json,yaml}`). The built-in patterns above match JSON-family extensions.

If no snippet files exist, no merged config is produced.

Run `opencode-sandbox config agent` to print the merged config that would be provisioned into the VM.

> **Note:** the merged config is written to the agent's VM config path (for opencode, `opencode.jsonc`, the last file
> opencode loads: `config.json` < `opencode.json` < `opencode.jsonc`), so it always wins the deep merge. When snippets
> exist, the config-file family (for opencode: `config.json`, `opencode.json`, `opencode.jsonc`, …) is removed from the
> VM so a host drop-in copy of any of those files cannot shadow the merged snippet config.

See the [permissions example]({% link getting-started.md %}#example-permissions) for a concrete snippet.

### Verbatim config directory mirror

Beyond the snippet merge, the `<agent>` config directories (`~/.config/opencode-sandbox/<agent>/` and
`.opencode-sandbox/<agent>/`) act as a **1:1 verbatim mirror** of the agent's VM config directory. Every file and
subdirectory in them is copied verbatim into the VM, preserving its relative path (for opencode: `tui.json`, `AGENTS.md`,
`agents/`, `commands/`, `themes/`, `plugins/`, `skills/`, `tools/`, …). This makes your whole agent setup available in
the VM without extra configuration.

Two families of files are *not* mirrored:

- Files matching the agent's snippet pattern (`opencode-*.json*`, `settings*.json*`) are **deep-merged** (see [Config
  snippet merge](#config-snippet-merge)), never mirrored.
- The agent's config-file family (for opencode: `config.json`, `opencode.json`, `opencode.jsonc`, …) is **reserved for
  the merged output** and never mirrored, so a mirror copy cannot shadow the merged snippet config.

Precedence when the same VM path is reachable from multiple sources:

`home.yaml` > merged snippet config > verbatim mirror > drop-in provisioning

The mirror is **always active**, independent of `provision-host-config`. Stale mirrored files (deleted from the host)
are left in place in the VM. Run `opencode-sandbox config agent` to list the mirror files, each shown as its host source
path → VM path. Previously non-pattern files in `<agent>/` were silently ignored; they are now mirrored verbatim.

### Default drop-in provisioning

Beyond the snippet merge, when running the launcher now **copies the active agent's config + credential files from the
host into the VM by default**, driven by a per-agent gitignore-style include-list manifest (provision rules). This means
your normal agent setup (e.g. an existing opencode config) works without extra configuration.

The drop-in copy is scoped to the agent's settings file, not its runtime state or credentials:

- **opencode** — `~/.config/opencode/**` (excluding `node_modules/`, `package*.json`, and `.gitignore`) plus
  `~/.local/share/opencode/auth.json`.
- **opencode2** — same drop-in copy as `opencode` (`~/.config/opencode/**` and `~/.local/share/opencode/auth.json`).
- **pi** — `~/.pi/agent/settings.json`.
- **claude-code** — `~/.claude/settings.json` (runtime state and the machine-managed `.credentials.json` are not copied).

Precedence: the merged snippet config and any `home.yaml` mappings override the drop-in copy for the same VM path.

When snippets exist, the drop-in copy of the config-file family is skipped entirely (see the note above) so host config
cannot override the merged snippets. Non-config files — e.g. plugins, custom commands, themes — are still copied.

To switch from your agent's own config to `opencode-sandbox/<agent>` snippet provisioning, create snippet files in the
user or project snippet directories (see [Config snippet merge](#config-snippet-merge)). Once a snippet matching the
agent's pattern exists, it wins over the host config for the merged config path. To stop the native-config drop-in
entirely so only the snippet merge and `home.yaml` mappings apply, set `provision-host-config: false` (below).

To turn off the drop-in copy altogether (config **and** credentials), set `provision-host-config: false` in the launcher
config:

```yaml
provision-host-config: false
```

This skips the whole host-config copy (for opencode: `~/.config/opencode/**` and `auth.json`), while the snippet merge
and `home.yaml` mappings keep working. On the next run, previously drop-in-copied config and credential files are removed
from existing home volumes so they cannot linger.

#### Authentication: file copy vs. env-secret

> **Security note:** because of the drop-in provisioning above, the opencode `auth.json` credential file is now copied
> into the VM by default. If you prefer to deliver credentials exclusively through the env-secret mechanism (which never
> writes them into the VM, see [Secrets](#secrets)), you can opt out of the credential file copy. The env-secret channel
> remains fully supported and unchanged; this does not replace it.

To opt out, exclude `auth.json` from the drop-in copy by placing a `home.yaml` entry that overrides the provisioned path
(see [Home files](#home-files)), or remove the credential file from the host before running. The launcher does not inject
host secrets in any other way; the env-secret mechanism is the supported channel for secrets you do not want on disk in
the VM.

For pi and claude-code, the drop-in copy does not include credential files; authenticate them with env secrets instead:

- **pi** — per-provider env vars, e.g. `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` (see pi's docs for the
  full list). Put them in an `env.secret` / `env.secret.yaml` file (below).
- **claude-code** — `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, or `CLAUDE_CODE_OAUTH_TOKEN`. Claude's
  `.credentials.json` is machine-managed and not hand-provisioned, so env vars are the supported channel here.
- **opencode** — `OPENCODE_API_KEY`.

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
directory, so `~/fdsa` should simply be written as `fdsa`), and the active agent's merged-config path is reserved (for
opencode, `.config/opencode/opencode.jsonc`) — it cannot be provisioned via `home.yaml`.

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
