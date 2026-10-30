package viperconfig

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestConfigLayersOrderAndPaths pins the layer order and the resolved path of
// every file layer. The order is the one NewResolver documents and merges, so
// display and merge cannot drift: both walk the same slice.
func TestConfigLayersOrderAndPaths(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	// Only the generic user layer has a file, so Present separates the layers
	// that resolve to a config file from the ones that do not.
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})

	layers := ConfigLayers("widget-bbb")

	wantNames := []string{
		"generic user config",
		"per-project user config",
		"project config",
		"legacy env",
		"env",
		"flags",
	}
	if len(layers) != len(wantNames) {
		t.Fatalf("ConfigLayers returned %d layers, want %d: %+v", len(layers), len(wantNames), layers)
	}
	for i, want := range wantNames {
		if layers[i].Name != want {
			t.Errorf("layer %d name = %q, want %q", i, layers[i].Name, want)
		}
	}

	wantPaths := []string{
		cp.UserConfigDir(),
		cp.UserSlugConfigDir("widget-bbb"),
		cp.ProjectConfigDir(),
		"",
		"",
		"",
	}
	for i, want := range wantPaths {
		if layers[i].Path != want {
			t.Errorf("layer %d path = %q, want %q", i, layers[i].Path, want)
		}
	}

	wantPresent := []bool{true, false, false, false, false, false}
	for i, want := range wantPresent {
		if layers[i].Present != want {
			t.Errorf("layer %d present = %v, want %v", i, layers[i].Present, want)
		}
	}
}

// TestNewResolverMergesEveryFileLayer proves every file layer is merged and
// they compose in precedence order. Each layer sets a key only it sets (so all
// three values prove all three were read) plus a shared log-level key (so the
// highest layer wins). The env and flag layers are not file layers; they reach
// viper through binding, not mergeDir.
func TestNewResolverMergesEveryFileLayer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(
		t,
		cp.UserConfigDir(),
		"config.yaml",
		map[string]any{"memory": "1G", "log-level": "verbose"},
	)
	testutil.WriteYAML(
		t,
		cp.UserSlugConfigDir("widget-bbb"),
		"config.yaml",
		map[string]any{"cpus": 3, "log-level": "warning"},
	)
	testutil.WriteYAML(
		t,
		cp.ProjectConfigDir(),
		"config.yaml",
		map[string]any{"tmp-size": "4G", "log-level": "error"},
	)

	r, err := NewResolver(nil, "widget-bbb")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.Memory != "1G" {
		t.Errorf("Memory = %q, want 1G (the generic user layer must be merged)", r.cfg.Memory)
	}
	if r.cfg.CPUs != 3 {
		t.Errorf("CPUs = %d, want 3 (the per-slug layer must be merged)", r.cfg.CPUs)
	}
	if r.cfg.TmpSize != "4G" {
		t.Errorf("TmpSize = %q, want 4G (the project layer must be merged)", r.cfg.TmpSize)
	}
	if r.cfg.LogLevel != "error" {
		t.Errorf("LogLevel = %q, want error (the project layer must win)", r.cfg.LogLevel)
	}
}
