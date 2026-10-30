package reprovision

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestDescribeConfigScopes(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")

	if got := describeConfigScopes(missing, missing, true); got != "(none)" {
		t.Errorf("onlyExisting with nothing present = %q, want (none)", got)
	}
	onlyExisting := describeConfigScopes(existing, missing, true)
	if !strings.Contains(onlyExisting, "user: ") || strings.Contains(onlyExisting, "project:") {
		t.Errorf("onlyExisting should list only the present scope, got %q", onlyExisting)
	}
	targets := describeConfigScopes(existing, missing, false)
	if !strings.Contains(targets, "user: ") || !strings.Contains(targets, "project: ") {
		t.Errorf("targets should list both scopes, got %q", targets)
	}
	if strings.Contains(targets, "relative to CWD") {
		t.Errorf("targets should show explicit absolute paths, got %q", targets)
	}
	// A missing user dir must still be named when onlyExisting is false: the
	// project scope being the only one present must not drop the user scope.
	if got, want := describeConfigScopes(
		missing,
		existing,
		false,
	), fmt.Sprintf(
		"user: %q, project: %q",
		missing,
		existing,
	); got != want {
		t.Errorf("targets with only the project present = %q, want %q", got, want)
	}
	// An existing project dir must be named when onlyExisting is true: the
	// user scope being absent must not drop the project scope.
	if got, want := describeConfigScopes(missing, existing, true), fmt.Sprintf("project: %q", existing); got != want {
		t.Errorf("onlyExisting with only the project present = %q, want %q", got, want)
	}
}

// emptyCandidatesConfigPaths returns no candidates, exercising the guard that
// keeps ResolveConfigSource from indexing an empty candidate list.
type emptyCandidatesConfigPaths struct{ configpaths.ConfigPaths }

func (emptyCandidatesConfigPaths) UserAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return nil, nil
}

func (emptyCandidatesConfigPaths) ProjectAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return nil, nil
}

func TestResolveConfigSourceEmptyCandidates(t *testing.T) {
	withEmptyCandidates(t)

	a, _ := agent.Lookup("opencode2")
	if _, err := ResolveConfigSource(a); err == nil {
		t.Error("expected an error for empty config-dir candidates")
	}
}

// invalidConfigDirAgent is a profile whose config-dir name is not a single
// clean path element, exercising the accessors' error path through the
// production resolution boundary.
type invalidConfigDirAgent struct{}

func (invalidConfigDirAgent) Name() string               { return "bad-profile" }
func (invalidConfigDirAgent) ConfigDirName() string      { return "../escape" }
func (invalidConfigDirAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

func TestLoadConfigFilesRejectsInvalidProfileName(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(invalidConfigDirAgent{}, t.TempDir(), t.TempDir(), &ui, true); err == nil {
		t.Fatal("LoadConfigFilesForHost with an invalid config-dir name = nil error, want error")
	}
}

func withEmptyCandidates(t *testing.T) {
	t.Helper()
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return emptyCandidatesConfigPaths{} }
	t.Cleanup(func() { configpaths.Get = orig })
}

func TestLoadAndDescribePropagateResolveError(t *testing.T) {
	withEmptyCandidates(t)

	a, _ := agent.Lookup("opencode2")
	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(a, t.TempDir(), t.TempDir(), &ui, true); err == nil {
		t.Error("expected LoadConfigFilesForHost to propagate the resolution error")
	}
	if _, _, _, _, _, err := Describe(a, t.TempDir(), t.TempDir(), &ui, true); err == nil {
		t.Error("expected Describe to propagate the resolution error")
	}
}

// staticCandidates serves fixed candidate dirs so a test can point the
// resolver at paths whose os.Stat fails with a non-"not exist" error.
type staticCandidates struct {
	configpaths.ConfigPaths

	dirs []string
}

func (s staticCandidates) UserAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return s.dirs, nil
}

func (s staticCandidates) ProjectAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return s.dirs, nil
}

func withStaticCandidates(t *testing.T, dirs ...string) {
	t.Helper()
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return staticCandidates{dirs: dirs} }
	t.Cleanup(func() { configpaths.Get = orig })
}

