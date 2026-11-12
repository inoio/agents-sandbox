package reprovision

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// writeHomeTarget writes a config file declaring the .toolrc home target whose
// source lives next to it, so the layer resolves the source against its own dir.
func writeHomeTarget(t *testing.T, dir, content string) {
	t.Helper()
	testutil.WriteFile(t, dir, "target-source", content)
	testutil.WriteFile(t, dir, "config.yaml", "home:\n  .toolrc: target-source\n")
}

// TestHomeFilesFromPerSlugConfig verifies a home: entry in the per-slug user
// config dir is provisioned even when neither the generic nor the project scope
// declares it.
func TestHomeFilesFromPerSlugConfig(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	writeHomeTarget(t, cp.UserSlugConfigDir("widget-bbb"), "per-slug\n")

	ui := termiotest.NewTestMock(t)
	cf, err := LoadConfigFilesForHost(
		agent.Resolve(opencodeTestAgent()), "widget-bbb", t.TempDir(), VMHomeDir, &ui, true,
	)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	want := filepath.Join(VMHomeDir, ".toolrc")
	got, ok := cf.HomeFiles[want]
	if !ok {
		t.Fatalf("HomeFiles missing %q: %v", want, cf.HomeFiles)
	}
	if string(got) != "per-slug\n" {
		t.Errorf("HomeFiles[%q] = %q, want %q", want, got, "per-slug\n")
	}
}

// TestHomeFilesPerSlugOverridesGeneric pins the scope precedence for home
// layers: the per-slug scope overrides the generic user scope, and the project
// scope still overrides the per-slug scope.
func TestHomeFilesPerSlugOverridesGeneric(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	writeHomeTarget(t, cp.UserConfigDir(), "generic\n")
	writeHomeTarget(t, cp.UserSlugConfigDir("widget-bbb"), "per-slug\n")

	ui := termiotest.NewTestMock(t)
	load := func() map[string][]byte {
		t.Helper()
		cf, err := LoadConfigFilesForHost(
			agent.Resolve(opencodeTestAgent()), "widget-bbb", t.TempDir(), VMHomeDir, &ui, true,
		)
		if err != nil {
			t.Fatalf("LoadConfigFilesForHost: %v", err)
		}
		return cf.HomeFiles
	}

	want := filepath.Join(VMHomeDir, ".toolrc")
	if got := string(load()[want]); got != "per-slug\n" {
		t.Fatalf("per-slug home file = %q, want %q", got, "per-slug\n")
	}

	writeHomeTarget(t, cp.ProjectConfigDir(), "project\n")
	if got := string(load()[want]); got != "project\n" {
		t.Errorf("project home file = %q, want %q", got, "project\n")
	}
}

// TestDescribeUsesConfigSlug pins the slug pass-through into the description: a
// snippet that exists only in the per-slug user scope is described, so the slug
// resolved at the CLI boundary reaches Describe's load instead of being dropped.
func TestDescribeUsesConfigSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()

	perSlugDir := configpathstest.CreateDir(
		filepath.Join(cp.UserSlugConfigDir("widget-bbb"), opencodeTestAgent().ConfigDirName()),
	)
	testutil.WriteFile(t, perSlugDir, "opencode-model.json", `{"model":"per-slug"}`)

	ui := termiotest.NewTestMock(t)
	merged, sources, _, _, _, source, err := Describe(
		opencodeTestAgent(), "widget-bbb", t.TempDir(), VMHomeDir, &ui, true,
	)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	snippet := filepath.Join(perSlugDir, "opencode-model.json")
	if !slices.Contains(sources, snippet) {
		t.Errorf("Describe sources = %v, want the per-slug snippet %q", sources, snippet)
	}
	if !slices.Contains(source.UserDirs, perSlugDir) {
		t.Errorf("Describe source user dirs = %v, want the per-slug dir %q", source.UserDirs, perSlugDir)
	}
	if !strings.Contains(string(merged), "per-slug") {
		t.Errorf("merged config = %q, want the per-slug snippet merged", string(merged))
	}
}
