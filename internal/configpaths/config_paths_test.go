package configpaths

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestUserDirEnvOverride(t *testing.T) {
	WithRealConfigPaths(t)
	cfgPaths := Get()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got := cfgPaths.UserStateDir(); got != filepath.Join(state, "agents-sandbox") {
		t.Errorf("UserStateDir() = %q, want %q", got, filepath.Join(state, "agents-sandbox"))
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
	WithRealConfigPaths(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := Get().UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	if got != home {
		t.Errorf("UserHomeDir() = %q, want %q", got, home)
	}
}

func TestUserHomeDirErrorsWhenHomeUnset(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("HOME", "")
	if _, err := Get().UserHomeDir(); err == nil {
		t.Fatal("UserHomeDir() error = nil, want error when $HOME is unset")
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
	WithRealConfigPaths(t)
	cfgPaths := Get()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	want := filepath.Join(home, ".config", "agents-sandbox")
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

// TestUserSlugConfigDir pins the real (unmocked) per-slug user-config path, the
// accessor viperconfig uses in place of its former inline Join.
func TestUserSlugConfigDir(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	got := Get().UserSlugConfigDir("widget-bbb")
	want := filepath.Join("/xdg/config", "agents-sandbox", "widget-bbb")
	if got != want {
		t.Errorf("UserSlugConfigDir() = %q, want %q", got, want)
	}
}

func TestUserAgentConfigDir(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("HOME", t.TempDir())
	a, _ := agent.Lookup("opencode")
	got, err := Get().UserAgentConfigDir(a)
	if err != nil {
		t.Fatalf("UserAgentConfigDir(opencode) error = %v", err)
	}
	want := filepath.Join(Get().UserConfigDir(), a.ConfigDirName())
	if got != want {
		t.Errorf("UserAgentConfigDir(opencode) = %q, want %q", got, want)
	}
}

func TestRealAgentConfigDirCandidates(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	oc2, _ := agent.Lookup("opencode2")
	want := []string{
		"/xdg/config/agents-sandbox/opencode2",
		"/xdg/config/agents-sandbox/opencode",
	}
	userCandidates, err := Get().UserAgentConfigDirCandidates(oc2)
	if err != nil {
		t.Fatalf("UserAgentConfigDirCandidates error = %v", err)
	}
	if !slices.Equal(userCandidates, want) {
		t.Errorf("user candidates = %v, want %v", userCandidates, want)
	}
	wantProject := []string{
		filepath.Join(Get().ProjectConfigDirAbs(), "opencode2"),
		filepath.Join(Get().ProjectConfigDirAbs(), "opencode"),
	}
	projectCandidates, err := Get().ProjectAgentConfigDirCandidates(oc2)
	if err != nil {
		t.Fatalf("ProjectAgentConfigDirCandidates error = %v", err)
	}
	if !slices.Equal(projectCandidates, wantProject) {
		t.Errorf("project candidates = %v, want %v", projectCandidates, wantProject)
	}
}

func TestUserEnvFiles(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("HOME", t.TempDir())
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
	a, _ := agent.Lookup("opencode")

	projectAgentDir, err := cfgPaths.ProjectAgentConfigDir(a)
	if err != nil {
		t.Fatalf("ProjectAgentConfigDir() error = %v", err)
	}
	if projectAgentDir != filepath.Join(cfgPaths.ProjectConfigDirAbs(), a.ConfigDirName()) {
		t.Errorf("ProjectAgentConfigDir() = %q", projectAgentDir)
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

func TestUserDataDirHonorsXDGDataHome(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	want := filepath.Join("/tmp/xdg-data", "agents-sandbox")
	if got := Get().UserDataDir(); got != want {
		t.Errorf("UserDataDir() = %q, want %q", got, want)
	}
}

func TestUserDataDirDefaultsToLocalShare(t *testing.T) {
	WithRealConfigPaths(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	want := filepath.Join(home, ".local", "share", "agents-sandbox")
	if got := Get().UserDataDir(); got != want {
		t.Errorf("UserDataDir() = %q, want %q", got, want)
	}
}

func TestMockUserDataDir(t *testing.T) {
	WithMockConfigPaths(t)
	got := Get().UserDataDir()
	if filepath.Base(got) != "userdata" {
		t.Errorf("mock UserDataDir() = %q, want a path ending in userdata", got)
	}
}

func TestFailFastUserDataDirPanics(t *testing.T) {
	assertPanics(t, func() {
		(&failFastConfigPaths{}).UserDataDir()
	})
}

func TestFailFastProjectEnvSecretYAMLFilePanics(t *testing.T) {
	assertPanics(t, func() {
		(&failFastConfigPaths{}).ProjectEnvSecretYAMLFile()
	})
}

func TestFailFastUserEnvSecretYAMLFilePanics(t *testing.T) {
	assertPanics(t, func() {
		(&failFastConfigPaths{}).UserEnvSecretYAMLFile()
	})
}

func TestMockUserAgentConfigDir(t *testing.T) {
	WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	got, err := Get().UserAgentConfigDir(a)
	if err != nil {
		t.Fatalf("mock UserAgentConfigDir(opencode) error = %v", err)
	}
	want := filepath.Join(Get().UserConfigDir(), a.ConfigDirName())
	if got != want {
		t.Errorf("mock UserAgentConfigDir(opencode) = %q, want %q", got, want)
	}
}

func TestMockProjectAgentConfigDir(t *testing.T) {
	WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	got, err := Get().ProjectAgentConfigDir(a)
	if err != nil {
		t.Fatalf("mock ProjectAgentConfigDir(opencode) error = %v", err)
	}
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

func TestValidConfigDirName(t *testing.T) {
	got, err := validConfigDirName("opencode2", "opencode2")
	if err != nil {
		t.Fatalf("validConfigDirName(valid) error = %v", err)
	}
	if got != "opencode2" {
		t.Errorf("validConfigDirName(valid) = %q, want opencode2", got)
	}
	for _, bad := range []string{"", ".", "..", "/", "//", "./", "a/b", "a/../b", "/abs", "../sibling", "foo/"} {
		t.Run("reject "+bad, func(t *testing.T) {
			if _, err := validConfigDirName("test", bad); err == nil {
				t.Errorf("validConfigDirName(%q) error = nil, want error", bad)
			}
		})
	}
}

// invalidConfigDirAgent is a profile whose config-dir name is not a single
// clean path element, used to exercise the accessors' error path.
type invalidConfigDirAgent struct{}

func (invalidConfigDirAgent) Name() string               { return "bad-profile" }
func (invalidConfigDirAgent) ConfigDirName() string      { return "../escape" }
func (invalidConfigDirAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// invalidAliasConfigDirAgent has a valid primary dir but a bad alias, so alias
// name validation is exercised through the candidate accessors.
type invalidAliasConfigDirAgent struct{ invalidConfigDirAgent }

func (invalidAliasConfigDirAgent) ConfigDirName() string      { return "valid-primary" }
func (invalidAliasConfigDirAgent) ConfigDirAliases() []string { return []string{"ok", ".."} }

func TestAgentConfigDirAccessorsErrorOnInvalidName(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bad := invalidConfigDirAgent{}

	tests := []struct {
		name string
		call func() error
	}{
		{"UserAgentConfigDir", func() error { _, err := Get().UserAgentConfigDir(bad); return err }},
		{"UserAgentConfigDirCandidates", func() error {
			_, err := Get().UserAgentConfigDirCandidates(bad)
			return err
		}},
		{"ProjectAgentConfigDir", func() error { _, err := Get().ProjectAgentConfigDir(bad); return err }},
		{"ProjectAgentConfigDirCandidates", func() error {
			_, err := Get().ProjectAgentConfigDirCandidates(bad)
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatalf("%s with an invalid config-dir name = nil error, want error", tc.name)
			}
		})
	}
}

func TestAgentConfigDirCandidatesErrorOnInvalidAlias(t *testing.T) {
	WithRealConfigPaths(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := Get().UserAgentConfigDirCandidates(invalidAliasConfigDirAgent{}); err == nil {
		t.Fatal("UserAgentConfigDirCandidates with an invalid alias = nil error, want error")
	}
}

func TestMustDirPanicsOnErrorAndReturnsValue(t *testing.T) {
	if got := MustDir("/x/opencode", nil); got != "/x/opencode" {
		t.Errorf("MustDir(valid) = %q, want /x/opencode", got)
	}
	assertPanics(t, func() { MustDir("", errors.New("invalid config dir name")) })
}

func TestMustDirsPanicsOnErrorAndReturnsValue(t *testing.T) {
	dirs := []string{"/x/opencode"}
	if got := MustDirs(dirs, nil); !slices.Equal(got, dirs) {
		t.Errorf("MustDirs(valid) = %v, want %v", got, dirs)
	}
	assertPanics(t, func() { MustDirs(nil, errors.New("invalid config dir name")) })
}

func TestMockAgentConfigDirAccessorsErrorOnInvalidName(t *testing.T) {
	WithMockConfigPaths(t)
	bad := invalidConfigDirAgent{}
	if _, err := Get().UserAgentConfigDir(bad); err == nil {
		t.Error("mock UserAgentConfigDir with an invalid name = nil error, want error")
	}
	if _, err := Get().ProjectAgentConfigDir(bad); err == nil {
		t.Error("mock ProjectAgentConfigDir with an invalid name = nil error, want error")
	}
}

func TestProjectConfigDirAbs(t *testing.T) {
	WithRealConfigPaths(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := Get().ProjectConfigDirAbs(); got != filepath.Join(wd, ".agents-sandbox") {
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
	if base, _ := resolveProjectConfig(worktree); base == projectConfigDir {
		t.Fatalf("fixture %q is not a bare checkout; the test cannot prove the pin", worktree)
	}

	WithRealConfigPaths(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if wd == worktree {
		t.Errorf("WithRealConfigPaths left the CWD in the bare worktree %q", worktree)
	}
	if got := Get().ProjectConfigDir(); got != projectConfigDir {
		t.Errorf("ProjectConfigDir() = %q, want the relative %q after WithRealConfigPaths", got, projectConfigDir)
	}
	if got := Get().ProjectConfigDirAbs(); got != filepath.Join(wd, projectConfigDir) {
		t.Errorf("ProjectConfigDirAbs() = %q, want %q under the pinned CWD", got, filepath.Join(wd, projectConfigDir))
	}
}

// TestPinProjectCWDPanicsInsideABareCheckout covers the helper's fail-fast: a
// temp dir inside a bare checkout still resolves to that checkout's bare root,
// so it cannot serve as the pinned project CWD and must not be used silently.
func TestPinProjectCWDPanicsInsideABareCheckout(t *testing.T) {
	pinHostDirs(t)
	root := writeBareCheckout(t)

	assertPanics(t, func() { pinProjectCWD(root) })
}

func TestProjectConfigDirAbsFallsBackWhenCwdUnreadable(t *testing.T) {
	WithRealConfigPaths(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if got := Get().ProjectConfigDirAbs(); got != ".agents-sandbox" {
		t.Errorf("ProjectConfigDirAbs() with an unreadable cwd = %q, want the relative fallback", got)
	}
}

func TestMSBHomeUnderDataDir(t *testing.T) {
	WithMockConfigPaths(t)
	got := MSBHome()
	want := filepath.Join(Get().UserDataDir(), "msb")
	if got != want {
		t.Errorf("MSBHome() = %q, want %q", got, want)
	}
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
