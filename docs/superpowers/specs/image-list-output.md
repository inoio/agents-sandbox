# Instructional Spec: Richer `image list` output (Chunk C)

## Goal

`opencode-sandbox image list` currently prints only two columns: reference and digest
(`%-50s %s`). Rework the output to match the microsandbox (`msb`) CLI exactly, surfacing
image metadata the SDK already exposes. The target format is what `msb image list`
prints:

```
REFERENCE                                                                 DIGEST                 SIZE         CREATED
opencode-sandbox/runner-inoio-de-srv-38hbcbx5r15p8h:41q7859zkl7myf        sha256:a90a7deebaca    824.6 MiB    2026-08-17 10:42:36
opencode-sandbox/runner-opencode-sandbox-32u5xppzd7ayvf:1yo9dfa5p50vzh    sha256:9e452a09069e    2.1 GiB      2026-08-16 23:34:19
```

## Design principle

Align list output with the microsandbox (`msb`) CLI and Docker. `msb image list`
renders aligned columns `REFERENCE  DIGEST  SIZE  CREATED`; `docker images` renders
`REPOSITORY  TAG  IMAGE ID  CREATED  SIZE`. Prefer columnar, left-aligned output over
the current ad-hoc two-column `%-50s %s` format. The format string must be a shared
const used by both the command and its tests.

- **Keep the two existing columns (reference, digest) stable** so scripts parsing the
  current output keep working; append columns to the right rather than reordering.
- Match `msb` verbatim for the added columns (`SIZE` in binary units, `CREATED` with
  seconds). Do **not** surface architecture, OS, layer count, or last-used time; `msb`
  does not show them and they would not fit a normal terminal.
- Do **not** truncate long references (`msb` shows them in full).
- Keep the output **plain** (no coloring, no bold headers). Do not adopt a
  table-rendering library for this chunk — the existing `printItems` + `termio`
  approach with a fixed format string is sufficient. (A table library is tracked
  separately in `table-rendering-library.md`, Spec 0.)

## SDK data available

`msb.ImageHandle` (internal/sandbox/msb/msb.go) currently exposes only
`Reference()`, `ManifestDigest()`, `LastUsedAt()`. The underlying SDK
`*microsandbox.ImageHandle` (sdk/go@v0.6.9/image.go) exposes:

- `Architecture() string`
- `OS() string`
- `LayerCount() uint`
- `SizeBytes() *int64` — total image size in bytes, or `nil` if unknown.
- `CreatedAt() time.Time` — when the image was first pulled; zero value if unknown.

`Image.List` (sdk/go@v0.6.9/image.go:34) returns handles ordered newest-first.

## Changes

### 1. msb wrapper — `internal/sandbox/msb/msb.go`

Extend the `ImageHandle` interface with the accessors needed by the new columns:

```go
type ImageHandle interface {
	Reference() string
	ManifestDigest() string
	LastUsedAt() time.Time
	SizeBytes() *int64
	CreatedAt() time.Time
}
```

`realMsbClient.ImageList` currently passes the SDK handle through unchanged
(`result[i] = h`). The concrete `*microsandbox.ImageHandle` already implements
`SizeBytes()` and `CreatedAt()`, so the pass-through satisfies the extended interface —
**no `realImageHandle` adapter is required** for the new methods. If the interface is
later extended with a method the SDK handle lacks, add a wrapping adapter then (YAGNI).

Update `MockImageHandle` in `internal/sandbox/msb/testmock.go`: add `SizeBytes_ *int64`
and `CreatedAt_ time.Time` fields plus the two accessor methods, mirroring the existing
`LastUsedAt_` pattern.

### 2. image package — `internal/sandbox/image/list.go`

Extend `Info` with the new display fields and populate them in `ListImages`:

```go
type Info struct {
	Reference string
	Digest    string
	Size      string
	CreatedAt string
}
```

