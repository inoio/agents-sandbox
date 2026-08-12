package sandbox

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/volume"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// Re-exported volume module symbols preserve the public API of the sandbox
// core so that cmd/opencode-msb continues to compile without changing its
// import paths.

// ListVolumes re-exports the volume module's ListVolumes.
func ListVolumes(ctx context.Context) ([]VolumeInfo, error) {
	return volume.ListVolumes(ctx)
}

// VolumeInfo is re-exported from the volume module.
type VolumeInfo = volume.VolumeInfo

// CmdMigrate re-exports the volume module's CmdMigrate.
func CmdMigrate(ctx context.Context, projectSlug, volumeName, imageTag string, rmOld, dryRun bool, ui termio.UI) error {
	return volume.CmdMigrate(ctx, projectSlug, volumeName, imageTag, rmOld, dryRun, ui)
}

// CmdReset re-exports the volume module's CmdReset.
func CmdReset(ctx context.Context, projectSlug, volumeName, imageTag string, rmOld, dryRun bool, ui termio.UI) error {
	return volume.CmdReset(ctx, projectSlug, volumeName, imageTag, rmOld, dryRun, ui)
}

// CmdEdit re-exports the volume module's CmdEdit.
func CmdEdit(ctx context.Context, projectSlug, volumeName, imageTag string, rmOld, dryRun bool, ui termio.UI) error {
	return volume.CmdEdit(ctx, projectSlug, volumeName, imageTag, rmOld, dryRun, ui)
}