func TestResolveConfigSourceStatError(t *testing.T) {
	// A candidate beneath a regular file makes os.Stat fail with ENOTDIR,
	// which must surface rather than be treated as "absent" — for the primary
	// candidate and for an alias candidate reached through the fallback loop.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := agent.Lookup("opencode2")

	t.Run("primary", func(t *testing.T) {
		withStaticCandidates(t, filepath.Join(blocker, "opencode2"))
		if _, err := ResolveConfigSource(a); err == nil {
			t.Error("expected an error when the primary candidate stat fails")
		}
	})

	t.Run("alias", func(t *testing.T) {
		withStaticCandidates(t, filepath.Join(t.TempDir(), "absent"), filepath.Join(blocker, "opencode"))
		if _, err := ResolveConfigSource(a); err == nil {
			t.Error("expected an error when an alias candidate stat fails")
		}
	})
}

// splitCandidates serves distinct user and project candidate lists, so a test
// can make one scope unreadable while the other is usable.
type splitCandidates struct {
	configpaths.ConfigPaths

	user, project []string
}

func (s splitCandidates) UserAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return s.user, nil
}

func (s splitCandidates) ProjectAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return s.project, nil
}

func TestResolveConfigSourceStatErrorDoesNotHideUsableScope(t *testing.T) {
	// An unreadable user candidate must not abort when the project candidate
	// exists: "either scope exists" still resolves the primary.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := t.TempDir()
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths {
		return splitCandidates{
			user:    []string{filepath.Join(blocker, "opencode2")},
			project: []string{existing},
		}
	}
	t.Cleanup(func() { configpaths.Get = orig })

	a, _ := agent.Lookup("opencode2")
	got, err := ResolveConfigSource(a)
	if err != nil {
		t.Fatalf("ResolveConfigSource: unexpected error %v", err)
	}
	if got.Fallback != "" || got.ProjectDir != existing {
		t.Errorf("ResolveConfigSource = %+v, want primary with project dir %q", got, existing)
	}
}

func TestSourceDirsMirrorResolvedSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	src := ConfigSource{
		UserDir:    filepath.Join(cp.UserConfigDir(), "opencode"),
		ProjectDir: filepath.Join(cp.ProjectConfigDirAbs(), "opencode"),
		Fallback:   "opencode",
	}
	user, project := SourceDirs(src)
	if user != src.UserDir || project != src.ProjectDir {
		t.Errorf("SourceDirs = (%q, %q), want the resolved source dirs", user, project)
	}
}

// TestResolveConfigSourceCarriesPrimaryDirs pins that the primary migration
// targets travel in the resolved source, so the fallback warning never
// re-derives (and re-validates) the profile's config-dir name.
func TestResolveConfigSourceCarriesPrimaryDirs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	a, _ := agent.Lookup("opencode2")

	// No dirs exist, so the primary dirs are returned with an empty fallback.
	src, err := ResolveConfigSource(a)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if src.PrimaryUserDir != configpaths.MustDir(cp.UserAgentConfigDir(a)) {
		t.Errorf("PrimaryUserDir = %q, want the seam's primary user dir", src.PrimaryUserDir)
	}
	if src.PrimaryProjectDir != configpaths.MustDir(cp.ProjectAgentConfigDir(a)) {
		t.Errorf("PrimaryProjectDir = %q, want the seam's primary project dir", src.PrimaryProjectDir)
	}
	if !filepath.IsAbs(src.PrimaryProjectDir) {
		t.Errorf("PrimaryProjectDir %q should be absolute", src.PrimaryProjectDir)
	}
}

// The candidate accessors fail only for a profile whose config-dir name is not
// a single clean path element. These sentinels let a test assert the error was
// propagated as-is, rather than a later fall-through error standing in for it.
var (
	errInvalidUserConfigDirName    = errors.New("invalid user config-dir name")
	errInvalidProjectConfigDirName = errors.New("invalid project config-dir name")
)

// userErrorCandidates serves a usable project candidate list but errors on the
// user list, exercising the first candidate call's error path.
type userErrorCandidates struct {
	configpaths.ConfigPaths
}

