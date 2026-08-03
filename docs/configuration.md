# Configuration

opencode-msb supports configuration at two levels: user-level defaults and project-level overrides. Both can set CLI flags and environment variables.

## User-level defaults

Place files under `~/.config/opencode-msb/` to set defaults for all projects:

| File                                | Purpose                                                |
|-------------------------------------|--------------------------------------------------------|
| `~/.config/opencode-msb/env`        | Environment variables forwarded to every sandbox       |
| `~/.config/opencode-msb/env.secret` | Secret environment variables (see [Secrets](#secrets)) |
| `~/.config/opencode-msb/config.*`   | Launcher defaults for CLI flags                        |

`env` and `env.secret` use `KEY=value` format (see [Environment Variables](#environment-variables) and [Secrets](#secrets) below).

Supported launcher config filenames: `config.yaml`, `config.yml`, `config.json`, `config.jsonc`, `config.json5`. The first one found is used.

### Launcher config fields

| Field              | CLI flag           | Description                                   |
|--------------------|--------------------|-----------------------------------------------|
| `yes`              | `--yes` / `-y`     | Assume yes to all prompts                     |
| `verbose`          | `--verbose` / `-v` | Show debug-level output                       |
| `quiet`            | `--quiet` / `-q`   | Suppress non-error output                     |
| `rebuild`          | `--rebuild` / `-r` | Rebuild runner image before starting          |
| `cpus`             | `--cpus` / `-c`    | Number of vCPUs for the VM                    |
| `memory`           | `--memory` / `-m`  | Memory limit (e.g. `8G`)                      |
| `tmp-size`         | `--tmp-size`       | Size of `/tmp` tmpfs in the sandbox           |
| `auto-prune-age`   | —                  | Accepted but unused (hardcoded 7-day default) |
| `manual-prune-age` | `--age`            | Default prune age threshold for `prune` cmd   |

Example `~/.config/opencode-msb/config.yaml`:

```yaml
verbose: true
cpus: 4
memory: 8G
auto-prune-age: "24h"           # currently not used
manual-prune-age: "7d"
```

## Project-level configuration

Place files under `.opencode-msb/` in your project directory. These override user-level defaults.

| File                           | Purpose                                           |
|--------------------------------|---------------------------------------------------|
| `.opencode-msb/Dockerfile`     | Custom runner image layers                        |
| `.opencode-msb/env`            | Project-specific environment variables            |
| `.opencode-msb/env.secret`     | Project-specific secrets                          |
| `.opencode-msb/config.*`       | Project-specific launcher defaults                |
| `.opencode-msb/opencode/`      | Project-specific opencode config files            |

Example `.opencode-msb/config.yaml`:

```yaml
memory: 16G
rebuild: true
```

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

Secrets are environment variables whose values are stored host-side only and delivered to the VM via the microsandbox secret mechanism. They never appear in Docker images or environment dumps inside the VM.

### Format

Secret files use `KEY=value@host` format:

```shell
# .opencode-msb/env.secret
GITHUB_TOKEN=ghp_xxxxxxxxxxxx@microsandbox
ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxx@microsandbox
```

The `@host` part is a policy tag that restricts which microsandbox runtime hosts can access the secret. Use `@microsandbox` by default.

Secret files:

- `~/.config/opencode-msb/env.secret` — user-level
- `.opencode-msb/env.secret` — project-level

### Accessing secrets inside the VM

Once set as a secret, the variable is available like any environment variable:

```shell
# Inside the sandbox (shell or opencode)
echo $GITHUB_TOKEN
```

Note: `.envrc` files in the project directory are automatically removed from the VM workspace before opencode runs. Secrets from `.envrc` must be migrated to `.opencode-msb/env.secret`.

## Launcher config

The launcher config file (YAML, JSON, JSONC, or JSON5) sets defaults for CLI flags. It is loaded from the user config directory and project config directory.

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

The `auto-prune-age` and `manual-prune-age` fields accept:

- Go duration: `"7200000000000ns"`, `"2h"`, `"24h"`
- Days shorthand: `"7d"`, `"14d"`

### Validation

The launcher validates:
- `cpus` must be between 0 and 255
- `auto-prune-age` and `manual-prune-age` must be > 0

Invalid config files prevent the launcher from starting.

## Opencode configuration

opencode-msb provisions opencode config into the VM at `/home/dev/.config/opencode/`. The merged config combines:

1. **Embedded provider config** — shipped as part of opencode-msb
2. **User config** — files in `~/.config/opencode-msb/opencode/`
3. **Project config** — files in `.opencode-msb/opencode/`

The user and project config are deep-merged. JSON files are merged by filename; other files (like `settings.json`) are taken from the project directory if present, otherwise the user directory.

See `opencode-msb config show` to inspect the merged config with source paths.
