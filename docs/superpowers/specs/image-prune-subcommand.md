# Instructional Spec: `image prune` subcommand (Chunk F)

## Goal

Expose the microsandbox SDK's unused `Image.Prune` capability as an `image prune`
subcommand, matching the semantics of the existing `prune` command (age threshold,
dry-run, force/confirmation).

## Background

The SDK exposes `microsandbox.Image.Prune(ctx)` which "removes cached image data that
is not used by sandboxes" and returns an `*ImagePruneReport`. It is currently unused by
the launcher. The existing `opencode-sandbox prune` command prunes stale VMs, volumes,
and images together; `image prune` targets images only.

## SDK data available

`sdk/go@v0.6.9/image.go`:

- `Image.Prune(ctx) (*ImagePruneReport, error)`.
- `ImagePruneReport` fields (image.go:209): `ImageRefsRemoved`, `ManifestsRemoved`,
  `LayersRemoved`, `FsmetaRemoved`, `VMDKRemoved uint32`, `BytesReclaimed *uint64`.
  Expose the relevant numbers (e.g. refs removed and bytes reclaimed) in output.

## Changes

1. **msb wrapper** (`internal/sandbox/msb/msb.go`): add `ImagePrune(ctx) (*msbSdk.ImagePruneReport, error)`
   to the `Client` interface and implement in `realMsbClient`. Update `testmock.go` and mocks.
2. **image package** (`internal/sandbox/image/`): add a `Prune` function (new file,
   e.g. `prune.go`) that calls the wrapper, filters to the launcher's own images if
   appropriate (see naming `ImagePrefix`), and returns a report suitable for the CLI.
3. **CLI** (`cmd/opencode-sandbox/commands_system.go`, `buildImageCmd`): add an `image
   prune` subcommand with:
   - `--age`/`--dry-run`/`--force` flags mirroring `buildPruneCmd`'s flags and parsing
     (`viperconfig.ParseHumanDuration`, default 7 days).
   - Renders the prune report (freed bytes, count removed).
4. **Tests**: unit tests for the `Prune` wrapper and image package function; CLI tests
   (`cmd/opencode-sandbox/cli_*_test.go`) for flag parsing and report output.

## Out of scope

- Changing the existing aggregate `prune` command's behavior.
- Image save/load.
- Interacting with the manual prune age config beyond what `buildPruneCmd` already does.