func (userErrorCandidates) UserAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return nil, errInvalidUserConfigDirName
}

func (userErrorCandidates) ProjectAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return []string{"/nonexistent/opencode2"}, nil
}

func TestResolveConfigSourcePropagatesUserCandidateError(t *testing.T) {
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return userErrorCandidates{} }
	t.Cleanup(func() { configpaths.Get = orig })

	a, _ := agent.Lookup("opencode2")
	_, err := ResolveConfigSource(a)
	if !errors.Is(err, errInvalidUserConfigDirName) {
		t.Fatalf("ResolveConfigSource with a failing user candidate = %v, want the candidate error", err)
	}
}

// projectErrorCandidates serves a usable user candidate list but errors on the
// project list, exercising the second candidate call's error path.
type projectErrorCandidates struct {
	configpaths.ConfigPaths
}

func (projectErrorCandidates) UserAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return []string{"/nonexistent/opencode2"}, nil
}

func (projectErrorCandidates) ProjectAgentConfigDirCandidates(agent.Agent) ([]string, error) {
	return nil, errInvalidProjectConfigDirName
}

func TestResolveConfigSourcePropagatesProjectCandidateError(t *testing.T) {
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return projectErrorCandidates{} }
	t.Cleanup(func() { configpaths.Get = orig })

	a, _ := agent.Lookup("opencode2")
	_, err := ResolveConfigSource(a)
	if !errors.Is(err, errInvalidProjectConfigDirName) {
		t.Fatalf("ResolveConfigSource with a failing project candidate = %v, want the candidate error", err)
	}
}

// TestBaseUserConfigDirIsParentOfUserDir pins the derivation: home files and
// hooks resolve relative to the tool's user config dir, one level above the
// agent subdir the resolved source names.
func TestBaseUserConfigDirIsParentOfUserDir(t *testing.T) {
	src := ConfigSource{UserDir: filepath.Join(t.TempDir(), "agents-sandbox", "opencode2")}
	if got, want := baseUserConfigDir(src), filepath.Dir(src.UserDir); got != want {
		t.Errorf("baseUserConfigDir = %q, want %q", got, want)
	}
}

func TestResolveConfigSource(t *testing.T) {
	cases := []struct {
		name                       string
		user2, user1, proj2, proj1 bool
		wantFallback               string
	}{
		{"nothing", false, false, false, false, ""},
		{"user primary", true, false, false, false, ""},
		{"project primary", false, false, true, false, ""},
		{"user legacy", false, true, false, false, "opencode"},
		{"project legacy", false, false, false, true, "opencode"},
		{"primary beats legacy", true, true, true, true, ""},
		{"any primary wins", false, true, true, false, ""},
		{"user primary, project legacy", true, false, false, true, ""},
		{"alias in both scopes", false, true, false, true, "opencode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			cp := configpaths.Get()
			mk := func(dir, name string, on bool) {
				if on {
					if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			mk(cp.UserConfigDir(), "opencode2", tc.user2)
			mk(cp.UserConfigDir(), "opencode", tc.user1)
			mk(cp.ProjectConfigDir(), "opencode2", tc.proj2)
			mk(cp.ProjectConfigDir(), "opencode", tc.proj1)

			a, _ := agent.Lookup("opencode2")
			got, err := ResolveConfigSource(a)
			if err != nil {
				t.Fatalf("ResolveConfigSource: %v", err)
			}

			name := "opencode2"
			if tc.wantFallback != "" {
				name = tc.wantFallback
			}
			want := ConfigSource{
				UserDir:           filepath.Join(cp.UserConfigDir(), name),
				ProjectDir:        filepath.Join(cp.ProjectConfigDir(), name),
				Fallback:          tc.wantFallback,
				PrimaryUserDir:    filepath.Join(cp.UserConfigDir(), "opencode2"),
				PrimaryProjectDir: filepath.Join(cp.ProjectConfigDirAbs(), "opencode2"),
			}
			if got != want {
				t.Errorf("ResolveConfigSource = %+v, want %+v", got, want)
			}
		})
	}
}
