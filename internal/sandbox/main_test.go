package sandbox

import "testing"

// TestMain installs a fail-fast default for GetConfigPaths so that no sandbox
// test can silently touch real user/project config directories. Tests that need
// isolated config paths call withMockConfigPaths(t); tests that deliberately
// exercise the real path resolution call withRealConfigPaths(t).
func TestMain(m *testing.M) {
	GetConfigPaths = func() ConfigPaths { return &failFastConfigPaths{} }
	m.Run()
}

// failFastConfigPaths is the default ConfigPaths installed for tests. Any of
// its methods panicking signals that a test called production code without
// installing an isolated or explicit config-paths implementation.
type failFastConfigPaths struct{}

func (f *failFastConfigPaths) mustMock() {
	panic(
		"GetConfigPaths reached real path resolution; install withMockConfigPaths(t) or withRealConfigPaths(t) in the test",
	)
}

func (f *failFastConfigPaths) UserConfigDir() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) UserCacheDir() string  { f.mustMock(); return "" }
func (f *failFastConfigPaths) UserStateDir() string  { f.mustMock(); return "" }

func (f *failFastConfigPaths) UserOpencodeConfigDir() string {
	f.mustMock()
	return ""
}

func (f *failFastConfigPaths) userEnvFile() string       { f.mustMock(); return "" }
func (f *failFastConfigPaths) userEnvSecretFile() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectConfigDir() string  { f.mustMock(); return "" }
func (f *failFastConfigPaths) projectOpencodeConfigDir() string {
	f.mustMock()
	return ""
}

func (f *failFastConfigPaths) projectEnvFile() string       { f.mustMock(); return "" }
func (f *failFastConfigPaths) projectEnvSecretFile() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) projectDockerfile() string    { f.mustMock(); return "" }
