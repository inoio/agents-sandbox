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

func withMockConfigPaths(t *testing.T) {
	t.Helper()
	orig := GetConfigPaths
	tempDir := t.TempDir()
	GetConfigPaths = func() ConfigPaths { return &mockConfigPaths{baseDir: tempDir} }
	t.Cleanup(func() { GetConfigPaths = orig })
}

// withRealConfigPaths opts a test into the real XDG path resolution instead of
// the fail-fast default installed by TestMain. Only use it when a test
// deliberately exercises the real resolver and isolates its directories via
// t.Setenv, as config_paths_test.go does.
func withRealConfigPaths(t *testing.T) {
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
