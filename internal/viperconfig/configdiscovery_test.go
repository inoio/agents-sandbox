package viperconfig

import (
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestLauncherAndHomeLoadersSelectTheSameConfigFile pins that the launcher
// config and the home manifest come from the same file of a directory holding
// several candidates. Both loaders discover their file through
// configpaths.FindConfigFile, so no second lookup can silently make them read
// different files.
func TestLauncherAndHomeLoadersSelectTheSameConfigFile(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "config.yaml", "cpus: 1\nhome:\n  .sandbox/from-yaml: /tmp/from-yaml\n")
	testutil.WriteFile(t, dir, "config.json", `{"cpus": 99, "home": {".sandbox/from-json": "/tmp/from-json"}}`)
	testutil.WriteFile(t, dir, "config.json5", `{cpus: 7}`)

	path, ext, ok := configpaths.FindConfigFile(dir)
	if !ok || path != filepath.Join(dir, "config.yaml") || ext != ".yaml" {
		t.Fatalf(
			"configpaths.FindConfigFile(%q) = %q, %q, %v; want %q, .yaml, true",
			dir, path, ext, ok, filepath.Join(dir, "config.yaml"),
		)
	}

	v := viper.New()
	if err := mergeDir(v, dir); err != nil {
		t.Fatalf("mergeDir(%q): %v", dir, err)
	}
	if got := v.GetUint8("cpus"); got != 1 {
		t.Errorf("launcher config cpus = %d, want 1 from config.yaml", got)
	}

	layers, has, err := homeconfig.LoadLayers([]string{dir})
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if !has {
		t.Fatal("LoadLayers reported no home manifest")
	}
	if got := layers[0].Manifest[".sandbox/from-yaml"].Source; got != "/tmp/from-yaml" {
		t.Errorf("home manifest %v is not the config.yaml layer", layers[0].Manifest)
	}
	if _, ok := layers[0].Manifest[".sandbox/from-json"]; ok {
		t.Errorf("home manifest read config.json, got %v", layers[0].Manifest)
	}
}
