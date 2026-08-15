package configpaths

import (
	"path/filepath"
	"testing"
)

func TestUserDirEnvOverride(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := Get()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got := cfgPaths.UserStateDir(); got != filepath.Join(state, "opencode-sandbox") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(state, "opencode-sandbox"))
	}
}

func TestUserDirsDefaultToHome(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := Get()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	wantConfig := filepath.Join(home, ".config", "opencode-sandbox")
	wantCache := filepath.Join(home, ".cache", "opencode-sandbox")
	wantState := filepath.Join(home, ".local", "state", "opencode-sandbox")

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
	cfgPaths := Get()
	config := t.TempDir()
	cache := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", state)

	if got := cfgPaths.UserConfigDir(); got != filepath.Join(config, "opencode-sandbox") {
		t.Errorf("UserConfigDir() = %q, want %q", got, filepath.Join(config, "opencode-sandbox"))
	}
	if got := cfgPaths.UserCacheDir(); got != filepath.Join(cache, "opencode-sandbox") {
		t.Errorf("UserCacheDir() = %q, want %q", got, filepath.Join(cache, "opencode-sandbox"))
	}
	if got := cfgPaths.UserStateDir(); got != filepath.Join(state, "opencode-sandbox") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(state, "opencode-sandbox"))
	}
}

func TestUserDirIgnoresRelativeEnv(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := Get()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	want := filepath.Join(home, ".config", "opencode-sandbox")
	if got := cfgPaths.UserConfigDir(); got != want {
		t.Errorf("UserConfigDir() = %q, want %q", got, want)
	}
}

func TestEnvSecretYAMLPaths(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c := &realConfigPaths{}
	if got := c.UserEnvSecretYAMLFile(); got != filepath.Join(c.UserConfigDir(), envSecretYAMLFileName) {
		t.Errorf("userEnvSecretYAMLFile() = %q", got)
	}
	if got := c.ProjectEnvSecretYAMLFile(); got != filepath.Join(c.ProjectConfigDir(), envSecretYAMLFileName) {
		t.Errorf("projectEnvSecretYAMLFile() = %q, want %q", got, c.ProjectConfigDir())
	}
}
