package driver

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestSeedsMergeLetsTheOverlayWin(t *testing.T) {
	base := Seeds{HomeFiles: map[string]string{"a": "base", "b": "base"}, HomeManifest: map[string]string{"m": "base"}}
	overlay := Seeds{HomeFiles: map[string]string{"b": "overlay"}, AgentSnippets: map[string]string{"s": "overlay"}}

	merged := base.Merge(overlay)

	if want := map[string]string{"a": "base", "b": "overlay"}; !maps.Equal(merged.HomeFiles, want) {
		t.Errorf("HomeFiles = %v, want %v", merged.HomeFiles, want)
	}
	if !maps.Equal(merged.HomeManifest, base.HomeManifest) || !maps.Equal(merged.AgentSnippets, overlay.AgentSnippets) {
		t.Errorf("merged = %+v, want each side's routes kept", merged)
	}
	if base.HomeFiles["b"] != "base" {
		t.Error("Merge changed its receiver's map")
	}
	if empty := (Seeds{}).Merge(
		Seeds{},
	); empty.HomeFiles != nil || empty.HomeManifest != nil ||
		empty.AgentSnippets != nil {
		t.Errorf("empty merge = %+v, want nil maps", empty)
	}
}

func TestSeedsApplyWritesEachRoute(t *testing.T) {
	home := t.TempDir()
	seeds := Seeds{
		HomeFiles:     map[string]string{".agent/settings.json": "host"},
		HomeManifest:  map[string]string{".state.json": "manifest"},
		AgentSnippets: map[string]string{"config-x.toml": "snippet"},
	}

	if err := seeds.Apply(home, "agent"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for path, want := range map[string]string{
		filepath.Join(home, "home", ".agent", "settings.json"):                              "host",
		filepath.Join(home, configDirName, launcherConfigDirName, "agent", "config-x.toml"): "snippet",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
	if _, err := os.Stat(
		filepath.Join(home, configDirName, launcherConfigDirName, launcherConfigFileName),
	); err != nil {
		t.Errorf("the launcher config with the home: manifest was not written: %v", err)
	}
}
