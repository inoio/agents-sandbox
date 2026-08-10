package sandbox

import (
	"path/filepath"
	"testing"
)

func TestXdgDirEnvOverride(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got := XdgStateDir(); got != filepath.Join(state, "opencode-msb") {
		t.Errorf("XdgStateDir() = %q, want %q", got, filepath.Join(state, "opencode-msb"))
	}
}

func TestXdgDirsDefaultToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	wantConfig := filepath.Join(home, ".config", "opencode-msb")
	wantCache := filepath.Join(home, ".cache", "opencode-msb")
	wantState := filepath.Join(home, ".local", "state", "opencode-msb")

	if got := XdgConfigDir(); got != wantConfig {
		t.Errorf("XdgConfigDir() = %q, want %q", got, wantConfig)
	}
	if got := XdgCacheDir(); got != wantCache {
		t.Errorf("XdgCacheDir() = %q, want %q", got, wantCache)
	}
	if got := XdgStateDir(); got != wantState {
		t.Errorf("XdgStateDir() = %q, want %q", got, wantState)
	}
}

func TestXdgDirsUsesSeparateEnvVars(t *testing.T) {
	config := t.TempDir()
	cache := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", state)

	if got := XdgConfigDir(); got != filepath.Join(config, "opencode-msb") {
		t.Errorf("XdgConfigDir() = %q, want %q", got, filepath.Join(config, "opencode-msb"))
	}
	if got := XdgCacheDir(); got != filepath.Join(cache, "opencode-msb") {
		t.Errorf("XdgCacheDir() = %q, want %q", got, filepath.Join(cache, "opencode-msb"))
	}
	if got := XdgStateDir(); got != filepath.Join(state, "opencode-msb") {
		t.Errorf("XdgStateDir() = %q, want %q", got, filepath.Join(state, "opencode-msb"))
	}
}

func TestXdgDirIgnoresRelativeEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	want := filepath.Join(home, ".config", "opencode-msb")
	if got := XdgConfigDir(); got != want {
		t.Errorf("XdgConfigDir() = %q, want %q", got, want)
	}
}

func TestStateFileAbsoluteUnderXdgStateDir(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("HOME", t.TempDir())
	got := stateFile("proj")
	want := filepath.Join(state, "opencode-msb", "proj", "state.yaml")
	if got != want {
		t.Errorf("StateFile() = %q, want %q", got, want)
	}
}

func TestEnvDirUsesCache(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", t.TempDir())
	got := envDir()
	want := filepath.Join(cache, "opencode-msb")
	if got != want {
		t.Errorf("envDir() = %q, want %q", got, want)
	}
}
