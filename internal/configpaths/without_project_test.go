package configpaths

import (
	"reflect"
	"testing"
)

// TestWithoutProjectLayerDropsEveryProjectInput pins the exclusion an
// unapproved project layer gets: no project scope, no project config dir, no
// project Dockerfile, so no reader that derives its inputs from these can reach
// the project layer.
func TestWithoutProjectLayerDropsEveryProjectInput(t *testing.T) {
	WithMockConfigPaths(t)
	paths := WithoutProjectLayer(Get())

	want := []ConfigScope{
		{Kind: ScopeGenericUser, Dir: paths.UserConfigDir()},
		{Kind: ScopePerSlugUser, Dir: paths.UserSlugConfigDir("widget-bbb")},
	}
	if got := ConfigScopes(paths, "widget-bbb"); !reflect.DeepEqual(got, want) {
		t.Errorf("ConfigScopes() = %+v, want only the user scopes %+v", got, want)
	}
	for name, got := range map[string]string{
		"ProjectConfigDir":         paths.ProjectConfigDir(),
		"ProjectConfigDirAbs":      paths.ProjectConfigDirAbs(),
		"ProjectDockerfile":        paths.ProjectDockerfile(),
		"IgnoredWorktreeConfigDir": paths.IgnoredWorktreeConfigDir(),
	} {
		if got != "" {
			t.Errorf("%s() = %q, want empty", name, got)
		}
	}
}

// TestWithoutProjectLayerKeepsTheUserPaths pins that the exclusion touches only
// the project layer: every user path delegates unchanged.
func TestWithoutProjectLayerKeepsTheUserPaths(t *testing.T) {
	WithMockConfigPaths(t)
	inner := Get()
	paths := WithoutProjectLayer(inner)

	innerHome, innerErr := inner.UserHomeDir()
	home, err := paths.UserHomeDir()
	if home != innerHome || (err == nil) != (innerErr == nil) {
		t.Errorf("UserHomeDir() = %q, %v, want %q, %v", home, err, innerHome, innerErr)
	}
	for name, pair := range map[string][2]string{
		"UserConfigDir":     {paths.UserConfigDir(), inner.UserConfigDir()},
		"UserSlugConfigDir": {paths.UserSlugConfigDir("s"), inner.UserSlugConfigDir("s")},
		"UserCacheDir":      {paths.UserCacheDir(), inner.UserCacheDir()},
		"UserStateDir":      {paths.UserStateDir(), inner.UserStateDir()},
		"UserDataDir":       {paths.UserDataDir(), inner.UserDataDir()},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s() = %q, want %q", name, pair[0], pair[1])
		}
	}
}

// TestWithProjectLayerUndoesTheExclusion pins that the exclusion can be undone,
// so a later decision starts from the full paths again.
func TestWithProjectLayerUndoesTheExclusion(t *testing.T) {
	WithMockConfigPaths(t)
	inner := Get()
	if got := WithProjectLayer(WithoutProjectLayer(inner)); got.ProjectConfigDir() != inner.ProjectConfigDir() {
		t.Errorf("ProjectConfigDir() after undo = %q, want %q", got.ProjectConfigDir(), inner.ProjectConfigDir())
	}
	if got := WithProjectLayer(inner); got != inner {
		t.Error("WithProjectLayer changed paths that were not excluded")
	}
}

func TestUseInstallsAndRestoresThePathFactory(t *testing.T) {
	WithMockConfigPaths(t)
	original := Get()
	restore := Use(func() ConfigPaths { return WithoutProjectLayer(original) })
	if Get().ProjectConfigDir() != "" {
		t.Error("Use did not install the factory")
	}
	restore()
	if Get().ProjectConfigDir() != original.ProjectConfigDir() {
		t.Error("restore did not reinstall the previous factory")
	}
}
