package reprovision

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestSlugHasUserInputsEmptySlugIsFalse covers the guard for a slug-less
// identity: ConfigScopes omits the per-slug scope for an empty slug, so there
// is no per-slug user input to read.
func TestSlugHasUserInputsEmptySlugIsFalse(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if SlugHasUserInputs("") {
		t.Error("an empty slug has no per-slug user inputs")
	}
}

// TestSlugHasUserInputsIgnoresNonPerSlugScopes pins that only the per-slug user
// scope counts: an input in the generic user scope must not report the per-slug
// dir as populated, or a bare root with only generic user config would be
// routed away from its legacy slug.
func TestSlugHasUserInputsIgnoresNonPerSlugScopes(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.yaml", "yes: true\n")

	if SlugHasUserInputs("widget-bbb") {
		t.Error("a generic-user config must not count as a per-slug input")
	}
}

// TestSlugHasUserInputs exercises the per-slug presence probe across every
// input kind the predicate must see: the launcher config file, the three
// env/secret files, and any registered agent's config dir. Empty and missing
// dirs must not count.
func TestSlugHasUserInputs(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	tests := []struct {
		name  string
		setup func(t *testing.T, slug string)
		want  bool
	}{
		{
			name: "config file",
			setup: func(t *testing.T, slug string) {
				testutil.WriteFile(t, cp.UserSlugConfigDir(slug), "config.yaml", "yes: true\n")
			},
			want: true,
		},
		{
			name: "env file",
			setup: func(t *testing.T, slug string) {
				testutil.WriteFile(t, cp.UserSlugConfigDir(slug), configpaths.EnvFileName, "FOO=bar\n")
			},
			want: true,
		},
		{
			name: "secret file",
			setup: func(t *testing.T, slug string) {
				testutil.WriteFile(t, cp.UserSlugConfigDir(slug), configpaths.EnvSecretFileName, "FOO=bar\n")
			},
			want: true,
		},
		{
			name: "secret yaml file",
			setup: func(t *testing.T, slug string) {
				testutil.WriteFile(t, cp.UserSlugConfigDir(slug), "env.secret.yaml", "FOO: bar\n")
			},
			want: true,
		},
		{
			name: "agent config dir",
			setup: func(t *testing.T, slug string) {
				dir := configpathstest.CreateDir(
					filepath.Join(cp.UserSlugConfigDir(slug), opencodeTestAgent().ConfigDirName()),
				)
				testutil.WriteFile(t, dir, "opencode-model.json", `{"model":"x"}`)
			},
			want: true,
		},
		{
			name: "file named like agent dir",
			setup: func(t *testing.T, slug string) {
				testutil.WriteFile(
					t,
					cp.UserSlugConfigDir(slug),
					opencodeTestAgent().ConfigDirName(),
					"not a directory\n",
				)
			},
			want: false,
		},
		{
			name:  "empty dir",
			setup: func(t *testing.T, slug string) { configpathstest.CreateDir(cp.UserSlugConfigDir(slug)) },
			want:  false,
		},
		{
			name:  "missing dir",
			setup: func(t *testing.T, _ string) {},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := strings.ReplaceAll(tt.name, " ", "-")
			tt.setup(t, slug)
			if got := SlugHasUserInputs(slug); got != tt.want {
				t.Errorf("SlugHasUserInputs(%q) = %v, want %v", slug, got, tt.want)
			}
		})
	}
}

// TestEffectiveConfigSlugSeesSecretsOnlyPerSlugDir pins that a per-slug dir
// holding only env.secret.yaml counts as populated: during bare-root
// legacy-slug selection, a secrets-only repo-slug dir must not be skipped,
// which would silently reintroduce the #578 failure.
func TestEffectiveConfigSlugSeesSecretsOnlyPerSlugDir(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	repoSlug := "repo-slug"
	legacySlug := "legacy-slug"
	testutil.WriteFile(t, configpaths.Get().UserSlugConfigDir(repoSlug), "env.secret.yaml", "TOKEN: secret\n")
	testutil.WriteFile(t, configpaths.Get().UserSlugConfigDir(legacySlug), "config.yaml", "yes: true\n")

	got := EffectiveConfigSlug(repoSlug, []string{legacySlug})
	if got != repoSlug {
		t.Errorf("EffectiveConfigSlug = %q, want the repo slug %q", got, repoSlug)
	}
}

// TestEffectiveConfigSlugPrefersRepoSlug pins that the repo slug's per-slug
// config wins over a legacy slug's during a bare-root config migration.
func TestEffectiveConfigSlugPrefersRepoSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	repoSlug := "repo-slug"
	legacySlug := "legacy-slug"
	testutil.WriteFile(t, configpaths.Get().UserSlugConfigDir(repoSlug), "config.yaml", "yes: true\n")
	testutil.WriteFile(t, configpaths.Get().UserSlugConfigDir(legacySlug), "config.yaml", "yes: true\n")

	got := EffectiveConfigSlug(repoSlug, []string{legacySlug})
	if got != repoSlug {
		t.Errorf("EffectiveConfigSlug = %q, want the repo slug %q", got, repoSlug)
	}
}

// TestEffectiveConfigSlugFallsBackToLegacy pins the pre-migration selection:
// while the repo-slug dir holds no inputs, the launcher reads the legacy slug's
// dir, so the user's saved settings are not skipped before adoption moves them.
func TestEffectiveConfigSlugFallsBackToLegacy(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	repoSlug := "repo-slug"
	legacySlug := "legacy-slug"
	testutil.WriteFile(t, configpaths.Get().UserSlugConfigDir(legacySlug), "config.yaml", "yes: true\n")

	got := EffectiveConfigSlug(repoSlug, []string{legacySlug})
	if got != legacySlug {
		t.Errorf("EffectiveConfigSlug = %q, want the legacy slug %q", got, legacySlug)
	}
}

// TestEffectiveConfigSlugReturnsRepoSlugWhenNothingExists pins the default: with
// no per-slug inputs anywhere the repo slug is returned, so callers still label
// the dir the launcher would create.
func TestEffectiveConfigSlugReturnsRepoSlugWhenNothingExists(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	got := EffectiveConfigSlug("repo-slug", []string{"legacy-slug"})
	if got != "repo-slug" {
		t.Errorf("EffectiveConfigSlug = %q, want the repo slug", got)
	}
}
