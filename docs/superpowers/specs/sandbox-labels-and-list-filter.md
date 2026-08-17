# Instructional Spec: Sandbox labels on create + `sandbox list` filter flags (Chunks D+E)

## Goal

Two coupled capabilities that belong together, plus parity with the microsandbox
(`msb`) CLI's `list` flags:

1. Tag the project sandbox VM the launcher creates with identifying labels (project
   slug, image ref).
2. Expose filter/format flags on `sandbox list`, matching `msb list` where applicable:
   `--label`, `--running`, `--stopped`, `-q`/names-only, `--format json`, and
   `--limit`.

They are coupled because a label filter is only useful once created sandboxes actually
carry labels. Today the launcher sets **no** labels on its sandboxes, so a filter flag
would match nothing.

To make `-q`/names-only available on `sandbox list`, the launcher's persistent global
`-q/--quiet` flag is renamed to `--error` (same "only show errors" behavior). This is a
breaking flag and config change, documented in README/docs.

## Design decisions (resolved)

- **Labels:** only two, set on the **project VM** at create: project slug and image
  ref. **No worktree label** — all worktree sessions for a project share one VM (the VM
  name is `projectVMName(slug)`, worktree-independent), so a per-session worktree label
  would be ambiguous. The project VM is the only create site that gets labels; the
  transient volume prefill/copy/edit helper sandboxes stay label-free.
- **`--running` + `--stopped` together:** `--running` wins (documented precedence). No
  error.
- **`--format json`:** a top-level JSON **array** of objects (matching `msb`), fields
  `name`, `status`, `image`, `created`, `updated`, `labels`. Timestamps marshal as Go
  `time.Time` default (RFC3339 with nanoseconds and offset, e.g.
  `2026-08-17T11:47:32.781877647+00:00`).
- **`--limit`:** applies to the **final** filtered result, after all local filters
  (VmPrefix name, running/stopped) are applied. Therefore no `WithListLimit` is used in
  the `msb` wrapper; the limit is a purely local truncation. The label filter is still
  applied server-side via `WithListLabels`.
- **Global flag rename:** `-q/--quiet` → `--error` (no shorthand), identical "only
  show errors" behavior. The internal config key `quiet` is renamed to `error` so the
  config-backed flag binding keeps working. This frees `-q`/`--quiet` for the `list`
  subcommand's names-only mode.

## msb `list` reference flags

From `msb list --help` (microsandbox v0.6.9):

