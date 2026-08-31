package configpaths

import (
	"path/filepath"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
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

func TestUserOpencodeConfigDir(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := Get()
	want := filepath.Join(cfgPaths.UserConfigDir(), ConfigDirName)
	if got := cfgPaths.UserOpencodeConfigDir(); got != want {
		t.Errorf("UserOpencodeConfigDir() = %q, want %q", got, want)
	}
}

func TestUserAgentConfigDir(t *testing.T) {
	WithRealConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	got := Get().UserAgentConfigDir(a)
	want := Get().UserOpencodeConfigDir()
	if got != want {
		t.Errorf("UserAgentConfigDir(opencode) = %q, want %q", got, want)
	}
}

func TestUserEnvFiles(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := Get()
	if got := cfgPaths.UserEnvFile(); got != filepath.Join(cfgPaths.UserConfigDir(), EnvFileName) {
		t.Errorf("UserEnvFile() = %q", got)
	}
	if got := cfgPaths.UserEnvSecretFile(); got != filepath.Join(cfgPaths.UserConfigDir(), EnvSecretFileName) {
		t.Errorf("UserEnvSecretFile() = %q", got)
	}
}

func TestProjectDirsAndFiles(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := Get()

	if got := cfgPaths.ProjectOpencodeConfigDir(); got != filepath.Join(projectConfigDir, ConfigDirName) {
		t.Errorf("ProjectOpencodeConfigDir() = %q", got)
	}
	if got := cfgPaths.ProjectEnvFile(); got != filepath.Join(projectConfigDir, EnvFileName) {
		t.Errorf("ProjectEnvFile() = %q", got)
	}
	if got := cfgPaths.ProjectEnvSecretFile(); got != filepath.Join(projectConfigDir, EnvSecretFileName) {
		t.Errorf("ProjectEnvSecretFile() = %q", got)
	}
	if got := cfgPaths.ProjectDockerfile(); got != filepath.Join(projectConfigDir, DockerFileName) {
		t.Errorf("ProjectDockerfile() = %q", got)
	}
}

func TestMockUserAgentConfigDir(t *testing.T) {
	WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	got := Get().UserAgentConfigDir(a)
	want := filepath.Join(Get().UserConfigDir(), a.ConfigDirName())
	if got != want {
		t.Errorf("mock UserAgentConfigDir(opencode) = %q, want %q", got, want)
	}
}

func TestMockProjectAgentConfigDir(t *testing.T) {
	WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	got := Get().ProjectAgentConfigDir(a)
	want := filepath.Join(Get().ProjectConfigDir(), a.ConfigDirName())
	if got != want {
		t.Errorf("mock ProjectAgentConfigDir(opencode) = %q, want %q", got, want)
	}
}

func TestFailFastUserAgentConfigDirPanics(t *testing.T) {
	assertPanics(t, func() {
		(&failFastConfigPaths{}).UserAgentConfigDir(agentAgent(t))
	})
}

func TestFailFastProjectAgentConfigDirPanics(t *testing.T) {
	assertPanics(t, func() {
		(&failFastConfigPaths{}).ProjectAgentConfigDir(agentAgent(t))
	})
}

func agentAgent(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return a
}

func assertPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected panic, got none")
		}
	}()
	fn()
}