`ListImages` reads `SizeBytes()`/`CreatedAt()` from each handle and renders them via
`FormatSize` and `FormatImageTime` below. Keep the existing `naming.ImagePrefix`
filtering.

Add two helpers in the `image` package (each with unit tests):

- `FormatSize(bytes *int64) string` — renders binary units matching `msb`:
  - `nil` → `unknown` (SDK may not report a size).
  - `0` → `0 B`.
  - otherwise a single mantissa with one decimal place and a binary suffix, e.g.
    `512 B`, `1.0 KiB`, `824.6 MiB`, `2.1 GiB`. Use base-1024 units (`KiB`, `MiB`,
    `GiB`, `TiB`). Use `math.Round` on the mantissa to one decimal place and trim a
    trailing `.0` when the mantissa is integral (e.g. `2.1 GiB`, but `1 KiB` for a
    whole `1024` bytes).
- `FormatImageTime(t time.Time) string` — renders `YYYY-MM-DD HH:MM:SS` in the time's
  own location (matching `msb`), or an empty string for the zero value.

These helpers are distinct from `session.FormatTime` (which omits seconds) because this
chunk deliberately matches `msb`'s second-granularity output; do not reuse it.

### 3. CLI — `cmd/opencode-sandbox/commands_system.go`, `buildImageCmd`

Replace the `"%-50s %s"` literal in the `list` subcommand with a shared const:

```go
// imageListFormat is shared by buildImageCmd and its tests so the column
// layout stays in sync.
const imageListFormat = "%-73s %-22s %-11s %s"
```

These widths approximate `msb`'s `REFERENCE` (73), `DIGEST` (22), `SIZE` (11) columns;
the trailing `CREATED` column is unconstrained. Render with the existing `printItems`:

```go
printItems(images, "No images found.", imageListFormat, ui,
	func(i image.Info) string { return i.Reference },
	func(i image.Info) string { return i.Digest },
	func(i image.Info) string { return i.Size },
	func(i image.Info) string { return i.CreatedAt },
)
```

Do **not** reuse `truncateImage` (sandbox list truncates references at 44; image list
matches `msb` and does not truncate).

### 4. Tests

- `cmd/opencode-sandbox/cli_list_subcommand_test.go` — `TestListImages` currently
  asserts on the old two-column layout with hardcoded alignment padding. Update the
  `wantOut` strings to the new four-column layout. Assertions go through
  `containsNormalized` (whitespace-insensitive), so exact padding does not matter; keep
  the reference and digest tokens intact and append `unknown`-or-size and a timestamp
  for each image. Add cases covering a `nil` size and a non-prefixed image (filtered
  out). Keep the "No images found." and error cases unchanged.
- `internal/sandbox/image/list_test.go` — extend the existing `TestListImages*` cases
  to set `SizeBytes_`/`CreatedAt_` on the `MockImageHandle` and assert the new `Info`
  fields are populated; add cases for `nil` size and zero `CreatedAt`.
- Add unit tests for `FormatSize` and `FormatImageTime` in `internal/sandbox/image/`
  covering: `nil`, `0`, sub-`KiB`, `MiB`, `GiB`, whole-unit mantissa trimming, zero
  `time.Time`, and a non-zero time.

## Shared format string

`imageListFormat` must be a package-level `const` in `cmd/opencode-sandbox` referenced
by both `buildImageCmd` and `TestListImages`, mirroring how `sandboxListFormat` is
shared for sandbox list. Add a `TestImageListFormatShared` guard (empty-string check)
alongside `TestSandboxListFormatShared`.

## Out of scope

- `image prune` (see Chunk F).
- Image pull/load/save behavior changes.
- `--limit`/pagination (not supported by SDK for images).
- Surfacing architecture, OS, layer count, or last-used time columns.
- Reference truncation (match `msb`, which does not truncate).
- Coloring / styling of the output (kept plain).
- Adopting a table-rendering library (see `table-rendering-library.md`, Spec 0).
- Reusing `session.FormatTime` (seconds format differs).