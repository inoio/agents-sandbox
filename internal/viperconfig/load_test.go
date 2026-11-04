package viperconfig

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/upgrade"
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

	layers := ConfigLayers(configpaths.Get(), "widget-bbb")

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

func TestResolverQuietAndProvisionHostConfigFalse(t *testing.T) {
	if NewResolverWithConfig(Config{Quiet: false}).Quiet() {
		t.Error("Quiet() = true, want false")
	}
	if !NewResolverWithConfig(Config{Quiet: true}).Quiet() {
		t.Error("Quiet() = false, want true")
	}
	if NewResolverWithConfig(Config{ProvisionHostConfig: false}).ProvisionHostConfig() {
		t.Error("ProvisionHostConfig() = true, want false")
	}
	if !NewResolverWithConfig(Config{ProvisionHostConfig: true}).ProvisionHostConfig() {
		t.Error("ProvisionHostConfig() = false, want true")
	}
}

// A 1ns interval is positive but below the floor, so the <= 0 guard must not
// swallow it.
func TestResolverUpgradeIntervalOneNanosecond(t *testing.T) {
	r := NewResolverWithConfig(Config{Upgrade: UpgradeConfig{Interval: time.Nanosecond}})
	if got := r.UpgradeInterval(); got != upgrade.MinInterval {
		t.Errorf("UpgradeInterval() = %v, want %v", got, upgrade.MinInterval)
	}
}

func TestResolverGettersReturnConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cfg := Config{
		CPUs: 4, Memory: "8G", TmpSize: "4G", DiskSize: "32G", WorkspaceQuota: "64G",
		Yes: true, LogLevel: "verbose", Quiet: true,
		AutoPruneAge: 7 * 24 * time.Hour, ManualPruneAge: 14 * 24 * time.Hour,
		AutoStopOnActiveSessions: true, AutoStopTimeout: 30 * time.Second, AutoStopMaxSessionRetries: 5,
		Agent: "pi", ProvisionHostConfig: true,
	}
	r := NewResolverWithConfig(cfg)
	if r.cfg.CPUs != 4 || r.cfg.Memory != "8G" || r.cfg.TmpSize != "4G" || r.cfg.DiskSize != "32G" ||
		r.cfg.WorkspaceQuota != "64G" {
		t.Errorf("resource getters mismatch: %+v", cfg)
	}
	if !r.Yes() || r.LogLevel() != "verbose" || !r.Quiet() {
		t.Error("UI getters mismatch")
	}
	if r.Agent() != "pi" || !r.ProvisionHostConfig() {
		t.Error("agent getters mismatch")
	}
	if r.AutoPruneAge() != 7*24*time.Hour || r.ManualPruneAge() != 14*24*time.Hour {
		t.Error("prune getters mismatch")
	}
	if !r.cfg.AutoStopOnActiveSessions ||
		r.cfg.AutoStopTimeout != 30*time.Second || r.cfg.AutoStopMaxSessionRetries != 5 {
		t.Error("autostop getters mismatch")
	}
}

func TestResolverWorkspaceQuotaGetter(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cfg := Config{WorkspaceQuota: "16G"}
	r := NewResolverWithConfig(cfg)
	if r.cfg.WorkspaceQuota != "16G" {
		t.Errorf("WorkspaceQuota = %q; want 16G", r.cfg.WorkspaceQuota)
	}
}

func TestResolverAgentGetter(t *testing.T) {
	r := NewResolverWithConfig(Config{Agent: "pi"})
	if r.Agent() != "pi" {
		t.Errorf("Agent = %q; want pi", r.Agent())
	}
}

func TestDockerDefaultsFalse(t *testing.T) {
	r := NewResolverWithConfig(Config{})
	if r.Config().Docker {
		t.Error("Config().Docker = true, want default false")
	}
}

func TestResolverUpgradeGetters(t *testing.T) {
	r := NewResolverWithConfig(Config{
		Upgrade: UpgradeConfig{Mode: "auto", Interval: 2 * time.Hour},
	})
	if r.UpgradeMode() != upgrade.ModeAuto {
		t.Fatalf("UpgradeMode = %q, want %q", r.UpgradeMode(), upgrade.ModeAuto)
	}
	if r.UpgradeInterval() != 2*time.Hour {
		t.Fatalf("UpgradeInterval = %v, want 2h", r.UpgradeInterval())
	}
}

func TestResolverUpgradeDefaults(t *testing.T) {
	r := NewResolverWithConfig(Config{})
	if r.UpgradeMode() != upgrade.ModePrompt {
		t.Fatalf("UpgradeMode default = %q, want prompt", r.UpgradeMode())
	}
	if r.UpgradeInterval() != upgrade.DefaultInterval {
		t.Fatalf("UpgradeInterval default = %v, want %v", r.UpgradeInterval(), upgrade.DefaultInterval)
	}
}

