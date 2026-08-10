package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type mockConfigPaths struct {
	baseDir string
}

// InstallFailFastConfigPaths binds the sandbox config-path factory to its
// fail-fast default. Exported so packages dispatching through the factory —
// e.g. viperconfig, cmd/opencode-msb — can list it in their own InitMocks call.
var InstallFailFastConfigPaths = func() { GetConfigPaths = FailFastConfigPaths } //nolint:gochecknoglobals // test hook, aligned with GetConfigPaths factory

// FailFastConfigPaths is the ConfigPaths installed by default under tests: any
// method call panics to signal a test reached real path resolution without opting in.
func FailFastConfigPaths() ConfigPaths { return &failFastConfigPaths{} }

func WithMockConfigPaths(t *testing.T) {
	t.Helper()
	orig := GetConfigPaths
	tempDir := t.TempDir()
	GetConfigPaths = func() ConfigPaths { return &mockConfigPaths{baseDir: tempDir} }
	t.Cleanup(func() { GetConfigPaths = orig })
}

// WithRealConfigPaths opts a test into the real XDG path resolution instead of
// the fail-fast default installed by TestMain. Only use it when a test
// deliberately exercises the real resolver and isolates its directories via
// t.Setenv, as config_paths_test.go does.
func WithRealConfigPaths(t *testing.T) {
	t.Helper()
	orig := GetConfigPaths
	GetConfigPaths = func() ConfigPaths { return &realConfigPaths{} }
	t.Cleanup(func() { GetConfigPaths = orig })
}

func ensureMockConfigDirectory(path string) string {
	err := os.MkdirAll(path, 0o750)
	if err != nil {
		fmt.Fprintln(os.Stdout, err)
	}
	return path
}

func (m *mockConfigPaths) UserConfigDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "userconfig"))
}

func (m *mockConfigPaths) UserCacheDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "usercache"))
}

func (m *mockConfigPaths) UserStateDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "userstate"))
}

func (m *mockConfigPaths) UserOpencodeConfigDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.UserConfigDir(), "opencode"))
}

func (m *mockConfigPaths) ProjectConfigDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "projectconfig"))
}

func (m *mockConfigPaths) userEnvFile() string {
	return filepath.Join(m.UserConfigDir(), envFileName)
}

func (m *mockConfigPaths) userEnvSecretFile() string {
	return filepath.Join(m.UserConfigDir(), envSecretFileName)
}

func (m *mockConfigPaths) projectOpencodeConfigDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.ProjectConfigDir(), "opencode"))
}

func (m *mockConfigPaths) projectEnvFile() string {
	return filepath.Join(m.ProjectConfigDir(), envFileName)
}

func (m *mockConfigPaths) projectEnvSecretFile() string {
	return filepath.Join(m.ProjectConfigDir(), envSecretFileName)
}

func (m *mockConfigPaths) projectDockerfile() string {
	return filepath.Join(m.ProjectConfigDir(), dockerfileName)
}

// failFastConfigPaths is the default ConfigPaths installed for tests. Any of
// its methods panicking signals that a test called production code without
// installing an isolated or explicit config-paths implementation.
type failFastConfigPaths struct{}

func (f *failFastConfigPaths) mustMock() {
	panic(
		"GetConfigPaths reached real path resolution; install WithMockConfigPaths(t) or WithRealConfigPaths(t) in the test",
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
