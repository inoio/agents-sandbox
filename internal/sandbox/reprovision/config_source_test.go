package reprovision

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestDescribeConfigScopes(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	userDirs := func(dirs ...string) []string { return dirs }

	if got := describeConfigScopes(userDirs(missing), missing, true); got != "(none)" {
		t.Errorf("onlyExisting with nothing present = %q, want (none)", got)
	}
	onlyExisting := describeConfigScopes(userDirs(existing), missing, true)
	if !strings.Contains(onlyExisting, "user: ") || strings.Contains(onlyExisting, "project:") {
		t.Errorf("onlyExisting should list only the present scope, got %q", onlyExisting)
	}
	targets := describeConfigScopes(userDirs(existing), missing, false)
	if !strings.Contains(targets, "user: ") || !strings.Contains(targets, "project: ") {
		t.Errorf("targets should list both scopes, got %q", targets)
	}
	if strings.Contains(targets, "relative to CWD") {
		t.Errorf("targets should show explicit absolute paths, got %q", targets)
	}
	// A missing user dir must still be named when onlyExisting is false: the
	// project scope being the only one present must not drop the user scope.
	if got, want := describeConfigScopes(
		userDirs(missing),
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
	if got, want := describeConfigScopes(
		userDirs(missing),
		existing,
		true,
	), fmt.Sprintf(
		"project: %q",
		existing,
	); got != want {
		t.Errorf("onlyExisting with only the project present = %q, want %q", got, want)
	}
	// Every user scope dir is named, in order, before the project dir.
	if got, want := describeConfigScopes(
		userDirs(existing, missing),
		existing,
		true,
	), fmt.Sprintf("user: %q, project: %q", existing, existing); got != want {
		t.Errorf("onlyExisting with a per-slug scope = %q, want %q", got, want)
	}
}

// configBases serves fixed user and project config bases, so a test can place
// the agent's config-dir candidates where os.Stat fails.
type configBases struct {
	configpaths.ConfigPaths

	user, project string
}

func (c configBases) UserConfigDir() string                { return c.user }
func (c configBases) UserSlugConfigDir(slug string) string { return filepath.Join(c.user, slug) }
func (c configBases) ProjectConfigDir() string             { return c.project }
func (c configBases) ProjectConfigDirAbs() string          { return c.project }

func withConfigBases(t *testing.T, user, project string) {
	t.Helper()
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return configBases{user: user, project: project} }
	t.Cleanup(func() { configpaths.Get = orig })
}

// resolveConfigSource resolves an agent's source against the installed seam's
// scopes with an empty slug, preserving the pre-per-slug test behavior.
func resolveConfigSource(a agent.Agent) (ConfigSource, error) {
	return ResolveConfigSource(a, configpaths.ConfigScopes(configpaths.Get(), ""))
}

// invalidConfigDirAgent is a profile whose config-dir name is not a single
// clean path element, exercising the accessors' error path through the
// production resolution boundary.
type invalidConfigDirAgent struct{}