func TestResolverUpgradeIntervalClampsMin(t *testing.T) {
	r := NewResolverWithConfig(Config{Upgrade: UpgradeConfig{Interval: time.Minute}})
	if r.UpgradeInterval() != upgrade.MinInterval {
		t.Fatalf("UpgradeInterval = %v, want clamped to %v", r.UpgradeInterval(), upgrade.MinInterval)
	}
}

func TestResolverUpgradeChannel(t *testing.T) {
	if got := NewResolverWithConfig(Config{}).UpgradeChannel(); got != upgrade.ChannelStable {
		t.Fatalf("UpgradeChannel default = %q, want stable", got)
	}
	r := NewResolverWithConfig(Config{Upgrade: UpgradeConfig{Channel: "canary"}})
	if got := r.UpgradeChannel(); got != upgrade.ChannelCanary {
		t.Fatalf("UpgradeChannel = %q, want canary", got)
	}
	// Unparsable values are possible via NewResolverWithConfig, which skips
	// validation, and fall back to stable.
	if got := NewResolverWithConfig(
		Config{Upgrade: UpgradeConfig{Channel: "bogus"}},
	).UpgradeChannel(); got != upgrade.ChannelStable {
		t.Fatalf("UpgradeChannel(bogus) = %q, want stable", got)
	}
}

func TestResolverNotifyDefaults(t *testing.T) {
	r := NewResolverWithConfig(Config{})
	cfg := resolverNotify(r)
	if cfg.Active() {
		t.Errorf("default notify should be inactive, got %+v", cfg)
	}
	if cfg.Audio != notify.AudioOff {
		t.Errorf("default audio = %q, want off", cfg.Audio)
	}
}

// TestResolverConfigReturnsACopy covers the accessor runoptions.Build consumes:
// a non-nil resolver returns a deep copy of its config, and a nil resolver
// returns nil (which runoptions.Build treats as "no config").
func TestResolverConfigReturnsACopy(t *testing.T) {
	r := NewResolverWithConfig(Config{
		CPUs: 4, Agent: "pi", OpenLinks: true,
		Network: network.Policy{
			Profiles:    []network.Profile{network.ProfilePublic},
			EgressAllow: []string{"example.com"},
			EgressDeny:  []string{"bad.example"},
			DNSServers:  []string{"1.1.1.1"},
		},
		Mounts: mounts.Mounts{"/guest": {Source: "/host"}},
		Home:   homeconfig.Layers{{Dir: "/cfg", Manifest: homeconfig.Manifest{"~/.x": {Source: "/host/.x"}}}},
		Secrets: Secrets{
			Common: SecretCommon{AllowPlaceholderFor: []string{"model.example"}},
		},
	})

	got := r.Config()
	if got == nil {
		t.Fatal("Config() = nil, want the resolved config")
	}
	if got.CPUs != 4 || got.Agent != "pi" || !got.OpenLinks {
		t.Errorf("Config() = %+v, want cpus 4, agent pi, open-links true", *got)
	}

	// The returned config must be a deep copy: mutating any field, including the
	// slices and maps, cannot change later resolver reads.
	got.Agent = "claudecode"
	got.Network.Profiles[0] = network.ProfileHost
	got.Network.EgressAllow[0] = "evil.example"
	got.Network.EgressDeny[0] = "evil.example"
	got.Network.DNSServers[0] = "8.8.8.8"
	got.Secrets.Common.AllowPlaceholderFor[0] = "evil.example"
	got.Mounts["/guest"] = mounts.BindMount{Source: "/evil"}
	got.Home[0].Manifest["~/.x"] = homeconfig.Entry{Source: "/evil"}

	again := r.Config()
	if again.Agent != "pi" {
		t.Errorf("mutating Config().Agent changed the resolver: Agent = %q, want pi", again.Agent)
	}
	if again.Network.Profiles[0] != network.ProfilePublic ||
		again.Network.EgressAllow[0] != "example.com" ||
		again.Network.EgressDeny[0] != "bad.example" ||
		again.Network.DNSServers[0] != "1.1.1.1" {
		t.Errorf("mutating Config() network changed the resolver: %+v", again.Network)
	}
	if again.Mounts["/guest"].Source != "/host" {
		t.Errorf("mutating Config() mounts changed the resolver: %+v", again.Mounts)
	}
	if again.Secrets.Common.AllowPlaceholderFor[0] != "model.example" {
		t.Errorf("mutating Config() secrets changed the resolver: %+v", again.Secrets)
	}
	if again.Home[0].Manifest["~/.x"].Source != "/host/.x" {
		t.Errorf("mutating Config() home changed the resolver: %+v", again.Home)
	}

	var nilResolver *Resolver
	if nilResolver.Config() != nil {
		t.Error("Config() on a nil resolver = non-nil, want nil")
	}
}

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

// configType returns "" for unsupported extensions.
func TestConfigTypeUnknown(t *testing.T) {
	if got := configType(".toml"); got != "" {
		t.Errorf("configType(.toml) = %q; want \"\"", got)
	}
	if got := configType(".JSON5"); got != "json" {
		t.Errorf("configType(.JSON5) = %q; want json", got)
	}
}