- `--running` — show only running sandboxes.
- `--stopped` — show only stopped sandboxes.
- `--label <KEY=VALUE>` — only sandboxes carrying this label; repeatable, AND-matched.
- `-q, --quiet` — show only sandbox names.
- `--format <FORMAT>` — output format; `json` is the supported value.
- (Diagnostic log-level flags `--error/--warn/--info/--debug/--trace` are handled by
  the launcher's existing `--verbose`/`--error`; not list-specific here.)
- (`--tree` is the launcher's separate `tree` command, not a list concern.)

## SDK data available

`sdk/go@v0.6.9/sandbox.go`:

- `WithListLabels(labels map[string]string) SandboxListOption` — requires every returned
  sandbox to carry all given labels.
- `WithListLimit(limit uint32) SandboxListOption` — max sandboxes per page (not used
  here; the limit is local).
- `WithListCursor(cursor string) SandboxListOption` — continuation token.
- `ListSandboxesWith(ctx, ...SandboxListOption) (*SandboxPage, error)` — `SandboxPage`
  has `Sandboxes []*SandboxHandle` and `NextCursor *string`.

`sdk/go@v0.6.9/options.go`:

- `WithLabels(labels map[string]string) SandboxOption` and `WithLabel(key, value string)`
  attach labels at create.
- `SandboxConfig.Labels map[string]string` is populated by `WithLabels`; it is readable
  from a handle via `SandboxHandle.Config()`.

The msb wrapper (`internal/sandbox/msb/msb.go`) already paginates internally in
`realMsbClient.ListSandboxes` using `WithListCursor`.

The `SandboxHandle` interface already exposes `Image()` (via `Config().Image`). Labels
will be read from `Config().Labels`.

## Changes

### 1. Rename global `-q/--quiet` → `--error`

- `cmd/opencode-sandbox/constants.go`: replace `pFlagQuiet = "quiet"` with
  `pFlagError = "error"`.
- `cmd/opencode-sandbox/commands.go` (`buildMinimalRootFlagsCmd`): bind
  `BoolP(pFlagError, "", false, "Only show error output")` — **no shorthand** (the `-q`
  shorthand is freed for `list`). The `-v/--verbose` and `-y/--yes` flags are unchanged.
- `internal/viperconfig/viperconfig.go`, for config-backed consistency (a config-backed
  flag binds by matching key name):
  - `Config.Quiet` → `Config.Error` with `mapstructure:"error"`.
  - `configFlagKeys` and `configEnvKeys`: `"quiet"` → `"error"`.
  - `flagTypedDefault` case `"quiet"` → `"error"`.
  - Env var becomes `OPENCODE_SANDBOX_ERROR`; config file key becomes `error:`.
- `cmd/opencode-sandbox/cli.go`: `applyCLISettings` uses `r.Error()`; `levelFrom` logic
  is unchanged (only-error ⇒ `LevelQuiet`).
- Any resolver accessor `Resolver.Quiet()` is renamed to `Resolver.Error()`; update
  callers.

**Breaking change:** `--quiet`/`-q`, the `quiet:` config key, and
`OPENCODE_SANDBOX_QUIET` are removed in favor of `--error`, `error:`, and
`OPENCODE_SANDBOX_ERROR`. Update docs (see Documentation).

### 2. Define label constants

Add `internal/sandbox/naming/labels.go` with constants for the labels the launcher sets,
using the `org.opencode-sandbox.` prefix (see `image.OpenCodeVersionLabel`):

- `LabelProject = "org.opencode-sandbox.project"` — value `git.ProjectSlug(ui)`.
- `LabelImage = "org.opencode-sandbox.image"` — value the runner image reference.

No worktree label (see Design decisions).

### 3. Set labels at sandbox creation

In `internal/sandbox/session/vm_lifecycle.go`, `ensureProjectVM` builds `optsList`
(labels added near the other create options):

```go
optsList := []msbSdk.SandboxOption{
    msbSdk.WithImage(imageRef),
    // ...
    msbSdk.WithLabels(map[string]string{
        naming.LabelProject: slug,
        naming.LabelImage:   imageRef,
    }),
    // ...
}
```

`slug` is already computed in `ensureProjectVM` (`git.ProjectSlug(ui)`); `imageRef` is a
parameter. `msb.Client.CreateSandbox`'s signature is unchanged — labels travel as a
`SandboxOption`. The transient volume helper sandboxes (volume.go, operations.go) are
**not** labeled.

### 4. Extend `msb.ListSandboxes` to accept a label filter

- `msb.Client` interface (`internal/sandbox/msb/msb.go`):
  `ListSandboxes(ctx context.Context)` →
  `ListSandboxes(ctx context.Context, labels map[string]string)`.
- `realMsbClient.ListSandboxes`: when `labels` is non-empty, pass
  `msbSdk.WithListLabels(labels)` to the first `ListSandboxesWith` call and keep it
  across all cursor-paginated pages. Keep the internal cursor loop unchanged.
- Update `MockMsbClient.ListSandboxes` (testmock.go) to accept the new signature and
  record the received labels for assertions; existing tests that set `ListSandboxesFn`
  or `Sandboxes` keep working.
- Update the three callers to pass nil/empty (they need no filtering):
  - `internal/sandbox/session/list.go:44`
  - `internal/sandbox/pruning/catalog.go:51`
  - `internal/sandbox/volume/operations.go:23`

`WithListLimit` is **not** used here (limit is local; see Design decisions).

### 5. Extend `session.ListSandboxes` with filter/limit/format

`internal/sandbox/session/list.go`:

- Add a `ListOption` struct:

  ```go
  type ListOption struct {
      Labels       map[string]string
      Limit        *uint32
      RunningOnly  bool
      StoppedOnly  bool
  }
  ```

  and change `ListSandboxes(ctx)` → `ListSandboxes(ctx, opts ...ListOption)`. A single
  `ListOption` is expected; merge all passed opts if defensive merging is desired, but a
  single option (or the zero value) is the normal case.

- Filtering order inside `ListSandboxes`:
  1. Call `msb.Get().ListSandboxes(ctx, labels)` (server-side label filter via
     `WithListLabels`).
  2. Keep only names with the `naming.VmPrefix` prefix (existing behavior).
  3. If `RunningOnly` or `StoppedOnly`, classify each status with
     `msb.GetVMStatus` / `msb.IsSandboxActive` (`internal/sandbox/msb/msb.go`) — not
     string-matching. If both are set, `RunningOnly` wins.
  4. If `Limit` is set, truncate the result to `Limit` **after** all the above filters.

- Extend `Info` with:
  - `Labels map[string]string` — populated from `handle.Config().Labels`.
  - `CreatedAtRaw time.Time` and `UpdatedAtRaw time.Time` — the raw handle timestamps,
    used only for `--format json`.
  - Keep the existing display string fields (`Name`, `Status`, `Image`,
    `CreatedAt string`) unchanged so the table view and existing tests are unaffected.

### 6. CLI flags on `sandbox list`

`cmd/opencode-sandbox/commands_system.go` `buildListCmd`:

- `-q, --quiet` → names-only mode: print one sandbox name per line (via `ui.Out`), no
  header. Wire this flag locally on the command; it is independent of the global level.
- `--label key=value` — repeatable (`StringArray`), AND-matched. Each value must be
  parseable as `KEY=VALUE`; a missing `=` is a usage error. Collect into a
  `map[string]string` and pass to `session.ListSandboxes` as `Labels`.
- `--limit N` — unsigned int; pass through as `*uint32`.
- `--running` / `--stopped` — bool; set `RunningOnly` / `StoppedOnly`. If both given,
  `--running` wins (no error).
- `--format <FORMAT>` — only `json` is supported; any other value is a usage error.
  When `--format json`, emit a top-level JSON **array** (matching `msb`) to
  `ui.Out`/stdout, each element:

  ```json
  {
    "name": "opencode-sandbox-vm-abc123",
    "status": "running",
    "image": "opencode-sandbox/runner:latest",
    "created": "2026-08-17T10:30:00+00:00",
    "updated": "2026-08-17T11:00:00+00:00",
    "labels": { "org.opencode-sandbox.project": "proj-abc", "org.opencode-sandbox.image": "opencode-sandbox/runner:latest" }
  }
  ```

  Timestamps marshal via Go `time.Time` default (RFC3339 with nanos+offset). Use a
  dedicated `Info` JSON view (e.g. a `jsonInfo` struct with the raw time fields and the
  label map). `--format json` and `-q` are mutually exclusive (error if both).
- Interaction of `--format json` with the column table: `--format json` bypasses the
  `printItems` table entirely; the default (no `--format`) keeps the existing column
  output (see Chunk A for columns).
- Update help text for the new flags.

### 7. Tests

- **Label constants** (`internal/sandbox/naming`): assert the exact key strings and
  `org.opencode-sandbox.` prefix.
- **Create attaches labels** (`internal/sandbox/session/vm_test.go`): assert the mock's
  `CreatedSandboxCalls` opts include `WithLabels` with project and image labels; assert
  helper volume sandboxes (volume_test.go) are **not** labeled.
- **`msb.ListSandboxes` label forwarding** (msb tests / testmock): non-empty labels pass
  `WithListLabels`; empty labels pass none; pagination preserved.
- **`session.ListSandboxes` ordering** (session `list_test.go`): VmPrefix filter, then
  running/stopped (via status, not string), then limit truncation; `--running` wins when
  both flags set; labels forwarded down.
- **CLI** (`cmd/opencode-sandbox/cli_list_subcommand_test.go`):
  - `--label` narrows results; malformed `--label` (no `=`) errors.
  - `--limit` caps output.
  - `--running` / `--stopped` filter correctly; both together → running wins.
  - `-q` prints names only (one per line, no header).
  - `--format json` emits a valid array round-tripping through `encoding/json` with the
    documented fields and Go time marshaling; `-q` + `--format json` errors; unknown
    `--format` value errors.
  - Global rename: `--error` still suppresses non-error output; the old `-q`/`--quiet`
    flags on the root command are gone.
  - Existing default (no filter flags) output unchanged.

## Documentation

Update when implementing (see AGENTS.md "Documentation" rule):

- `docs/commands.md`: replace the `--quiet` row with `--error` (no shorthand) and its
  help text; document the new `sandbox list` flags (`--label`, `--limit`, `--running`,
  `--stopped`, `-q/--quiet`, `--format`).
- `docs/configuration.md`: rename the `quiet` config key/environment rows to `error` /
  `OPENCODE_SANDBOX_ERROR`.
- `README.md`: note the `--quiet` → `--error` rename as a breaking change and the new
  list filter flags.

## Out of scope

- A worktree/branch label (see Design decisions — one shared VM per project).
- Label filtering for volumes/images (no SDK support).
- Diagnostic log-level flags from `msb list` (`--error/--warn/--info/--debug/--trace`) —
  the launcher covers log levels with `--verbose`/`--error`.
- `--tree` — the launcher has a dedicated `tree` command.
- Table-rendering library / styling (see `table-rendering-library.md`, Spec 0).
- `WithListLimit` / server-side limit in the `msb` wrapper (the `--limit` is local).