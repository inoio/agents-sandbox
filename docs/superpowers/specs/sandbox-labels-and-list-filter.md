# Instructional Spec: Sandbox labels on create + `sandbox list` filter flags (Chunks D+E)

## Goal

Two coupled capabilities that belong together, plus parity with the microsandbox
(`msb`) CLI's `list` flags:

1. Tag every sandbox VM the launcher creates with identifying labels (project slug,
   worktree/branch, image ref).
2. Expose filter/format flags on `sandbox list`, matching `msb list` where applicable:
   `--label`, `--running`, `--stopped`, `-q`/names-only, `--format json`, and
   `--limit`.

They are coupled because a label filter is only useful once created sandboxes actually
carry labels. Today the launcher sets **no** labels on its sandboxes, so a filter flag
would match nothing.

## msb `list` reference flags

From `msb list --help` (microsandbox v0.6.9):

- `--running` — show only running sandboxes.
- `--stopped` — show only stopped sandboxes.
- `--label <KEY=VALUE>` — only sandboxes carrying this label; repeatable, AND-matched.
- `-q, --quiet` — show only sandbox names.
- `--format <FORMAT>` — output format; `json` is the supported value.
- (Diagnostic log-level flags `--error/--warn/--info/--debug/--trace` are handled by
  the launcher's existing `--verbose`/`--quiet`; not list-specific here.)
- (`--tree` is the launcher's separate `tree` command, not a list concern.)

## SDK data available

`sdk/go@v0.6.9/sandbox.go`:

- `WithListLabels(labels map[string]string) SandboxListOption` — requires every returned
  sandbox to carry all given labels.
- `WithListLimit(limit uint32) SandboxListOption` — max sandboxes per page.
- `WithListCursor(cursor string) SandboxListOption` — continuation token.
- `ListSandboxesWith(ctx, ...SandboxListOption) (*SandboxPage, error)` — `SandboxPage`
  has `Sandboxes []*SandboxHandle` and `NextCursor *string`.

The msb wrapper (`internal/sandbox/msb/msb.go`) already paginates internally in
`realMsbClient.ListSandboxes` using `WithListCursor`.

## Changes

### 1. Define label constants

Add to `internal/sandbox/naming` (or a new small file there) constants for the labels
the launcher will set, e.g.:

- `LabelProject` — value `git.ProjectSlug(ui)`.
- `LabelWorktree` — worktree/branch identifier (empty for the default session).
- `LabelImage` — the runner image reference.

Use `org.opencode-sandbox.` prefix (see existing `image.OpenCodeVersionLabel` for the
convention).

### 2. Set labels at sandbox creation

Find where the launcher calls `CreateSandbox` (via `msb.Client`); thread a `map[string]string`
through so the labels are attached. Populate with the values defined above. Touches
`msb.Client.CreateSandbox` only if its signature changes (prefer adding labels inside the
call site rather than widening the interface).

### 3. Extend `ListSandboxes` to accept filter/limit

Change `msb.Client.ListSandboxes(ctx)` → `ListSandboxes(ctx, opts ...ListOption)` (or an
explicit options struct) so the label filter and limit can be passed down to
`ListSandboxesWith`. Keep the internal cursor pagination; when a `limit` is set, honor it
across pages. Update `testmock.go` and all callers.

### 4. Extend `session.ListSandboxes`

`internal/sandbox/session/list.go`: accept filter/format arguments and pass them through.
Keep the `VmPrefix` name filtering. Where `--running`/`--stopped` filter on lifecycle
status, reuse `msb.GetVMStatus` / `msb.IsSandboxActive` (internal/sandbox/msb/msb.go)
rather than string-matching status.

### 5. CLI flags on `sandbox list`

`cmd/opencode-sandbox/commands_system.go` `buildListCmd`:

- `--label key=value` (repeatable) → `WithListLabels` (AND-matched, like msb).
- `--limit N` → `WithListLimit`.
- `--running` → keep only active sandboxes (`msb.IsSandboxActive`).
- `--stopped` → keep only stopped sandboxes.
  - If both `--running` and `--stopped` are given, treat as an error (mutually
    exclusive), or document precedence.
- `-q, --quiet` → print only sandbox names (one per line), regardless of the global
  quiet level. Note the launcher already has a persistent `-q` flag; wire this so
  names-only mode takes effect for `list`.
- `--format json` → emit a JSON array of sandboxes instead of the column table. Define
  the JSON schema (fields: name, status, image, created, updated; plus labels once
  present). `--format` currently supports only `json`; reject unknown values.
- Update help text and column output (see Chunk A for columns).

### 6. Tests

- Unit tests: labels are attached at create; `ListSandboxes` forwards filter/limit;
  label filter narrows results; limit caps results; `--running`/`--stopped` filter
  correctly via status; JSON marshaling round-trips.
- CLI tests (`cmd/opencode-sandbox/cli_list_subcommand_test.go`): `--label`, `--limit`,
  `--running`, `--stopped`, `-q`, and `--format json` parse and take effect; existing
  default output unchanged.

## Out of scope

- Label filtering for volumes/images (no SDK support).
- Diagnostic log-level flags from `msb list` (`--error/--warn/--info/--debug/--trace`) —
  the launcher already covers log levels with `--verbose`/`--quiet`.
- `--tree` — the launcher has a dedicated `tree` command.
- Table-rendering library / styling (see `table-rendering-library.md`, Spec 0).