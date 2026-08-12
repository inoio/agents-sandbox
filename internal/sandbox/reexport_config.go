package sandbox

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
)

// Re-exported configpaths module symbols preserve the public API of the
// sandbox core so that cmd/opencode-msb continues to compile without changing
// its import paths.

type ConfigPaths = configpaths.ConfigPaths

// Prefix is kept as a backward-compatible alias for cmd that references sandbox.Prefix.
const Prefix = naming.Prefix

// GetConfigPaths re-exports the configpaths module's GetConfigPaths factory.
func GetConfigPaths() configpaths.ConfigPaths {
	return configpaths.GetConfigPaths()
}

// InstallFailFastConfigPaths re-exports the configpaths module's InstallFailFastConfigPaths.
func InstallFailFastConfigPaths() {
	configpaths.InstallFailFastConfigPaths()
}

// WithMockConfigPaths re-exports the configpaths module's WithMockConfigPaths.
func WithMockConfigPaths(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
}

// WithRealConfigPaths re-exports the configpaths module's WithRealConfigPaths.
func WithRealConfigPaths(t *testing.T) {
	configpaths.WithRealConfigPaths(t)
}
