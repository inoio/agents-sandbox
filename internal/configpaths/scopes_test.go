package configpaths

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestConfigScopesOrderAndDirs pins the ordered authority: [generic user,
// per-slug user, project], with dirs from the corresponding accessors, so the
// readers and writers every later scope task adds can rely on the order.
func TestConfigScopesOrderAndDirs(t *testing.T) {
	WithMockConfigPaths(t)
	paths := Get()
	want := []ConfigScope{
		{Kind: ScopeGenericUser, Dir: paths.UserConfigDir()},
		{Kind: ScopePerSlugUser, Dir: paths.UserSlugConfigDir("widget-bbb")},
		{Kind: ScopeProject, Dir: paths.ProjectConfigDir()},
	}
	if got := ConfigScopes(paths, "widget-bbb"); !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigScopes() = %+v, want %+v", got, want)
	}
}

// TestConfigScopesOmitsPerSlugForEmptySlug pins the empty-slug exception: only
// [generic user, project], never an empty per-slug dir a merged file could
// otherwise land in.
func TestConfigScopesOmitsPerSlugForEmptySlug(t *testing.T) {
	WithMockConfigPaths(t)
	paths := Get()
	want := []ConfigScope{
		{Kind: ScopeGenericUser, Dir: paths.UserConfigDir()},
		{Kind: ScopeProject, Dir: paths.ProjectConfigDir()},
	}
	if got := ConfigScopes(paths, ""); !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigScopes() = %+v, want %+v", got, want)
	}
}

// TestConfigScopeFileJoins pins the four joins a scope knows: the three env/secret
// file names via the shared constants and the agent config dir by name.
func TestConfigScopeFileJoins(t *testing.T) {
	s := ConfigScope{Dir: "/base"}
	if got, want := s.EnvFile(), filepath.Join("/base", EnvFileName); got != want {
		t.Errorf("EnvFile() = %q, want %q", got, want)
	}
	if got, want := s.EnvSecretFile(), filepath.Join("/base", EnvSecretFileName); got != want {
		t.Errorf("EnvSecretFile() = %q, want %q", got, want)
	}
	if got, want := s.EnvSecretYAMLFile(), filepath.Join("/base", envSecretYAMLFileName); got != want {
		t.Errorf("EnvSecretYAMLFile() = %q, want %q", got, want)
	}
	if got, want := s.AgentConfigDir("opencode2"), filepath.Join("/base", "opencode2"); got != want {
		t.Errorf("AgentConfigDir() = %q, want %q", got, want)
	}
}

// TestScopeDirsMapsOrder pins that ScopeDirs is a plain order-preserving map.
func TestScopeDirsMapsOrder(t *testing.T) {
	scopes := []ConfigScope{
		{Kind: ScopeGenericUser, Dir: "one"},
		{Kind: ScopePerSlugUser, Dir: "two"},
		{Kind: ScopeProject, Dir: "three"},
	}
	want := []string{"one", "two", "three"}
	if got := ScopeDirs(scopes); !reflect.DeepEqual(got, want) {
		t.Errorf("ScopeDirs() = %v, want %v", got, want)
	}
}
