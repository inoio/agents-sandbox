package configpaths

import (
	"path/filepath"
	"testing"
)

func TestUserDirEnvOverride(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := GetConfigPaths()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got := cfgPaths.UserStateDir(); got != filepath.Join(state, "opencode-msb") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(state, "opencode-msb"))
	}
}

func TestUserDirsDefaultToHome(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := GetConfigPaths()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	wantConfig := filepath.Join(home, ".config", "opencode-msb")
	wantCache := filepath.Join(home, ".cache", "opencode-msb")
	wantState := filepath.Join(home, ".local", "state", "opencode-msb")

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

func TestUserDirsUsesSeparateEnvVars(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := GetConfigPaths()
	config := t.TempDir()
	cache := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", state)

	if got := cfgPaths.UserConfigDir(); got != filepath.Join(config, "opencode-msb") {
		t.Errorf("UserConfigDir() = %q, want %q", got, filepath.Join(config, "opencode-msb"))
	}
	if got := cfgPaths.UserCacheDir(); got != filepath.Join(cache, "opencode-msb") {
		t.Errorf("UserCacheDir() = %q, want %q", got, filepath.Join(cache, "opencode-msb"))
	}
	if got := cfgPaths.UserStateDir(); got != filepath.Join(state, "opencode-msb") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(state, "opencode-msb"))
	}
}

func TestUserDirIgnoresRelativeEnv(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := GetConfigPaths()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	want := filepath.Join(home, ".config", "opencode-msb")
	if got := cfgPaths.UserConfigDir(); got != want {
		t.Errorf("UserConfigDir() = %q, want %q", got, want)
	}
}
