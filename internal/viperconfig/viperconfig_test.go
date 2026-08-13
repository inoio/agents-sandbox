package viperconfig

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

func TestLoadMissingFilesReturnsDefaults(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cfg, keys, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected no keys, got %v", keys)
	}
	if cfg.CPUs != 0 || cfg.Memory != "" || cfg.TmpSize != "" || cfg.DiskSize != "" || cfg.Yes || cfg.Verbose ||
		cfg.Quiet || cfg.Rebuild || cfg.AutoPruneAge != 0 || cfg.ManualPruneAge != 0 {
		t.Errorf("expected zero defaults, got %+v", cfg)
	}
}

func TestLoadYAMLConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"cpus":     4,
		"memory":   "8G",
		"tmp-size": "4G",
		"rebuild":  true,
		"verbose":  true,
	})

	cfg, keys, err := Load()
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
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	testutil.WritePath(t, filepath.Join(cp.UserConfigDir(), "config.json5"), `{
		// a comment
		"cpus": 2,
		"memory": "512M",
		"yes": true
	}`)

	cfg, keys, err := Load()
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
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"cpus":   2,
		"memory": "4G",
		"yes":    true,
	})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{
		"memory": "8G",
		"yes":    false,
	})

	cfg, keys, err := Load()
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
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 300})

	_, _, err := Load()
	if err == nil {
		t.Fatal("expected error for cpus > 255")
	}
}

func TestLoadMalformedConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	testutil.WritePath(t, filepath.Join(cp.UserConfigDir(), "config.json5"), "{")

	_, _, err := Load()
	if err == nil {
		t.Fatal("expected error for malformed config")
	}
}

func TestLoadPruneAgeConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
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
			cp := configpaths.GetConfigPaths()
			testutil.WritePath(t, filepath.Join(cp.UserConfigDir(), "config.json"), tc.json)

			cfg, keys, err := Load()
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

func TestLoadDiskSizeConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"disk-size": "24G",
	})

	cfg, keys, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.DiskSize != "24G" {
		t.Errorf("expected disk-size 24G, got %q", cfg.DiskSize)
	}
	if !keys["disk-size"] {
		t.Error("expected disk-size key")
	}
}

func TestLoadInvalidPruneAge(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
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
			cp := configpaths.GetConfigPaths()
			testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{tc.key: tc.value})

			_, _, err := Load()
			if err == nil {
				t.Fatalf("expected error for %s=%s", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.errSuffix) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errSuffix)
			}
		})
	}
}

func TestConfigReapPolicyDefaults(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cfg, _, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	rp := options.NewReapPolicy(cfg.AutoStopOnActiveSessions, cfg.AutoStopMaxSessionRetries)
	if rp.AutoStopOnActiveSessions {
		t.Error("expected AutoStopOnActiveSessions false by default")
	}
	if rp.MaxSessionRetries != 10 {
		t.Errorf("expected MaxSessionRetries 10 by default, got %d", rp.MaxSessionRetries)
	}
}

func TestConfigIdleTimeoutDefault(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cfg, _, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	want := 10 * time.Second
	if cfg.IdleTimeout() != want {
		t.Errorf("expected IdleTimeout %v by default, got %v", want, cfg.IdleTimeout())
	}
}

func TestConfigAutoStopOnActiveSessions(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"auto-stop-on-active-sessions": true,
	})

	cfg, _, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.AutoStopOnActiveSessions {
		t.Error("expected AutoStopOnActiveSessions true")
	}
}

func TestConfigAutoStopTimeoutParsing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	for _, tc := range []struct {
		name string
		json string
		want time.Duration
	}{
		{
			name: "60s",
			json: `{"auto-stop-timeout": "60s"}`,
			want: 60 * time.Second,
		},
		{
			name: "2m",
			json: `{"auto-stop-timeout": "2m"}`,
			want: 2 * time.Minute,
		},
		{
			name: "1h30m",
			json: `{"auto-stop-timeout": "1h30m"}`,
			want: 90 * time.Minute,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp := configpaths.GetConfigPaths()
			testutil.WritePath(t, filepath.Join(cp.UserConfigDir(), "config.json"), tc.json)

			cfg, _, err := Load()
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}
			if cfg.AutoStopTimeout != tc.want {
				t.Errorf("AutoStopTimeout: got %v, want %v", cfg.AutoStopTimeout, tc.want)
			}
			if cfg.IdleTimeout() != tc.want {
				t.Errorf("IdleTimeout: got %v, want %v", cfg.IdleTimeout(), tc.want)
			}
		})
	}
}

func TestConfigAutoStopMaxSessionRetries(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"auto-stop-max-session-retries": 5,
	})

	cfg, _, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.AutoStopMaxSessionRetries != 5 {
		t.Errorf("AutoStopMaxSessionRetries: got %d, want 5", cfg.AutoStopMaxSessionRetries)
	}

	rp := options.NewReapPolicy(cfg.AutoStopOnActiveSessions, cfg.AutoStopMaxSessionRetries)
	if rp.MaxSessionRetries != 5 {
		t.Errorf("ReapPolicy.MaxSessionRetries: got %d, want 5", rp.MaxSessionRetries)
	}
}

func TestConfigAutoStopNegativeMaxSessionRetries(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.GetConfigPaths()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"auto-stop-max-session-retries": -1,
	})

	_, _, err := Load()
	if err == nil {
		t.Fatal("expected error for negative auto-stop-max-session-retries")
	}
	if !strings.Contains(err.Error(), "auto-stop-max-session-retries") {
		t.Errorf("error %q does not mention auto-stop-max-session-retries", err.Error())
	}
}

func TestConfigAutoStopNegativeTimeout(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	for _, tc := range []struct {
		name   string
		value  any
		errSfx string
	}{
		{name: "negative seconds", value: "-5s", errSfx: "auto-stop-timeout"},
		{name: "negative hours", value: "-1h", errSfx: "auto-stop-timeout"},
		{name: "negative complex", value: "-1h30m", errSfx: "auto-stop-timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp := configpaths.GetConfigPaths()
			testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
				"auto-stop-timeout": tc.value,
			})

			_, _, err := Load()
			if err == nil {
				t.Fatalf("expected error for auto-stop-timeout=%v", tc.value)
			}
			if !strings.Contains(err.Error(), tc.errSfx) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errSfx)
			}
		})
	}
}
