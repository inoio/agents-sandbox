# Launcher config and user-level env files

## Goal

Allow users to define launcher-level defaults and environment variables in both
`~/.config/opencode-msb/` (user-level) and `.opencode-msb/` (project-level),
with the same merge/precedence rules already used for opencode JSON configs.

## Background

Currently the launcher only reads project-level files:

- `.opencode-msb/env` and `.opencode-msb/env.secret` for VM environment/secrets
- `.opencode-msb/opencode/*.json(c)` for opencode config

This design adds user-level equivalents for env files and introduces a new
launcher config file that can override default CLI flag values.

## New files

| Kind            | User-level                          | Project-level            |
|-----------------|-------------------------------------|--------------------------|
| Plain env       | `~/.config/opencode-msb/env`        | `.opencode-msb/env`      |
| Secret env      | `~/.config/opencode-msb/env.secret` | `.opencode-msb/env.secret` |
| Launcher config | `~/.config/opencode-msb/config.*`   | `.opencode-msb/config.*` |

Supported launcher config extensions (first match per directory wins):

- `config.yaml`
- `config.yml`
- `config.json`
- `config.jsonc`
- `config.json5`

The existing opencode config directories (`~/.config/opencode-msb/opencode/` and
`.opencode-msb/opencode/`) remain separate, so launcher files are never merged
into opencode's config.

## Launcher config schema

Flat JSONC/JSON5/YAML object. All keys are optional.

```jsonc
{
  // Global flags
  "yes": false,
  "verbose": false,
  "quiet": false,

  // Run / shell flags
  "cpus": 4,
  "memory": "4G",
  "rebuild": false
}
```

- `cpus`: integer `0..255`. `0` means "use all CPUs" and matches the existing
  CLI default.
- `memory`: string such as `"4G"` or `"512M"`.
- `yes`, `verbose`, `quiet`, `rebuild`: booleans.

## Precedence

1. Built-in defaults.
2. User-level launcher config.
3. Project-level launcher config — overrides user keys.
4. CLI flags — always win.

The same rule applies to `env`/`env.secret`: user-level variables are loaded
first, then project-level variables are merged on top, with project values
winning on key conflicts.

## Architecture

### New package: `internal/launcherconfig`

Responsibilities:

- Locate the first supported launcher config file in a directory.
- Load the user config as a base, then merge the project config on top using
  Viper.
- Pre-process `.jsonc`/`.json5` files with the existing
  `github.com/titanous/json5` library, convert the result to JSON bytes, and
  feed those bytes to Viper as `json`.
- Decode the merged Viper state into a typed `Config` struct.
- Validate `cpus` and return clear errors for malformed files.

```go
type Config struct {
    Yes     bool
    Verbose bool
    Quiet   bool
    CPUs    uint8
    Memory  string
    Rebuild bool
}

func Load(userDir, projectDir string) (Config, error)
```

### CLI wiring: `cmd/opencode-msb/cli.go`

- Extend `sandbox.Config` with a `UserLauncherDir` field set to
  `~/.config/opencode-msb`.
- In the root command's `PersistentPreRunE`, call `launcherconfig.Load` with
  the user and project launcher directories.
- For each supported flag, if the user did not explicitly set it on the CLI,
  apply the launcher-config value to the Cobra flag. This makes the value
  available to `RunE` handlers and to `newLogger` without changing the existing
  flag-reading code.

### Sandbox wiring: `internal/sandbox/runner.go`

- In `createSandbox`, build the env map by merging
  `cfg.UserLauncherDir/env` and `.opencode-msb/env`, project overrides user.
- Build secrets the same way from `cfg.UserLauncherDir/env.secret` and
  `.opencode-msb/env.secret`.
- Pass the merged maps to `msb.WithEnv` and `BuildSecrets`.

## Error handling

- Missing config or env files are silently ignored.
- Malformed launcher config file → hard error that includes the file path and
  parse error.
- Invalid `cpus` value (negative or greater than 255) → hard error.
- `env`/`env.secret` keep the current lenient parsing behavior.

## Out of scope

- JSON Schema for the launcher config. The surface is small enough that
  runtime validation is sufficient for the MVP.
- Deep merging of nested launcher config objects. The schema is flat; nested
  objects are not supported.
- Changing the existing opencode config merger in `internal/config`.

## Testing

- `internal/launcherconfig` unit tests:
  - YAML, JSON, and JSON5 loading.
  - User/project precedence.
  - CLI flag override behavior.
  - Invalid `cpus` and malformed files.
- `internal/sandbox` unit tests:
  - Merging user/project `env` and `env.secret`.
- `cmd/opencode-msb/cli_test.go`:
  - Config-provided defaults are applied.
  - CLI flags override config-provided defaults.

## Migration / user impact

Existing setups that only use `.opencode-msb/env`, `.opencode-msb/env.secret`,
and `.opencode-msb/opencode/*.json(c)` continue to work unchanged. Adding
`~/.config/opencode-msb/env` or `~/.config/opencode-msb/config.yaml` is purely
optional.
