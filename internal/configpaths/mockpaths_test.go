package configpaths

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// TestMockConfigPaths exercises the WithMockConfigPaths helper that other
// packages' tests rely on, verifying each method resolves to a path rooted in
// the isolated temp base directory rather than the real XDG locations.
func TestMockConfigPaths(t *testing.T) {
	WithMockConfigPaths(t)
	cfgPaths := Get()
	m := Get().(*mockConfigPaths)
	base := m.baseDir
	a, _ := agent.Lookup("opencode")

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
	userAgentDir, err := cfgPaths.UserAgentConfigDir(a)
	if err != nil {
		t.Fatalf("UserAgentConfigDir() error = %v", err)
	}
	if userAgentDir != filepath.Join(base, "userconfig", a.ConfigDirName()) {
		t.Errorf(
			"UserAgentConfigDir() = %q, want %q",
			userAgentDir,
			filepath.Join(base, "userconfig", a.ConfigDirName()),
		)
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
	projectAgentDir, err := cfgPaths.ProjectAgentConfigDir(a)
	if err != nil {
		t.Fatalf("ProjectAgentConfigDir() error = %v", err)
	}
	if projectAgentDir != filepath.Join(base, "projectconfig", a.ConfigDirName()) {
		t.Errorf(
			"ProjectAgentConfigDir() = %q, want %q",
			projectAgentDir,
			filepath.Join(base, "projectconfig", a.ConfigDirName()),
		)
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

func TestMockAgentConfigDirCandidates(t *testing.T) {
	WithMockConfigPaths(t)
	base := Get().(*mockConfigPaths).baseDir
	oc, _ := agent.Lookup("opencode")
	oc2, _ := agent.Lookup("opencode2")

	openCodeUser, err := Get().UserAgentConfigDirCandidates(oc)
	if err != nil {
		t.Fatalf("UserAgentConfigDirCandidates error = %v", err)
	}
	if !slices.Equal(
		openCodeUser,
		[]string{filepath.Join(base, "userconfig", "opencode")},
	) {
		t.Errorf("opencode user candidates = %v", openCodeUser)
	}
	want := []string{filepath.Join(base, "userconfig", "opencode2"), filepath.Join(base, "userconfig", "opencode")}
	openCode2User, err := Get().UserAgentConfigDirCandidates(oc2)
	if err != nil {
		t.Fatalf("UserAgentConfigDirCandidates error = %v", err)
	}
	if !slices.Equal(openCode2User, want) {
		t.Errorf("opencode2 user candidates = %v, want %v", openCode2User, want)
	}
	wantProject := []string{
		filepath.Join(base, "projectconfig", "opencode2"),
		filepath.Join(base, "projectconfig", "opencode"),
	}
	openCode2Project, err := Get().ProjectAgentConfigDirCandidates(oc2)
	if err != nil {
		t.Fatalf("ProjectAgentConfigDirCandidates error = %v", err)
	}
	if !slices.Equal(openCode2Project, wantProject) {
		t.Errorf("opencode2 project candidates = %v, want %v", openCode2Project, wantProject)
	}
	if _, err := os.Stat(filepath.Join(base, "userconfig", "opencode2")); !os.IsNotExist(err) {
		t.Errorf("candidate methods must not create dirs; stat err = %v", err)
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
