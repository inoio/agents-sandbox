# Instructional Spec: Richer `volume list` output (Chunk B)

## Goal

`opencode-sandbox volume list` currently prints only two columns: volume name and
path. Extend the output to surface the volume metadata the microsandbox SDK already
exposes, so users can see disk usage, quota, kind, creation time, and labels.

## Design principle

Align list output formats with the microsandbox (`msb`) CLI and Docker. Both render
tables with aligned columns (`docker volume ls`: `DRIVER  VOLUME NAME`; `msb volume ls`:
name/path/kind, etc.). Prefer columnar output over the current ad-hoc two-column
`%-50s %s` format. Columns render left-aligned with sensible widths; the format string
must be shared by the command and its tests.

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

## Changes

1. **msb wrapper** (`internal/sandbox/msb/msb.go`): extend the `VolumeHandle`
   interface and `realVolumeHandle` with the new accessors listed above, mirroring
   the existing `CreatedAt()` pattern. Update `testmock.go` and any mocks.
2. **volume package** (`internal/sandbox/volume/list.go`): extend `VolumeInfo` with
   the new fields and populate them in `ListVolumes`. Add a helper to render size
   (bytes → human-readable, e.g. `1.2G`); mirror any existing size-format helper in
   the codebase rather than adding a new one.
3. **CLI** (`cmd/opencode-sandbox/commands_system.go`, `buildVolumeCmd`): update the
   `list` subcommand's `printItems` format to the new columnar format. Pick columns
   that fit a normal terminal width; truncate long paths if needed.
4. **Tests** (`cmd/opencode-sandbox/cli_volume_test.go`): update expectations for the
   new output. Add unit tests for any new size-formatting helper and for the extended
   `ListVolumes` population.

## Out of scope

- Volume labels filtering/selection (no SDK option exists; see Chunk E for sandbox
  labels only).
- Creating or modifying volumes.
- `--limit`/pagination (not supported by SDK for volumes).