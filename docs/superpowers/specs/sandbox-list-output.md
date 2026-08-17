# Instructional Spec: Richer `sandbox list` output (Chunk A)

## Goal

`opencode-sandbox list` (a.k.a. `sandbox list`) currently prints only two columns:
sandbox name and status. Extend the output to surface metadata the microsandbox SDK
already exposes for each handle — creation time, update time, backend kind, and the
sandbox's image — so users can see at a glance what each VM is.

## Design principle

Align list output formats with the microsandbox (`msb`) CLI and Docker. `docker ps`
renders `CONTAINER ID  IMAGE  COMMAND  CREATED  STATUS  PORTS  NAMES`; `msb list`/`msb
status` render aligned columns for the same kind of metadata. Prefer columnar,
left-aligned output over the current ad-hoc two-column `%-40s %s` format. The format
string must be shared by the command and its tests.

Keep the existing two primary columns (name, status) stable so scripts parsing the
current output keep working; append columns to the right rather than reordering.

## SDK data available

`msb.SandboxHandle` (internal/sandbox/msb/msb.go) currently exposes:
`Name()`, `Status()`, `UpdatedAt()`, `Image()`, plus lifecycle methods. It does **not**
yet expose `CreatedAt()` or `BackendKind()`. The underlying SDK
`*microsandbox.SandboxHandle` (sdk/go@v0.6.9/sandbox.go) exposes:

- `CreatedAt() time.Time`
- `BackendKind() BackendKind`
- `Config() (*SandboxConfig, error)` → `Image`, `CPUs uint8`, `MemoryMiB uint32`.

## Changes

1. **msb wrapper** (`internal/sandbox/msb/msb.go`): extend the `SandboxHandle`
   interface and `realSandboxHandle` with `CreatedAt()` and `BackendKind()`,
   mirroring the existing `UpdatedAt()` adapter. `Image()` already exists and calls
   `Config()`; keep it.

2. **session package** (`internal/sandbox/session/list.go`): extend `Info` with fields
   for the new columns (e.g. `CreatedAt`, `UpdatedAt`, `Image`). Populate them in
   `ListSandboxes` from the handle. Keep `Status` as today. Decide a stable display
   format for times (see "Time formatting" below) and document it here.

3. **CLI** (`cmd/opencode-sandbox/commands_system.go`, `buildListCmd`): update the
   `printItems` format to the new columnar format. Chosen columns: `NAME`, `STATUS`,
   `IMAGE`, `CREATED` (and `UPDATED` if it fits a normal terminal width). Truncate
   long image references and keep names/status unchanged from today. Render with the
   existing `printItems` + `termio` approach; see "Rendering & styling" below.

## Rendering & styling

Keep the output **plain** (no coloring, no bold headers). Do **not** adopt a
table-rendering library for this chunk — the existing `printItems` + `termio` approach
with a fixed format string is sufficient for the few added columns. If a table library
is ever adopted, it is tracked separately in `table-rendering-library.md` (Spec 0), a
possible predecessor to this and the other list-output chunks.

4. **Tests** (`cmd/opencode-sandbox/cli_list_subcommand_test.go`, and the session
   `list_test.go` if present):
   - The existing tests assert on a normalized two-field line like
     `opencode-sandbox-vm-abc running`. Update expectations to include the new
     columns; keep the name and status tokens intact so the assertion style still
     holds.
   - The `MockSandboxHandle` (internal/sandbox/msb/testmock.go) needs new fields
     (`CreatedAt_`, `BackendKind_`) and the interface methods; the mock's `Config()`
     already returns `Cfg *msbSdk.SandboxConfig`, so set `Cfg.Image` in tests to
     exercise the image column.
   - Add a unit test in the session package for `ListSandboxes` populating the new
     `Info` fields.

## Time formatting

Pick one format and reuse it for both `CreatedAt` and `UpdatedAt`. Prefer a compact
relative or `YYYY-MM-DD HH:MM` form so columns stay narrow; note there is likely no
existing time formatter in the repo — add one small helper in the session package (or
reuse one if it already exists) and unit-test it. Document the chosen format here when
implementing.

## Out of scope

- Label filtering / pagination flags (see `sandbox-labels-and-list-filter.md`).
- Volume/image list output (see `volume-list-output.md`, `image-list-output.md`).
- Changing how status strings are produced (`string(h.Status())`).
- Any change to the sandbox name or the `VmPrefix` filtering.
- Coloring / styling of the output (kept plain).
- Adopting a table-rendering library (see `table-rendering-library.md`, Spec 0).