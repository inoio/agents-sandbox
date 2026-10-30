package reprovision

import (
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

// configBases serves fixed user and project config bases, so a test can place
// the agent's config-dir candidates where os.Stat fails.
type configBases struct {
	configpaths.ConfigPaths

	user, project string
}

func (c configBases) UserConfigDir() string       { return c.user }
func (c configBases) ProjectConfigDirAbs() string { return c.project }

func withConfigBases(t *testing.T, user, project string) {
	t.Helper()
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return configBases{user: user, project: project} }
	t.Cleanup(func() { configpaths.Get = orig })
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

func TestLoadAndDescribePropagateResolveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	a := invalidConfigDirAgent{}
	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(a, t.TempDir(), t.TempDir(), &ui, true); err == nil {
		t.Error("expected LoadConfigFilesForHost to propagate the resolution error")
	}
	if _, _, _, _, _, err := Describe(a, t.TempDir(), t.TempDir(), &ui, true); err == nil {
		t.Error("expected Describe to propagate the resolution error")
	}
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
		withConfigBases(t, blocker, blocker)
		if _, err := ResolveConfigSource(a); err == nil {
			t.Error("expected an error when the primary candidate stat fails")
		}
	})

	t.Run("alias", func(t *testing.T) {
		// The primary is absent; the alias is a symlink loop, so its stat
		// fails with ELOOP rather than reporting it absent.
		base := t.TempDir()
		if err := os.Symlink("opencode", filepath.Join(base, "opencode")); err != nil {
			t.Fatal(err)
		}
		withConfigBases(t, base, base)
		if _, err := ResolveConfigSource(a); err == nil {
			t.Error("expected an error when an alias candidate stat fails")
		}
	})
}

func TestResolveConfigSourceStatErrorDoesNotHideUsableScope(t *testing.T) {
	// An unreadable user candidate must not abort when the project candidate
	// exists: "either scope exists" still resolves the primary.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectBase := t.TempDir()
	existing := filepath.Join(projectBase, "opencode2")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	withConfigBases(t, blocker, projectBase)

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
	if src.PrimaryUserDir != configpaths.CreateDir(filepath.Join(cp.UserConfigDir(), a.ConfigDirName())) {
		t.Errorf("PrimaryUserDir = %q, want the seam's primary user dir", src.PrimaryUserDir)
	}
	if src.PrimaryProjectDir != configpaths.CreateDir(filepath.Join(cp.ProjectConfigDirAbs(), a.ConfigDirName())) {
		t.Errorf("PrimaryProjectDir = %q, want the seam's primary project dir", src.PrimaryProjectDir)
	}
	if !filepath.IsAbs(src.PrimaryProjectDir) {
		t.Errorf("PrimaryProjectDir %q should be absolute", src.PrimaryProjectDir)
	}
}

func TestResolveConfigSourcePropagatesInvalidConfigDirName(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if _, err := ResolveConfigSource(invalidConfigDirAgent{}); err == nil {
		t.Fatal("ResolveConfigSource with an invalid config-dir name = nil error, want error")
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
