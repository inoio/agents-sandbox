package configpaths

import (
	"path/filepath"
	"testing"
)

// TestMockConfigPaths exercises the WithMockConfigPaths helper that other
// packages' tests rely on, verifying each method resolves to a path rooted in
// the isolated temp base directory rather than the real XDG locations.
func TestMockConfigPaths(t *testing.T) {
	WithMockConfigPaths(t)
	cfgPaths := Get()
	m := Get().(*mockConfigPaths)
	base := m.baseDir

	if got := cfgPaths.UserConfigDir(); got != filepath.Join(base, "userconfig") {
		t.Errorf("UserConfigDir() = %q, want %q", got, filepath.Join(base, "userconfig"))
	}
	if got := cfgPaths.UserCacheDir(); got != filepath.Join(base, "usercache") {
		t.Errorf("UserCacheDir() = %q, want %q", got, filepath.Join(base, "usercache"))
	}
	if got := cfgPaths.UserStateDir(); got != filepath.Join(base, "userstate") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(base, "userstate"))
	}
	if got := cfgPaths.UserOpencodeConfigDir(); got != filepath.Join(base, "userconfig", "opencode") {
		t.Errorf("UserOpencodeConfigDir() = %q, want %q", got, filepath.Join(base, "userconfig", "opencode"))
	}
	if got := cfgPaths.ProjectConfigDir(); got != filepath.Join(base, "projectconfig") {
		t.Errorf("ProjectConfigDir() = %q, want %q", got, filepath.Join(base, "projectconfig"))
	}
	if got := cfgPaths.ProjectOpencodeConfigDir(); got != filepath.Join(base, "projectconfig", "opencode") {
		t.Errorf("ProjectOpencodeConfigDir() = %q, want %q", got, filepath.Join(base, "projectconfig", "opencode"))
	}
	if got := cfgPaths.UserEnvFile(); got != filepath.Join(base, "userconfig", envFileName) {
		t.Errorf("UserEnvFile() = %q, want %q", got, filepath.Join(base, "userconfig", envFileName))
	}
	if got := cfgPaths.UserEnvSecretFile(); got != filepath.Join(base, "userconfig", envSecretFileName) {
		t.Errorf("UserEnvSecretFile() = %q, want %q", got, filepath.Join(base, "userconfig", envSecretFileName))
	}
	if got := cfgPaths.UserEnvSecretYAMLFile(); got != filepath.Join(base, "userconfig", envSecretYAMLFileName) {
		t.Errorf("UserEnvSecretYAMLFile() = %q, want %q", got, filepath.Join(base, "userconfig", envSecretYAMLFileName))
	}
	if got := cfgPaths.ProjectEnvFile(); got != filepath.Join(base, "projectconfig", envFileName) {
		t.Errorf("ProjectEnvFile() = %q, want %q", got, filepath.Join(base, "projectconfig", envFileName))
	}
	if got := cfgPaths.ProjectEnvSecretFile(); got != filepath.Join(base, "projectconfig", envSecretFileName) {
		t.Errorf("ProjectEnvSecretFile() = %q, want %q", got, filepath.Join(base, "projectconfig", envSecretFileName))
	}
	if got := cfgPaths.ProjectEnvSecretYAMLFile(); got != filepath.Join(base, "projectconfig", envSecretYAMLFileName) {
		t.Errorf(
			"ProjectEnvSecretYAMLFile() = %q, want %q",
			got,
			filepath.Join(base, "projectconfig", envSecretYAMLFileName),
		)
	}
	if got := cfgPaths.ProjectDockerfile(); got != filepath.Join(base, "projectconfig", dockerfileName) {
		t.Errorf("ProjectDockerfile() = %q, want %q", got, filepath.Join(base, "projectconfig", dockerfileName))
	}
}

// TestWithMockConfigPathsInstallsMock verifies the helper installs a working
// mockConfigPaths factory.
func TestWithMockConfigPathsInstallsMock(t *testing.T) {
	WithMockConfigPaths(t)
	if _, ok := Get().(*mockConfigPaths); !ok {
		t.Fatal("Get did not install a mockConfigPaths")
	}
}
