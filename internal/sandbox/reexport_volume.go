package sandbox

// Re-exported volume module symbols preserve the public API of the sandbox
// core so that cmd/opencode-msb and any external consumers continue to work
// without changing their import paths.

import "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/volume"

// ListVolumes is re-exported from the volume module.
//
//nolint:gochecknoglobals // re-export preserves sandbox core public API
var ListVolumes = volume.ListVolumes

// VolumeInfo is re-exported from the volume module.
type VolumeInfo = volume.VolumeInfo

//nolint:gochecknoglobals // re-export preserves sandbox core public API
var (
	CmdMigrate = volume.CmdMigrate
	CmdReset   = volume.CmdReset
	CmdEdit    = volume.CmdEdit
)
