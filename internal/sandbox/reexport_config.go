package sandbox

import "gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"

type ConfigPaths = configpaths.ConfigPaths

var (
	GetConfigPaths             = configpaths.GetConfigPaths             //nolint:gochecknoglobals // re-export
	InstallFailFastConfigPaths = configpaths.InstallFailFastConfigPaths //nolint:gochecknoglobals // re-export
	WithMockConfigPaths        = configpaths.WithMockConfigPaths        //nolint:gochecknoglobals // re-export
	WithRealConfigPaths        = configpaths.WithRealConfigPaths        //nolint:gochecknoglobals // re-export
)
