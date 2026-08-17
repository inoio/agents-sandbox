# Cobra/Viper env + config integration

## Problem

`opencode-sandbox` reads launcher defaults from config files
(`~/.config/opencode-sandbox/config.*` and `.opencode-sandbox/config.*`) and applies them onto
CLI flags with a hand-rolled mechanism in `applyLauncherConfig` and the `setBoolFlag` /
`setUint8Flag` / `setStringFlag` / `setDurationFlag` helpers
(`cmd/opencode-sandbox/cli.go`). This has several consequences:

- **No environment-variable support.** A user cannot set `cpus`, `memory`, `verbose`, etc.
  via an env var; only config files work. Config files and env vars are two carriers for the
  same configuration schema, and a 12-factor launcher should support both with a single,
  consistent precedence.
- **Duplicated precedence logic.** `applyLauncherConfig` re-implements "CLI flag wins if
  explicitly set, otherwise fall back to config" by checking `flag.Changed` and calling
  `flag.Value.Set(...)`. Viper's `BindPFlag` provides this precedence natively.
- **`rebuild` is misclassified** as a durable launcher default even though it is a
  per-command one-shot action toggle (`force a clean rebuild`), not a durable setting.

This change wires cobra + viper together the way the Cobra "working with flags" guide
describes: bind flags to viper, enable env vars, and let viper resolve the final value with
the precedence `flag > env > config > default`. It also splits config-backed settings from
per-command action toggles.

## Goals

- Support env vars for launcher config settings via the `OPENCODE_SANDBOX_` prefix, with a
  single, well-defined precedence: `flag > env > config file > default`.
- Make viper the single resolver for config-backed values, removing the hand-rolled
  `applyLauncherConfig` / `setXFlag` / `keys`-map logic.
- Split durable defaults (config + env backed) from per-command action toggles (CLI-only).
- Preserve the exact current precedence behavior for config files and CLI flags.

## Non-goals

- Supporting a second env prefix (e.g. `OCS_`). A single `OPENCODE_SANDBOX_` prefix keeps the
  implementation simple and the precedence unambiguous.
- Binding per-command action toggles (rebuild, dry-run, force, ...) to env or config.
- Changing the config-file format, locations, or validation rules.
- Changing the sandbox-internal env file mechanism (`~/.config/opencode-sandbox/env`); that is
  a separate concern from launcher configuration.

## Inclusion / exclusion

The rule: **env + config binding applies to durable defaults with a single global meaning.
Per-command one-shot action toggles and per-command operational flags stay CLI-only.**

### Include (env + config backed)

| Key                | Type     | Env var                                  |
|--------------------|----------|------------------------------------------|
| `cpus`             | uint8    | `OPENCODE_SANDBOX_CPUS`                  |
| `memory`           | string   | `OPENCODE_SANDBOX_MEMORY`                |
| `tmp-size`         | string   | `OPENCODE_SANDBOX_TMP_SIZE`              |
| `disk-size`        | string   | `OPENCODE_SANDBOX_DISK_SIZE`             |
| `yes`              | bool     | `OPENCODE_SANDBOX_YES`                   |
| `verbose`          | bool     | `OPENCODE_SANDBOX_VERBOSE`               |
| `quiet`            | bool     | `OPENCODE_SANDBOX_QUIET`                 |
| `auto-prune-age`   | duration | `OPENCODE_SANDBOX_AUTO_PRUNE_AGE`        |
| `manual-prune-age` | duration | `OPENCODE_SANDBOX_MANUAL_PRUNE_AGE`      |
| `auto-stop-on-active-sessions`  | bool     | `OPENCODE_SANDBOX_AUTO_STOP_ON_ACTIVE_SESSIONS`  |
| `auto-stop-timeout`            | duration | `OPENCODE_SANDBOX_AUTO_STOP_TIMEOUT`            |
| `auto-stop-max-session-retries` | int      | `OPENCODE_SANDBOX_AUTO_STOP_MAX_SESSION_RETRIES` |

### Exclude (CLI-only action toggles / per-command ops)

- `rebuild` — removed from the config schema; a per-command one-shot action toggle.
- `dry-run`, `dry-run-vm` — one-shot action toggles.
- `force`, `remove` — per-command destructive ops.
- `worktree`, `serve-only`, `root` — session-scoped, not global defaults.
- `age` (on `prune`) — per-invocation override of `manual-prune-age`, already config-backed.
- `opencode-version` (on `build`) — one-shot pin.

## Precedence

Viper's built-in resolution (`find`) provides exactly the required precedence:

1. **Bound flag** — honored only when `flag.HasChanged()` is true (viper.go
   `pflag` check). An unspecified flag with a default does **not** override env/config.
2. **Environment** — `OPENCODE_SANDBOX_<KEY>`.
3. **Config file** — user-level, then project-level (project overrides user, as today).
4. **Default** — registered via viper `SetDefault`.

Note: `BindPFlag` does not propagate the flag's default into viper; defaults are only read
from viper's `defaults` map (`SetDefault`). Therefore each bound flag's default is mirrored
into viper via `SetDefault` in `PersistentPreRunE`. The flag definition remains the single
source of truth for the default value (used for help/usage text); viper reads it at the
lowest-precedence stage, so config/env still win over an unspecified flag's default.

## Design

### `internal/viperconfig` — the resolver

Replace the current `Load() (Config, map[string]bool, error)` entry point with a typed
resolver that encapsulates a `*viper.Viper` and exposes config-backed getters. The resolver:

- Reads config files with the existing `mergeDir` / `findConfigFile` logic (unchanged).
- Configures env support: `SetEnvPrefix("OPENCODE_SANDBOX")`,
  `SetEnvKeyReplacer(strings.NewReplacer("-", "_"))`, `AutomaticEnv()`, and binds each
  env-backed key explicitly via `BindEnv(key, "OPENCODE_SANDBOX_<KEY>")`.
- Binds config-backed flags: for each included key, locate the corresponding flag on the
  executed command (walking `cmd.Flags()` then `cmd.InheritedFlags()`) and call
  `BindPFlag(key, flag)`; register the flag's default via `SetDefault(key, <default>)`.
- Runs the existing `validate` after env + config merge, so a bad `cpus` from env is caught
  the same as one from a config file. Because validation runs at construction time (in
  `PersistentPreRunE`), an invalid value from any source fails startup before any getter is
  called. The typed getters are therefore plain (no error return).
- Keeps the `Config` struct + `durationDecodeHook` + `ParseHumanDuration` (still used by
  `prune --age` and the reap policy / idle timeout).

Typed getters (config-backed):

```go
type Resolver interface {
    CPUs() uint8
    Memory() string
    TmpSize() string
    DiskSize() string
    Yes() bool
    Verbose() bool
    Quiet() bool
    AutoPruneAge() time.Duration
    ManualPruneAge() time.Duration
    AutoStopOnActiveSessions() bool
    AutoStopTimeout() time.Duration
    AutoStopMaxSessionRetries() int
    IdleTimeout() time.Duration
}
```

### `cmd/opencode-sandbox` — wiring

- **Delete** `applyLauncherConfig` and the `setBoolFlag` / `setUint8Flag` / `setStringFlag` /
  `setDurationFlag` helpers (`cli.go`), and the `keys` map returned by `Load`.
- **`PersistentPreRunE`** (`commands.go:109`) constructs the resolver (passing the executed
  command so flags can be bound) and stores it on the command context, replacing the current
  `launcherConfigKey`-carried `viperconfig.Config`. Keeps calling `applyCLISettings` and
  `pruning.AutoPrune`.
- **`applyCLISettings`** (`cli.go:50`) reads `yes` / `verbose` / `quiet` from the resolver
  instead of `cmd.Flags().GetBool`.
- **`extractRunOptions`** (`commands.go:23`) reads `cpus`, `memory`, `tmp-size`, `disk-size`
  from the resolver instead of `cmd.Flags()`; the reap policy and idle timeout come from the
  resolver too. `worktree`, `dry-run`, `dry-run-vm`, `serve-only`, `root`, `rebuild` stay on
  `cmd.Flags()`.
- **`buildPruneCmd`** (`commands_system.go:304`) falls back to the resolver's
  `ManualPruneAge()` when `--age` is unset, replacing the current config-key read.
- **`buildVolumeOpsCmd` / `buildBuildCmd` / stop / kill / config / list / doctor / version /
  tree** — no config-backed values; unchanged. Their `rebuild`, `dry-run`, `force`,
  `opencode-version`, `remove` flags stay on `cmd.Flags()`.

### `rebuild` removal

Remove the `rebuild` field from `viperconfig.Config` and from `applyLauncherConfig`. The
`rebuild` flag remains defined on the commands that use it (`run`/`shell`, `image build`,
`volume migrate`/`reset`) as a CLI-only toggle. Existing config files that set `rebuild: true`
will no longer affect behavior; the key is dropped silently. This is a breaking change for
anyone relying on `rebuild` in a config file; it is documented as such.

## Error handling

- Validation errors (e.g. `cpus` out of range, bad duration) bubble up through
  `PersistentPreRunE` → `execute` → `main` exactly as today (`Failed: ...`, exit 1).
- A malformed env value for a typed key (e.g. `OPENCODE_SANDBOX_CPUS=abc`) is caught by the
  same validation that runs at construction time, so startup fails with a clear message
  rather than silently resolving to a zero value.

## Testing (TDD)

- **`internal/viperconfig`**:
  - Precedence: flag > env > config > default for a representative key.
  - Env resolution and key replacement (`auto-stop-on-active-sessions` →
    `OPENCODE_SANDBOX_AUTO_STOP_ON_ACTIVE_SESSIONS`).
  - An unspecified flag with a default does **not** override env/config.
  - `rebuild` no longer present in the schema.
  - Validation applies to env-provided values.
- **`cmd/opencode-sandbox`**:
  - Replace `TestApplyLauncherConfigSetsUnsetFlags` and
    `TestApplyLauncherConfigRespectsCLIOverrides` with env/config/flag precedence tests
    driven through `buildRootCmd` + `Execute`.
  - Verify action toggles (`rebuild`, `dry-run`, `force`, ...) remain CLI-only.
  - Verify `prune` without `--age` uses `manual-prune-age` from config/env.
- Run `make check` (fmt, lint, test) on the final change.

## Documentation

- `docs/configuration.md`:
  - Remove `rebuild` from the config-file field table.
  - Add a "Launcher configuration environment variables" subsection (distinct from the
    existing sandbox-internal `env` file section): list each env var, the `OPENCODE_SANDBOX_`
    prefix, and the precedence `flag > env > config > default`.
  - Update the precedence section to include env vars.
- `README.md` — add/refresh a short env-var configuration reference if one exists; otherwise
  point to `docs/configuration.md`.
- `AGENTS.md` — no changes required.