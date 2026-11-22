package prepare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
)

// TestRememberHomeVolumeAnswerNeverRewritesTheProjectConfig: the answer is a
// personal preference, and the project config is shared with the team.
func TestRememberHomeVolumeAnswerNeverRewritesTheProjectConfig(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	projectDir := paths.ProjectConfigDir()
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "home-volume:\n  on-image-change: ask\n"
	if err := os.WriteFile(filepath.Join(projectDir, "config.yaml"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := rememberHomeVolumeAnswer(paths, options.RunOptions{}, state.Key{Slug: "widget-abc"}, "keep")
	if err == nil || !strings.Contains(err.Error(), "project config") {
		t.Errorf("rememberHomeVolumeAnswer = %v, want a refusal naming the project config", err)
	}
	if content, _ := os.ReadFile(filepath.Join(projectDir, "config.yaml")); string(content) != original {
		t.Errorf("project config.yaml = %q, want it untouched", content)
	}
}

// TestRememberHomeVolumeAnswerUpdatesTheLayerThatSetsIt: a per-slug ask would
// shadow a generic-user write, so the owning layer is updated instead.
func TestRememberHomeVolumeAnswerUpdatesTheLayerThatSetsIt(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	slugDir := paths.UserSlugConfigDir("widget-abc")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(slugDir, "config.yaml"),
		[]byte("home-volume:\n  on-image-change: ask\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	// The config slug wins over the checkout slug, and without one the
	// checkout slug names the per-slug layer.
	for name, tc := range map[string]struct {
		opts options.RunOptions
		key  state.Key
	}{
		"config slug":   {options.RunOptions{ConfigSlug: "widget-abc"}, state.Key{Slug: "other-checkout"}},
		"checkout slug": {options.RunOptions{}, state.Key{Slug: "widget-abc"}},
	} {
		dir, err := rememberHomeVolumeAnswer(paths, tc.opts, tc.key, "keep")
		if err != nil {
			t.Fatalf("%s: rememberHomeVolumeAnswer: %v", name, err)
		}
		if dir != slugDir {
			t.Errorf("%s: dir = %q, want the per-slug layer %q", name, dir, slugDir)
		}
	}
	if content, _ := os.ReadFile(
		filepath.Join(slugDir, "config.yaml"),
	); !strings.Contains(
		string(content),
		"on-image-change: keep",
	) {
		t.Errorf("per-slug config.yaml = %q, want the answer", content)
	}
}

func TestRememberHomeVolumeAnswerRefusesAnOverridingEnvironmentVariable(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_HOME_VOLUME_ON_IMAGE_CHANGE", "ask")

	if _, err := rememberHomeVolumeAnswer(
		configpaths.Get(),
		options.RunOptions{},
		state.Key{Slug: "widget-abc"},
		"keep",
	); err == nil ||
		!strings.Contains(err.Error(), "AGENTS_SANDBOX_HOME_VOLUME_ON_IMAGE_CHANGE") {
		t.Errorf("rememberHomeVolumeAnswer = %v, want the environment variable named", err)
	}
}

func TestHomeVolumePolicyCarriesTheConfiguredAnswer(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	opts := options.RunOptions{HomeVolumeOnImageChange: volume.AnswerAsk}
	policy := homeVolumePolicy(configpaths.Get(), opts, state.Key{Slug: "s"}, false)
	if policy.Answer != volume.AnswerAsk || policy.Remember == nil {
		t.Fatalf("policy = %+v, want ask with a writer", policy)
	}
	// A skip-VM run must not persist a change.
	if dryRun := homeVolumePolicy(configpaths.Get(), opts, state.Key{Slug: "s"}, true); dryRun.Remember != nil {
		t.Error("a --dry-run-vm policy can remember an answer")
	}
}

// outsideRepoPaths has no project config dir, as for a run outside a repository.
type outsideRepoPaths struct{ configpaths.ConfigPaths }

func (outsideRepoPaths) ProjectConfigDir() string { return "" }

// TestRememberHomeVolumeAnswerOutsideARepository: with no project config dir and
// no layer setting the key, the write goes to the per-slug user config; an
// unset owner is not mistaken for the (empty) project config dir.
func TestRememberHomeVolumeAnswerOutsideARepository(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := outsideRepoPaths{configpaths.Get()}

	dir, err := rememberHomeVolumeAnswer(paths, options.RunOptions{}, state.Key{Slug: "widget-abc"}, "keep")
	if err != nil {
		t.Fatalf("rememberHomeVolumeAnswer: %v", err)
	}
	if dir != paths.UserSlugConfigDir("widget-abc") {
		t.Errorf("dir = %q, want the per-slug user config", dir)
	}
}
