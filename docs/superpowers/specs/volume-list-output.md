# Instructional Spec: Richer `volume list` output (Chunk B)

## Goal

`opencode-sandbox volume list` currently prints only two columns: volume name and
path. Replace that output with a columnar table that surfaces the volume metadata the
microsandbox SDK already exposes: name, storage kind, size (quota/capacity), and
creation time.

## Design principle

Align list output with the microsandbox (`msb`) CLI. `msb volume list` renders
`NAME  KIND  SIZE  CREATED` as left-aligned columns. Match that column set exactly:
`NAME`, `KIND`, `SIZE`, `CREATED`, and drop the current `PATH` column (msb does not
show the path). Prefer columnar output over the current ad-hoc two-column `%-50s %s`
format. The format string must be shared by the command and its tests.

## SDK data available

`msb.VolumeHandle` (internal/sandbox/msb/msb.go) currently exposes only
`Name()`, `Path()`, `Kind()`, `CreatedAt()`. The underlying SDK
`*microsandbox.VolumeHandle` (sdk/go@v0.6.9/volume.go) additionally exposes:

- `IsDefault() bool`
- `QuotaMiB() *uint32`
- `UsedBytes() uint64`
- `CapacityBytes() *uint64`
- `DiskFormat() *string`
- `DiskFstype() *string`
- `Labels() map[string]string`

## Chosen columns and rendering

Columns, left-aligned, in this order (matching `msb volume list`):

| Column  | Source | Rendering |
|---------|--------|-----------|
| `NAME`  | `h.Name()` | As-is. |
| `KIND`  | `h.Kind()` | `string(h.Kind())`, as today. |
| `SIZE`  | quota, then capacity | quota (`QuotaMiB`) if non-nil, else capacity (`CapacityBytes`) if non-nil, else `-` for dir/unlimited volumes. Both quota and capacity are bytes → human-readable via the shared size helper (e.g. `1.2G`). |
| `CREATED` | `h.CreatedAt()` | `YYYY-MM-DD HH:MM:SS` in the time's own location (matches msb). Zero time renders as `-`. |

Absent/nil metadata (unlimited quota, no capacity, no creation time) renders as the
`-` placeholder so rows stay aligned.

The path is deliberately dropped from the output; it is no longer a column.

## Shared size helper

Add one human-readable bytes→string helper (e.g. `1234567` → `1.2G`) in a shared
internal package (not per-call-site). Both `volume list` (Chunk B) and `image list`
(Chunk C, see `image-list-output.md`) reuse it. There is currently no such helper in
the repo — do not duplicate one. Unit-test it.

Home: `internal/sandbox/humanize` with a `FormatBytes(uint64) string` (or `FormatSize`)
func. Placed as a sibling of `internal/sandbox/image` and `internal/sandbox/volume` so
both import it without an import cycle.

## Changes

1. **msb wrapper** (`internal/sandbox/msb/msb.go`): extend the `VolumeHandle`
   interface and `realVolumeHandle` with the new accessors listed above, mirroring
   the existing `CreatedAt()` pattern. `realVolumeHandle.val` is `any` and switches on
   `*msbSdk.VolumeHandle` and `*msbSdk.Volume`; the new accessors only exist on
   `*msbSdk.VolumeHandle`. For the `*msbSdk.Volume` case return zero values (nil
   pointers, `0`, `false`, empty map/string). `ListVolumes` always yields
   `*msbSdk.VolumeHandle`, so list output is unaffected. Update `testmock.go`
   (`MockVolumeHandle`) and any mocks.
2. **volume package** (`internal/sandbox/volume/list.go`): extend `VolumeInfo` with
   the fields needed for output and populate them in `ListVolumes` from the handle.
   Keep the existing `Name`/`Kind`; add quota, capacity, and a `CreatedAt` display
   string using the shared time/`-` rules. Do not add a PATH field that is unused.
3. **CLI** (`cmd/opencode-sandbox/commands_system.go`, `buildVolumeCmd`): update the
   `list` subcommand's `printItems` call to the new columnar format. Add a shared
   `volumeListFormat` constant (like `sandboxListFormat`) used by both the command and
   its tests, so the layout stays in sync.
4. **Tests** (`cmd/opencode-sandbox/cli_volume_test.go`, or a new
   `cli_volume_list_test.go`): add/update coverage for the new output using the
   `containsNormalized` assertion style. Add unit tests for the shared size helper and
   for the extended `ListVolumes` population (including `-` for dir/unlimited and for
   missing creation time).

## Time formatting

Render the `CREATED` column with `YYYY-MM-DD HH:MM:SS` (with seconds) to match msb.
This is distinct from `session.FormatTime`, which renders without seconds — do not
reuse it here. Add a small formatter in the `volume` package (e.g. an exported
`FormatVolumeTime(time.Time) string` returning `-` for the zero time) and unit-test it.

## Out of scope

- Volume labels filtering/selection (no SDK option exists; see Chunk E for sandbox
  labels only).
- Surfacing `IsDefault`, `DiskFormat`, `DiskFstype`, or `Labels` as columns (kept in
  the wrapper for future use but not rendered).
- Creating or modifying volumes.
- `--limit`/pagination (not supported by SDK for volumes).
- `--quiet` / `--format json` flags (not currently offered by this command).