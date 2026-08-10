package sandbox

import "testing"

// TestMain installs a fail-fast default for GetConfigPaths so that no sandbox
// test can silently touch real user/project config directories. Tests that need
// isolated config paths call WithMockConfigPaths(t); tests that deliberately
// exercise the real path resolution call WithRealConfigPaths(t).
func TestMain(m *testing.M) {
	GetConfigPaths = FailFastConfigPaths
	m.Run()
}
