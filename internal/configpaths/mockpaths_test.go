package configpaths

import (
	"errors"
	"os"
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
	if got := cfgPaths.UserSlugConfigDir("widget-bbb"); got != filepath.Join(base, "userconfig", "widget-bbb") {
		t.Errorf(
			"UserSlugConfigDir() = %q, want %q",
			got,
			filepath.Join(base, "userconfig", "widget-bbb"),
		)
	}
	if got := cfgPaths.UserCacheDir(); got != filepath.Join(base, "usercache") {
		t.Errorf("UserCacheDir() = %q, want %q", got, filepath.Join(base, "usercache"))
	}
	if got := cfgPaths.UserStateDir(); got != filepath.Join(base, "userstate") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(base, "userstate"))
	}
	home, err := cfgPaths.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	if home != filepath.Join(base, "home") {
		t.Errorf("UserHomeDir() = %q, want %q", home, filepath.Join(base, "home"))
	}
	if got := os.Getenv("HOME"); got != filepath.Join(base, "home") {
		t.Errorf("$HOME = %q, want %q (WithMockConfigPaths must redirect HOME)", got, filepath.Join(base, "home"))
	}
	if got := cfgPaths.ProjectConfigDirAbs(); got != filepath.Join(base, "projectconfig") {
		t.Errorf("ProjectConfigDirAbs() = %q, want %q", got, filepath.Join(base, "projectconfig"))
	}
	if got := cfgPaths.ProjectConfigDir(); got != filepath.Join(base, "projectconfig") {
		t.Errorf("ProjectConfigDir() = %q, want %q", got, filepath.Join(base, "projectconfig"))
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
	if got := cfgPaths.IgnoredWorktreeConfigDir(); got != "" {
		t.Errorf("IgnoredWorktreeConfigDir() = %q, want empty; the mock cannot express a bare base", got)
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

// TestWithMockConfigPathsHomeError verifies the error variant returns the
// injected home-resolution error while keeping the other paths mocked.
func TestWithMockConfigPathsHomeError(t *testing.T) {
	wantErr := errors.New("home unavailable")
	WithMockConfigPathsHomeError(t, wantErr)
	if _, err := Get().UserHomeDir(); !errors.Is(err, wantErr) {
		t.Fatalf("UserHomeDir() error = %v, want %v", err, wantErr)
	}
	if got := Get().UserConfigDir(); got == "" {
		t.Error("UserConfigDir() = empty, want a mocked path")
	}
}

// TestWithMockConfigPathsHomeErrorRedirectsHome pins the error variant's
// $HOME redirect: a resolver that bypasses the seam must still be confined to
// the mock tree rather than reading the developer's real home.
func TestWithMockConfigPathsHomeErrorRedirectsHome(t *testing.T) {
	WithMockConfigPathsHomeError(t, errors.New("home unavailable"))
	base := Get().(*mockConfigPaths).baseDir
	if got, want := os.Getenv("HOME"), filepath.Join(base, "home"); got != want {
		t.Errorf("$HOME = %q, want %q (WithMockConfigPathsHomeError must redirect HOME)", got, want)
	}
}

// TestWithMockConfigPathsHomeErrorRestoresGet pins the helper's cleanup: once
// the installing test returns, Get must be back to the factory the helper
// replaced, so the mock does not leak into whichever test runs next.
func TestWithMockConfigPathsHomeErrorRestoresGet(t *testing.T) {
	t.Run("installed", func(t *testing.T) {
		WithMockConfigPathsHomeError(t, errors.New("home unavailable"))
		if _, ok := Get().(*mockConfigPaths); !ok {
			t.Fatal("Get did not install a mockConfigPaths")
		}
	})
	if _, ok := Get().(*mockConfigPaths); ok {
		t.Error(
			"Get still returns a mockConfigPaths after WithMockConfigPathsHomeError's cleanup ran; " +
				"the replaced factory was not restored",
		)
	}
}

func TestCreateDirCreatesAndReturnsTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "userconfig", "opencode")

	if got := CreateDir(dir); got != dir {
		t.Errorf("CreateDir() = %q, want %q", got, dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("CreateDir did not create %q (stat err = %v)", dir, err)
	}
}
