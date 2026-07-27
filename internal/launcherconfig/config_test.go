package launcherconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadMissingFilesReturnsDefaults(t *testing.T) {
	cfg, keys, err := Load("", "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected no keys, got %v", keys)
	}
	if cfg.CPUs != 0 || cfg.Memory != "" || cfg.TmpSize != "" || cfg.Yes || cfg.Verbose ||
		cfg.Quiet || cfg.Rebuild || cfg.AutoPruneAge != 0 || cfg.ManualPruneAge != 0 {
		t.Errorf("expected zero defaults, got %+v", cfg)
	}
}

func TestLoadYAMLConfig(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "config.yaml", map[string]any{
		"cpus":     4,
		"memory":   "8G",
		"tmp-size": "4G",
		"rebuild":  true,
		"verbose":  true,
	})

	cfg, keys, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.CPUs != 4 {
		t.Errorf("expected cpus 4, got %d", cfg.CPUs)
	}
	if cfg.Memory != "8G" {
		t.Errorf("expected memory 8G, got %q", cfg.Memory)
	}
	if cfg.TmpSize != "4G" {
		t.Errorf("expected tmp-size 4G, got %q", cfg.TmpSize)
	}
	if !cfg.Rebuild || !cfg.Verbose {
		t.Errorf("expected rebuild and verbose true, got %+v", cfg)
	}
	if !keys["cpus"] || !keys["memory"] || !keys["tmp-size"] {
		t.Errorf("expected cpus, memory, and tmp-size keys, got %v", keys)
	}
}

func TestLoadJSON5Config(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.json5"), `{
		// a comment
		"cpus": 2,
		"memory": "512M",
		"yes": true
	}`)

	cfg, keys, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.CPUs != 2 || cfg.Memory != "512M" || !cfg.Yes {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if !keys["cpus"] || !keys["yes"] {
		t.Errorf("expected cpus and yes keys, got %v", keys)
	}
}

func TestLoadProjectOverridesUser(t *testing.T) {
	user := t.TempDir()
	project := t.TempDir()
	writeYAML(t, user, "config.yaml", map[string]any{
		"cpus":   2,
		"memory": "4G",
		"yes":    true,
	})
	writeYAML(t, project, "config.yaml", map[string]any{
		"memory": "8G",
		"yes":    false,
	})

	cfg, keys, err := Load(user, project)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.CPUs != 2 {
		t.Errorf("expected cpus 2 from user, got %d", cfg.CPUs)
	}
	if cfg.Memory != "8G" {
		t.Errorf("expected memory 8G from project, got %q", cfg.Memory)
	}
	if cfg.Yes {
		t.Error("expected yes=false from project override")
	}
	if !keys["cpus"] || !keys["memory"] || !keys["yes"] {
		t.Errorf("expected all keys set, got %v", keys)
	}
}

func TestLoadInvalidCPUs(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "config.yaml", map[string]any{"cpus": 300})

	_, _, err := Load(dir, "")
	if err == nil {
		t.Fatal("expected error for cpus > 255")
	}
}

func TestLoadMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.json5"), "{")

	_, _, err := Load(dir, "")
	if err == nil {
		t.Fatal("expected error for malformed config")
	}
}

func TestLoadPruneAgeConfig(t *testing.T) {
	for _, tc := range []struct {
		name       string
		json       string
		wantAuto   time.Duration
		wantManual time.Duration
	}{
		{
			name:       "7d and 14d",
			json:       `{"auto-prune-age": "7d", "manual-prune-age": "14d"}`,
			wantAuto:   7 * 24 * time.Hour,
			wantManual: 14 * 24 * time.Hour,
		},
		{
			name:       "hours",
			json:       `{"auto-prune-age": "48h"}`,
			wantAuto:   48 * time.Hour,
			wantManual: 0,
		},
		{
			name:       "minutes",
			json:       `{"manual-prune-age": "60m"}`,
			wantAuto:   0,
			wantManual: time.Hour,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "config.json"), tc.json)

			cfg, keys, err := Load(dir, "")
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}
			if cfg.AutoPruneAge != tc.wantAuto {
				t.Errorf("AutoPruneAge: got %v, want %v", cfg.AutoPruneAge, tc.wantAuto)
			}
			if cfg.ManualPruneAge != tc.wantManual {
				t.Errorf("ManualPruneAge: got %v, want %v", cfg.ManualPruneAge, tc.wantManual)
			}
			if tc.wantAuto != 0 && !keys["auto-prune-age"] {
				t.Error("expected auto-prune-age in keys")
			}
			if tc.wantManual != 0 && !keys["manual-prune-age"] {
				t.Error("expected manual-prune-age in keys")
			}
		})
	}
}

func TestLoadInvalidPruneAge(t *testing.T) {
	for _, tc := range []struct {
		key       string
		value     string
		errSuffix string
	}{
		{key: "auto-prune-age", value: "0", errSuffix: "auto-prune-age must be > 0"},
		{key: "manual-prune-age", value: "-1d", errSuffix: "manual-prune-age must be > 0"},
		{key: "auto-prune-age", value: "-10h", errSuffix: "auto-prune-age must be > 0"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			dir := t.TempDir()
			writeYAML(t, dir, "config.yaml", map[string]any{tc.key: tc.value})

			_, _, err := Load(dir, "")
			if err == nil {
				t.Fatalf("expected error for %s=%s", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.errSuffix) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errSuffix)
			}
		})
	}
}

//nolint:unparam // name is always "config.yaml" but kept for clarity
func writeYAML(t *testing.T, dir, name string, v map[string]any) {
	t.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal yaml: %v", err)
	}
	writeFile(t, filepath.Join(dir, name), string(data))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
