# Instructional Spec: Richer `image list` output (Chunk C)

## Goal

`opencode-sandbox image list` currently prints only two columns: reference and digest.
Extend the output to surface image metadata the microsandbox SDK already exposes:
architecture, OS, layer count, size, creation time, and last-used time.

## Design principle

Align list output formats with the microsandbox (`msb`) CLI and Docker. `docker images`
renders `REPOSITORY  TAG  IMAGE ID  CREATED  SIZE`; `msb image list` renders aligned
columns for the same metadata. Prefer columnar, left-aligned output over the current
ad-hoc two-column `%-50s %s` format. The format string must be shared by the command
and its tests.

## SDK data available

`msb.ImageHandle` (internal/sandbox/msb/msb.go) currently exposes only
`Reference()`, `ManifestDigest()`, `LastUsedAt()`. The underlying SDK
`*microsandbox.ImageHandle` (sdk/go@v0.6.9/image.go) additionally exposes:

- `Architecture() string`
- `OS() string`
- `LayerCount() uint`
- `SizeBytes() *int64`
- `CreatedAt() time.Time`

## Changes

1. **msb wrapper** (`internal/sandbox/msb/msb.go`): extend the `ImageHandle` interface
   and `realMsbClient.ImageList`/handle adapter with the new accessors, mirroring the
   existing `LastUsedAt()` pattern. Note: `ImageHandle` is passed through directly from
   the SDK (`result[i] = h`), so verify the interface adapter wraps the SDK handle; if
   the wrapper currently returns the SDK handle unadapted, wrap it to satisfy the
   interface. Update `testmock.go` and mocks.
2. **image package** (`internal/sandbox/image/list.go`): extend `Info` with the new
   fields and populate them in `ListImages`. Add/reuse a human-readable size helper
   (bytes → e.g. `1.2G`); reuse any existing size helper in the repo.
3. **CLI** (`cmd/opencode-sandbox/commands_system.go`, `buildImageCmd`): update the `list`
   subcommand's `printItems` format to the new columnar format. Choose columns that fit
   a normal terminal; truncate long references if needed.
4. **Tests** (`cmd/opencode-sandbox/cli_*_test.go` covering `image list`): update
   expectations for the new output. Add unit tests for the extended `ListImages`
   population and any size helper.

## Out of scope

- `image prune` (see Chunk F).
- Image pull/load/save behavior changes.
- `--limit`/pagination (not supported by SDK for images).