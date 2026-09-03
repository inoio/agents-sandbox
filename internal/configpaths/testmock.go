package configpaths

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

type mockConfigPaths struct {
	baseDir string
}

// InstallFailFastConfigPaths binds the sandbox config-path factory to its
// fail-fast default. Exported so packages dispatching through the factory —
// e.g. viperconfig, cmd/opencode-sandbox — can list it in their own InitMocks call.
var InstallFailFastConfigPaths = func() { Get = FailFastConfigPaths } //nolint:gochecknoglobals // test hook, aligned with Get factory

// FailFastConfigPaths is the ConfigPaths installed by default under tests: any
// method call panics to signal a test reached real path resolution without opting in.
func FailFastConfigPaths() ConfigPaths { return &failFastConfigPaths{} }

func WithMockConfigPaths(t *testing.T) {
	t.Helper()
	orig := Get
	tempDir := t.TempDir()
	Get = func() ConfigPaths { return &mockConfigPaths{baseDir: tempDir} }
	t.Cleanup(func() { Get = orig })
}

// WithRealConfigPaths opts a test into the real XDG path resolution instead of
// the fail-fast default installed by TestMain. Only use it when a test
// deliberately exercises the real resolver and isolates its directories via
// t.Setenv, as config_paths_test.go does.
func WithRealConfigPaths(t *testing.T) {
	t.Helper()
	orig := Get
	Get = func() ConfigPaths { return &realConfigPaths{} }
	t.Cleanup(func() { Get = orig })
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

func (m *mockConfigPaths) UserAgentConfigDir(a agent.Agent) string {
	return ensureMockConfigDirectory(filepath.Join(m.UserConfigDir(), a.ConfigDirName()))
}

func (m *mockConfigPaths) ProjectConfigDir() string {
	return ensureMockConfigDirectory(filepath.Join(m.baseDir, "projectconfig"))
}

func (m *mockConfigPaths) UserEnvFile() string {
	return filepath.Join(m.UserConfigDir(), envFileName)
}

func (m *mockConfigPaths) UserEnvSecretFile() string {
	return filepath.Join(m.UserConfigDir(), envSecretFileName)
}

func (m *mockConfigPaths) ProjectAgentConfigDir(a agent.Agent) string {
	return ensureMockConfigDirectory(filepath.Join(m.ProjectConfigDir(), a.ConfigDirName()))
}

func (m *mockConfigPaths) ProjectEnvFile() string {
	return filepath.Join(m.ProjectConfigDir(), envFileName)
}

func (m *mockConfigPaths) ProjectEnvSecretFile() string {
	return filepath.Join(m.ProjectConfigDir(), envSecretFileName)
}

func (m *mockConfigPaths) UserEnvSecretYAMLFile() string {
	return filepath.Join(m.UserConfigDir(), envSecretYAMLFileName)
}

func (m *mockConfigPaths) ProjectEnvSecretYAMLFile() string {
	return filepath.Join(m.ProjectConfigDir(), envSecretYAMLFileName)
}

func (m *mockConfigPaths) ProjectDockerfile() string {
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

func (f *failFastConfigPaths) UserAgentConfigDir(_ agent.Agent) string {
	f.mustMock()
	return ""
}

func (f *failFastConfigPaths) UserEnvFile() string       { f.mustMock(); return "" }
func (f *failFastConfigPaths) UserEnvSecretFile() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectConfigDir() string  { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectAgentConfigDir(_ agent.Agent) string {
	f.mustMock()
	return ""
}

func (f *failFastConfigPaths) ProjectEnvFile() string       { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectEnvSecretFile() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectEnvSecretYAMLFile() string {
	f.mustMock()
	return ""
}
func (f *failFastConfigPaths) UserEnvSecretYAMLFile() string { f.mustMock(); return "" }
func (f *failFastConfigPaths) ProjectDockerfile() string     { f.mustMock(); return "" }
