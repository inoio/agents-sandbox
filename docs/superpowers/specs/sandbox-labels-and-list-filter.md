# Instructional Spec: Sandbox labels on create + list label filter & pagination (Chunks D+E)

## Goal

Two coupled capabilities that belong together:

1. Tag every sandbox VM the launcher creates with identifying labels (project slug,
   worktree/branch, image ref).
2. Expose `--label` filtering and `--limit` pagination on `sandbox list`, using the
   SDK's `WithListLabels` / `WithListLimit`.

They are coupled because a label filter is only useful once created sandboxes actually
carry labels. Today the launcher sets **no** labels on its sandboxes, so a filter flag
would match nothing.

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

`internal/sandbox/session/list.go`: accept filter/limit arguments and pass them through.
Keep the `VmPrefix` name filtering.

### 5. CLI flags on `sandbox list`

`cmd/opencode-sandbox/commands_system.go` `buildListCmd`:

- `--label key=value` (repeatable) → `WithListLabels`.
- `--limit N` → `WithListLimit`.
- Update help text and `printItems` output if adding columns (see Chunk A).

### 6. Tests

- Unit tests: labels are attached at create; `ListSandboxes` forwards filter/limit;
  label filter narrows results; limit caps results.
- CLI tests (`cmd/opencode-sandbox/cli_list_subcommand_test.go`): `--label` and `--limit`
  flags parse and take effect; existing default output unchanged.

## Out of scope

- Label filtering for volumes/images (no SDK support).
- Parser for `--label` with multiple values — implement repeatable flag per cobra
  conventions used elsewhere in the repo.