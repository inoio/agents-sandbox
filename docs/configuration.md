# Configuration

opencode-msb supports configuration at two levels: user-level defaults and project-level overrides. Both can set CLI
flags and environment variables.

## User-level defaults

Place files under `~/.config/opencode-msb/` to set defaults for all projects:

The tool follows
the [XDG base directory spec](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html):
`~/.config/opencode-msb`, `~/.cache/opencode-msb`, and `~/.local/state/opencode-msb` are the defaults, but the
`XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, and `XDG_STATE_HOME` environment variables override them when set (and absolute).

| File                                                   | Purpose                                                |
|--------------------------------------------------------|--------------------------------------------------------|
| `~/.config/opencode-msb/env`                           | Environment variables forwarded to every sandbox       |
| `~/.config/opencode-msb/env.secret`                    | Secret environment variables (legacy, see [Secrets](#secrets)) |
| `~/.config/opencode-msb/env.secret.yaml`               | Secret environment variables (YAML/JSON, see [Secrets](#secrets)) |
| `~/.config/opencode-msb/config.(y[a]ml\|json[(c\|5)])` | Launcher defaults for CLI flags                        |

`env` uses `KEY=value` format. `env.secret` uses `KEY=value@host` (see [Secrets](#secrets)).

Supported launcher config filenames: `config.yaml`, `config.yml`, `config.json`, `config.jsonc`, `config.json5`. The
first one found is used.

### Launcher config fields

| Field                           | Corresponding CLI flag | Description                                                                                                                                  |
|---------------------------------|------------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `yes`                           | `--yes` / `-y`         | Assume yes to all prompts                                                                                                                    |
| `verbose`                       | `--verbose` / `-v`     | Show debug-level output                                                                                                                      |
| `quiet`                         | `--quiet` / `-q`       | Suppress non-error output                                                                                                                    |
| `rebuild`                       | `--rebuild` / `-r`     | Rebuild runner image before starting                                                                                                         |
| `cpus`                          | `--cpus` / `-c`        | Number of vCPUs for the VM                                                                                                                   |
| `memory`                        | `--memory` / `-m`      | Memory limit (e.g. `8G`)                                                                                                                     |
| `disk-size`                     | `--disk-size`          | Project VM root disk size (e.g. `16G`). Empty = microsandbox runtime default (~4 GiB). Applied at VM creation; a change triggers recreation. |
| `tmp-size`                      | `--tmp-size`           | Size of `/tmp` tmpfs in the sandbox                                                                                                          |
| `auto-prune-age`                | —                      | Auto-prune threshold, runs before every command (default: 30d, only in config)                                                               |
| `manual-prune-age`              | `--age`                | Default prune age threshold for `prune` cmd                                                                                                  |
| `auto-stop-on-active-sessions`  | —                      | Stop VM immediately on client detach without waiting for active sessions (default: false, only in config; `busy` sessions are never cut off) |
| `auto-stop-timeout`             | —                      | Idle timeout after last client detaches (default: 10s, only in config)                                                                       |
| `auto-stop-max-session-retries` | —                      | Retries to tolerate for a session stuck in `retry` before stopping (default: 10, only in config)                                             |

Example `~/.config/opencode-msb/config.yaml`:

```yaml
verbose: true
cpus: 4
memory: 8G
disk-size: 16G
auto-prune-age: "7d"
manual-prune-age: "7d"
auto-stop-on-active-sessions: false
auto-stop-timeout: "10s"
auto-stop-max-session-retries: 10
```

## Project-level configuration

Place files under `.opencode-msb/` in your project directory. These override user-level defaults.

| File                                          | Purpose                                |
|-----------------------------------------------|----------------------------------------|
| `.opencode-msb/Dockerfile`                    | Custom runner image layers             |
| `.opencode-msb/env`                           | Project-specific environment variables |
| `.opencode-msb/env.secret`                    | Project-specific secrets (legacy)      |
| `.opencode-msb/env.secret.yaml`               | Project-specific secrets (YAML/JSON)   |
| `.opencode-msb/config.(y[a]ml\|json[(c\|5)])` | Project-specific launcher defaults     |
| `.opencode-msb/opencode/*`                    | Project-specific opencode config files |

## Precedence

Configuration is resolved in this order (later entries override earlier ones):

1. **Built-in defaults** — compiled-in values
2. **User-level** — `~/.config/opencode-msb/`
3. **Project-level** — `.opencode-msb/`
4. **CLI flags** — always win

## Environment Variables

Two files define environment variables passed to the sandbox:

- `~/.config/opencode-msb/env` — user-level, every project
- `.opencode-msb/env` — project-level, current project only

Format: one `KEY=value` per line. Comments (lines starting with `#`) and blank lines are ignored.

```shell
# .opencode-msb/env
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
# .opencode-msb/env.secret
GITHUB_TOKEN=ghp_xxxxxxxxxxxx@github.com
ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxx@anthropic.com
```

**YAML format** — `env.secret.yaml`

A YAML object map from env-var name to `{ value, host?, hosts?, allow_any_host_dangerous? }`. Values may contain **any
characters** including `@`. `host` and `hosts` are optional when `allow_any_host_dangerous` is set, but otherwise
required — entries with neither hosts nor a dangerous flag are dropped with a warning. JSON is also accepted (YAML 1.2
is a JSON superset).

```yaml
# .opencode-msb/env.secret.yaml
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

- `~/.config/opencode-msb/env.secret` — user-level, legacy text format
- `~/.config/opencode-msb/env.secret.yaml` — user-level, structured YAML (or JSON)
- `.opencode-msb/env.secret` — project-level, legacy text format
- `.opencode-msb/env.secret.yaml` — project-level, structured YAML (or JSON)

### Accessing secrets inside the VM

Once set as a secret, the variable is available like any environment variable:

```shell
# Inside the sandbox (shell or opencode)
echo $GITHUB_TOKEN
```

Note: `.envrc` files in the project directory are denied by opencode's provider config and will not be processed or
surfaced. Secrets from `.envrc` must be migrated to `.opencode-msb/env.secret`.

## Launcher config

The launcher config file (YAML, JSON, JSONC, or JSON5) sets defaults for CLI flags. It is loaded from the user config
directory and project config directory.

### Supported formats

All five formats are supported. The first one found in the directory wins:

```
config.yaml    # preferred
config.yml
config.json
config.jsonc
config.json5
```

### Duration fields

The `auto-prune-age` field (for `run`/`shell` auto-pruning), `manual-prune-age` field (for `prune` default), and
`auto-stop-timeout` field (for post-detach idle timeout) accept:

- Go duration: `"7200000000000ns"`, `"2h"`, `"24h"`
- Days shorthand: `"7d"`, `"14d"`

### Validation

The launcher validates:

- `cpus` must be between 0 and 255

Invalid config files prevent the launcher from starting.

## Resource Config Application

When a session is started against a VM, resource config changes need to be applied before taking effect in the new
session. The change type determines the mechanism used to apply the new settings:

| Resource          | Change type    | Behavior                                                                                                 |
|-------------------|----------------|----------------------------------------------------------------------------------------------------------|
| `cpus`            | Live Modify    | Applied live via SDK Modify (hotplug)                                                                    |
| `memory`          | Live Modify    | Applied live via SDK Modify (hotplug)                                                                    |
| `env`             | VM recreate    | microsandbox cannot apply env live or on a daemon restart, so the VM is rebuilt; env is baked in at creation |
| `secrets`         | VM recreate    | microsandbox cannot apply secrets live or on a daemon restart, so the VM is rebuilt; secrets are baked in at creation |
| `opencode config` | Daemon restart | Applied live via copy commands; opencode daemon is restarted in-place, picking up the changes            |
| `tmp-size`        | VM recreate    | VM is stopped, removed, and rebuilt with new tmpfs size. Home volume is preserved.                       |
| `disk-size`       | VM recreate    | VM is stopped, removed, and rebuilt with new disk size. Home volume is preserved.                        |
| `image`           | VM recreate    | VM is recreated with the new root image. Home volume is preserved.                                       |

When **no other client** is attached, config changes apply immediately.

### Parallel Sessions

When multiple `opencode-msb` sessions are actively connected to a VM, applying a resource change by recreating the VM
may disrupt active sessions. In this case, the launcher will prompt you whether to keep the current VM (defer) or
recreate. The default is to keep/defer.

## Opencode configuration

opencode-msb provisions opencode config into the VM at `/home/dev/.config/opencode/`. The merged config combines:

1. **Embedded provider config** — shipped as part of opencode-msb
2. **User config** — files in `~/.config/opencode-msb/opencode/`
3. **Project config** — files in `.opencode-msb/opencode/`

The user and project config are deep-merged by filename.

See `opencode-msb config show` to inspect the merged config with source paths.