func (invalidConfigDirAgent) Name() string               { return "bad-profile" }
func (invalidConfigDirAgent) Experimental() bool         { return false }
func (invalidConfigDirAgent) ConfigDirName() string      { return "../escape" }
func (invalidConfigDirAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

func TestLoadConfigFilesRejectsInvalidProfileName(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(
		agent.Resolve(invalidConfigDirAgent{}),
		"",
		t.TempDir(),
		t.TempDir(),
		&ui,
		true,
	); err == nil {
		t.Fatal("LoadConfigFilesForHost with an invalid config-dir name = nil error, want error")
	}
}

func TestLoadAndDescribePropagateResolveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	a := invalidConfigDirAgent{}
	ui := termio.NewTestMock(t)
	if _, err := LoadConfigFilesForHost(agent.Resolve(a), "", t.TempDir(), t.TempDir(), &ui, true); err == nil {
		t.Error("expected LoadConfigFilesForHost to propagate the resolution error")
	}
	if _, _, _, _, _, err := Describe(a, "", t.TempDir(), t.TempDir(), &ui, true); err == nil {
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
		if _, err := resolveConfigSource(a); err == nil {
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
		if _, err := resolveConfigSource(a); err == nil {
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
	got, err := resolveConfigSource(a)
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
		UserDirs:   []string{filepath.Join(cp.UserConfigDir(), "opencode")},
		ProjectDir: filepath.Join(cp.ProjectConfigDirAbs(), "opencode"),
		Fallback:   "opencode",
	}
	userDirs, project := SourceDirs(src)
	if !slices.Equal(userDirs, src.UserDirs) || project != src.ProjectDir {
		t.Errorf("SourceDirs = (%q, %q), want the resolved source dirs", userDirs, project)
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
	src, err := resolveConfigSource(a)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if want := []string{
		configpaths.CreateDir(filepath.Join(cp.UserConfigDir(), a.ConfigDirName())),
	}; !slices.Equal(
		src.PrimaryUserDirs,
		want,
	) {
		t.Errorf("PrimaryUserDirs = %v, want the seam's primary user dirs %v", src.PrimaryUserDirs, want)
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

	if _, err := resolveConfigSource(invalidConfigDirAgent{}); err == nil {
		t.Fatal("ResolveConfigSource with an invalid config-dir name = nil error, want error")
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
			got, err := resolveConfigSource(a)
			if err != nil {
				t.Fatalf("ResolveConfigSource: %v", err)
			}

			name := "opencode2"
			if tc.wantFallback != "" {
				name = tc.wantFallback
			}
			want := ConfigSource{
				UserDirs:          []string{filepath.Join(cp.UserConfigDir(), name)},
				ProjectDir:        filepath.Join(cp.ProjectConfigDir(), name),
				Fallback:          tc.wantFallback,
				PrimaryUserDirs:   []string{filepath.Join(cp.UserConfigDir(), "opencode2")},
				PrimaryProjectDir: filepath.Join(cp.ProjectConfigDirAbs(), "opencode2"),
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ResolveConfigSource = %+v, want %+v", got, want)
			}
		})
	}
}

// TestResolveConfigSourceOneNamespaceAcrossScopes pins the global namespace
// decision: a per-slug primary dir wins over a generic legacy dir, and the chosen
// primary namespace applies to every scope (the generic legacy dir is dropped).
func TestResolveConfigSourceOneNamespaceAcrossScopes(t *testing.T) {
	generic := t.TempDir()
	perSlug := t.TempDir()
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(generic, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(perSlug, "opencode2"), 0o755); err != nil {
		t.Fatal(err)
	}
	scopes := []configpaths.ConfigScope{
		{Kind: configpaths.ScopeGenericUser, Dir: generic},
		{Kind: configpaths.ScopePerSlugUser, Dir: perSlug},
		{Kind: configpaths.ScopeProject, Dir: project},
	}

	a, _ := agent.Lookup("opencode2")
	src, err := ResolveConfigSource(a, scopes)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if src.Fallback != "" {
		t.Errorf("Fallback = %q, want empty (the primary namespace wins globally)", src.Fallback)
	}
	wantUserDirs := []string{
		filepath.Join(generic, "opencode2"),
		filepath.Join(perSlug, "opencode2"),
	}
	if !slices.Equal(src.UserDirs, wantUserDirs) {
		t.Errorf("UserDirs = %v, want %v", src.UserDirs, wantUserDirs)
	}
	if want := filepath.Join(project, "opencode2"); src.ProjectDir != want {
		t.Errorf("ProjectDir = %q, want %q", src.ProjectDir, want)
	}
}

// TestResolveConfigSourceFallbackWarningNamesLegacy pins that a legacy namespace
// found in any scope selects that namespace for every scope and that the scope
// description names the legacy dir actually read.
func TestResolveConfigSourceFallbackWarningNamesLegacy(t *testing.T) {
	generic := t.TempDir()
	perSlug := t.TempDir()
	project := t.TempDir()
	legacy := filepath.Join(generic, "opencode")
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	scopes := []configpaths.ConfigScope{
		{Kind: configpaths.ScopeGenericUser, Dir: generic},
		{Kind: configpaths.ScopePerSlugUser, Dir: perSlug},
		{Kind: configpaths.ScopeProject, Dir: project},
	}

	a, _ := agent.Lookup("opencode2")
	src, err := ResolveConfigSource(a, scopes)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if src.Fallback != "opencode" {
		t.Errorf("Fallback = %q, want opencode", src.Fallback)
	}
	described := describeConfigScopes(src.UserDirs, src.ProjectDir, true)
	if !strings.Contains(described, legacy) {
		t.Errorf("describeConfigScopes = %q, want the legacy dir %q", described, legacy)
	}
}

// TestResolveConfigSourceDegradesWhenCwdUnreadable pins the graceful degrade:
// a relative project scope dir whose Abs resolution fails (the CWD cannot be
// read) must be kept as-is rather than aborting resolution, mirroring
// ProjectConfigDirAbs.
func TestResolveConfigSourceDegradesWhenCwdUnreadable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	generic := t.TempDir()
	if err := os.Mkdir(filepath.Join(generic, "opencode2"), 0o755); err != nil {
		t.Fatal(err)
	}
	scopes := []configpaths.ConfigScope{
		{Kind: configpaths.ScopeGenericUser, Dir: generic},
		{Kind: configpaths.ScopeProject, Dir: ".agents-sandbox"},
	}

	a, _ := agent.Lookup("opencode2")
	src, err := ResolveConfigSource(a, scopes)
	if err != nil {
		t.Fatalf("ResolveConfigSource: %v", err)
	}
	if want := filepath.Join(".agents-sandbox", "opencode2"); src.ProjectDir != want {
		t.Errorf("ProjectDir = %q, want the relative candidate %q", src.ProjectDir, want)
	}
	if src.Fallback != "" {
		t.Errorf("Fallback = %q, want empty (the primary user dir exists)", src.Fallback)
	}
}
