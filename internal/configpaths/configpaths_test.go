package configpaths_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

func TestUserDirEnvOverride(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	cfgPaths := configpaths.Get()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got := cfgPaths.UserStateDir(); got != filepath.Join(state, "agents-sandbox") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(state, "agents-sandbox"))
	}
}

func TestUserDirsDefaultToHome(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	cfgPaths := configpaths.Get()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	wantConfig := filepath.Join(home, ".config", "agents-sandbox")
	wantCache := filepath.Join(home, ".cache", "agents-sandbox")
	wantState := filepath.Join(home, ".local", "state", "agents-sandbox")

	if got := cfgPaths.UserConfigDir(); got != wantConfig {
		t.Errorf("UserConfigDir() = %q, want %q", got, wantConfig)
	}
	if got := cfgPaths.UserCacheDir(); got != wantCache {
		t.Errorf("UserCacheDir() = %q, want %q", got, wantCache)
	}
	if got := cfgPaths.UserStateDir(); got != wantState {
		t.Errorf("UserStateDir() = %q, want %q", got, wantState)
	}
}

func TestUserHomeDirReturnsHome(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	if got != home {
		t.Errorf("UserHomeDir() = %q, want %q", got, home)
	}
}

func TestUserHomeDirErrorsWhenHomeUnset(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	t.Setenv("HOME", "")
	if _, err := configpaths.Get().UserHomeDir(); err == nil {
		t.Fatal("UserHomeDir() error = nil, want error when $HOME is unset")
	}
}

func TestUserDirsUsesSeparateEnvVars(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	cfgPaths := configpaths.Get()
	config := t.TempDir()
	cache := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", state)

	if got := cfgPaths.UserConfigDir(); got != filepath.Join(config, "agents-sandbox") {
		t.Errorf("UserConfigDir() = %q, want %q", got, filepath.Join(config, "agents-sandbox"))
	}
	if got := cfgPaths.UserCacheDir(); got != filepath.Join(cache, "agents-sandbox") {
		t.Errorf("UserCacheDir() = %q, want %q", got, filepath.Join(cache, "agents-sandbox"))
	}
	if got := cfgPaths.UserStateDir(); got != filepath.Join(state, "agents-sandbox") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(state, "agents-sandbox"))
	}
}

func TestUserDirIgnoresRelativeEnv(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	cfgPaths := configpaths.Get()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	want := filepath.Join(home, ".config", "agents-sandbox")
	if got := cfgPaths.UserConfigDir(); got != want {
		t.Errorf("UserConfigDir() = %q, want %q", got, want)
	}
}

// TestUserSlugConfigDir pins the real (unmocked) per-slug user-config path, the
// accessor viperconfig uses in place of its former inline Join.
func TestUserSlugConfigDir(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	got := configpaths.Get().UserSlugConfigDir("widget-bbb")
	want := filepath.Join("/xdg/config", "agents-sandbox", "widget-bbb")
	if got != want {
		t.Errorf("UserSlugConfigDir() = %q, want %q", got, want)
	}
}

func TestProjectDirsAndFiles(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	cfgPaths := configpaths.Get()
	want := filepath.Join(configpaths.ProjectConfigDir, configpaths.DockerFileName)
	if got := cfgPaths.ProjectDockerfile(); got != want {
		t.Errorf("ProjectDockerfile() = %q, want %q", got, want)
	}
}

func TestUserDataDirHonorsXDGDataHome(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	want := filepath.Join("/tmp/xdg-data", "agents-sandbox")
	if got := configpaths.Get().UserDataDir(); got != want {
		t.Errorf("UserDataDir() = %q, want %q", got, want)
	}
}

func TestUserDataDirDefaultsToLocalShare(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	want := filepath.Join(home, ".local", "share", "agents-sandbox")
	if got := configpaths.Get().UserDataDir(); got != want {
		t.Errorf("UserDataDir() = %q, want %q", got, want)
	}
}

func TestProjectConfigDirAbs(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := configpaths.Get().ProjectConfigDirAbs(); got != filepath.Join(wd, ".agents-sandbox") {
		t.Errorf("ProjectConfigDirAbs() = %q, want %q", got, filepath.Join(wd, ".agents-sandbox"))
	}
}

// TestWithRealConfigPathsPinsCwd pins the helper's contract: opting into the
// real resolver also pins the CWD to a controlled, layout-free directory.
// Starting inside a bare checkout's worktree is what makes the test fail if the
// pin is dropped — an ordinary checkout passes either way — so the regression
// this addresses is caught by ordinary CI, not only by a bare checkout.
func TestWithRealConfigPathsPinsCwd(t *testing.T) {
	pinHostDirs(t)
	root := writeBareCheckout(t)
	worktree := filepath.Join(root, "main")
	t.Chdir(worktree)

	// Sanity: the ambient layout is exactly the one the pin must neutralize.
	if base := configpaths.ProjectConfigBase(worktree); base == configpaths.ProjectConfigDir {
		t.Fatalf("fixture %q is not a bare checkout; the test cannot prove the pin", worktree)
	}

	configpathstest.WithRealConfigPaths(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if wd == worktree {
		t.Errorf("WithRealConfigPaths left the CWD in the bare worktree %q", worktree)
	}
	if got := configpaths.Get().ProjectConfigDir(); got != configpaths.ProjectConfigDir {
		t.Errorf(
			"ProjectConfigDir() = %q, want the relative %q after WithRealConfigPaths",
			got,
			configpaths.ProjectConfigDir,
		)
	}
	if got := configpaths.Get().ProjectConfigDirAbs(); got != filepath.Join(wd, configpaths.ProjectConfigDir) {
		t.Errorf(
			"ProjectConfigDirAbs() = %q, want %q under the pinned CWD",
			got,
			filepath.Join(wd, configpaths.ProjectConfigDir),
		)
	}
}

func TestProjectConfigDirAbsFallsBackWhenCwdUnreadable(t *testing.T) {
	configpathstest.WithRealConfigPaths(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if got := configpaths.Get().ProjectConfigDirAbs(); got != ".agents-sandbox" {
		t.Errorf("ProjectConfigDirAbs() with an unreadable cwd = %q, want the relative fallback", got)
	}
}

func TestMSBHomeUnderDataDir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	got := configpaths.MSBHome()
	want := filepath.Join(configpaths.Get().UserDataDir(), "msb")
	if got != want {
		t.Errorf("MSBHome() = %q, want %q", got, want)
	}
}
