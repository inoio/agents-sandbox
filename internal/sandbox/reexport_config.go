package sandbox

import (
	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
)

type ConfigPaths = configpaths.ConfigPaths

// Prefix is kept as a backward-compatible alias for cmd that references sandbox.Prefix.
const Prefix = naming.Prefix

var (
	GetConfigPaths             = configpaths.GetConfigPaths             //nolint:gochecknoglobals // re-export
	InstallFailFastConfigPaths = configpaths.InstallFailFastConfigPaths //nolint:gochecknoglobals // re-export
	WithMockConfigPaths        = configpaths.WithMockConfigPaths        //nolint:gochecknoglobals // re-export
	WithRealConfigPaths        = configpaths.WithRealConfigPaths        //nolint:gochecknoglobals // re-export
)
