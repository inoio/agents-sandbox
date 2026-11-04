package viperconfig

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/upgrade"
)

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

	r, err := NewResolver(nil, configpaths.Get(), "widget-bbb")
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

// TestConfigAllowPlaceholderForFromFile pins that the config-file-only
// secrets.common.allow-placeholder-for key reaches Config.
func TestConfigAllowPlaceholderForFromFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"secrets": map[string]any{
			"common": map[string]any{
				"allow-placeholder-for": []string{"model.example"},
			},
		},
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	want := []string{"model.example"}
	if got := r.cfg.Secrets.Common.AllowPlaceholderFor; !slices.Equal(got, want) {
		t.Errorf("Secrets.Common.AllowPlaceholderFor = %v, want %v", got, want)
	}
}

// provision-host-config is derived like every other key: its env var must be
// honored, and false must survive the on-by-default setting.
func TestResolverProvisionHostConfigEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_PROVISION_HOST_CONFIG", "false")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.ProvisionHostConfig() {
		t.Error("ProvisionHostConfig = true, want AGENTS_SANDBOX_PROVISION_HOST_CONFIG=false to disable it")
	}
}

// Per-slug mergeDir failure propagates out of NewResolver.
func TestNewResolverPerSlugMergeError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	slugDir := filepath.Join(cp.UserConfigDir(), "slug")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatalf("mkdir per-slug: %v", err)
	}
	testutil.WriteFile(t, slugDir, "config.yaml", "cpus: 2\n  memory: [\n")

	_, err := NewResolver(nil, configpaths.Get(), "slug")
	if err == nil {
		t.Fatal("expected error for malformed per-slug config")
	}
}

// Project dir mergeDir failure propagates out of NewResolver.
func TestNewResolverProjectMergeError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "cpus: 2\n  memory: [\n")

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error for malformed project config")
	}
}

// A config.yaml that is a directory makes os.ReadFile fail inside mergeDir.
func TestNewResolverUnreadableConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	if err := os.MkdirAll(filepath.Join(cp.UserConfigDir(), "config.yaml"), 0o755); err != nil {
		t.Fatalf("mkdir config.yaml: %v", err)
	}

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error reading a directory as config")
	}
	if !strings.Contains(err.Error(), "read launcher config") {
		t.Errorf("error %q does not name the read failure", err)
	}
	if _, ok := errors.AsType[*os.PathError](err); !ok {
		t.Errorf("error %q does not wrap the underlying path error", err)
	}
}

// A malformed json5 config makes json5.Unmarshal fail inside mergeDir.
func TestNewResolverInvalidJSON5(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.json5", `{"cpus": }`)

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error for malformed json5 config")
	}
	if !strings.Contains(err.Error(), "parse launcher config") {
		t.Errorf("error %q does not name the parse failure", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

// A json5 value that json.Marshal cannot serialize (NaN) makes the normalize
// step fail inside mergeDir.
func TestNewResolverJSON5MarshalError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.json5", `{"cpus": NaN}`)

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error for non-serializable json5 config")
	}
	if !strings.Contains(err.Error(), "normalize launcher config") {
		t.Errorf("error %q does not name the normalize failure", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

// A plain .json config with invalid JSON triggers the non-yaml MergeConfig
// error path.
func TestNewResolverInvalidJSON(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.json", `{"cpus": }`)

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error for malformed json config")
	}
	if !strings.Contains(err.Error(), "load launcher config") {
		t.Errorf("error %q does not name the load failure", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

// auto-stop-timeout of 0 makes validate() reject via validateAutoStopTimeout.
func TestNewResolverInvalidAutoStopTimeout(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"auto-stop-timeout": 0})

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error for auto-stop-timeout 0")
	}
}

// A positive prune age passes validatePruneAges without error.
func TestNewResolverValidPruneAge(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"auto-prune-age": "7d"})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.AutoPruneAge() != 7*24*time.Hour {
		t.Errorf("AutoPruneAge = %v, want 7d", r.AutoPruneAge())
	}
}

// A config value of the wrong shape for a typed field (a list for the string
// memory key) makes viper.Unmarshal fail, surfacing as a decode error.
func TestNewResolverSurfacesDecodeError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": []any{1, 2}})

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected a decode error for a list-valued memory key")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("error = %v, want it to name the offending memory key", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error = %v, want it to wrap its cause", err)
	}
}

// Config.DockerSource reports the layer the resolved docker value came from, so
// the run and build paths can rank their flags against it.
func TestConfigDockerSource(t *testing.T) {
	tests := []struct {
		name      string
		env       [][2]string
		configYML string
		flag      string
		want      DockerSource
	}{
		{name: "nothing set", want: DockerSourceNone},
		{
			name:      "canonical config key",
			configYML: "docker: false\n",
			want:      DockerSourceDockerConfig,
		},
		{
			name: "canonical env",
			env:  [][2]string{{"AGENTS_SANDBOX_DOCKER", "true"}},
			want: DockerSourceDockerEnv,
		},
		{
			name: "deprecated canonical env prefix",
			env:  [][2]string{{"OPENCODE_SANDBOX_DOCKER", "true"}},
			want: DockerSourceDockerEnv,
		},
		{
			name: "canonical flag",
			flag: KeyDocker,
			want: DockerSourceDockerFlag,
		},
		{
			name:      "deprecated dind config key",
			configYML: "dind: true\n",
			want:      DockerSourceDindConfig,
		},
		{
			name: "deprecated dind env",
			env:  [][2]string{{"AGENTS_SANDBOX_DIND", "true"}},
			want: DockerSourceDindEnv,
		},
		{
			name: "deprecated dind env prefix",
			env:  [][2]string{{"OPENCODE_SANDBOX_DIND", "true"}},
			want: DockerSourceDindEnv,
		},
		{
			name:      "canonical config beats a dind env",
			env:       [][2]string{{"AGENTS_SANDBOX_DIND", "true"}},
			configYML: "docker: false\n",
			want:      DockerSourceDockerConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			for _, kv := range tt.env {
				t.Setenv(kv[0], kv[1])
			}
			if tt.configYML != "" {
				testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", tt.configYML)
			}
			var cmd *cobra.Command
			if tt.flag != "" {
				cmd = &cobra.Command{Use: "root"}
				cmd.Flags().Bool(tt.flag, false, "")
				if err := cmd.Flags().Set(tt.flag, "true"); err != nil {
					t.Fatalf("set %s: %v", tt.flag, err)
				}
			}

			r, err := NewResolver(cmd, configpaths.Get(), "")
			if err != nil {
				t.Fatalf("NewResolver: %v", err)
			}
			if got := r.Config().DockerSource(); got != tt.want {
				t.Errorf("Config().DockerSource() = %d, want %d", got, tt.want)
			}
		})
	}
}

// A command with no canonical --docker flag registered is not a docker source,
// so the resolution falls through to the config layers.
func TestConfigDockerSourceWithoutDockerFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	root := &cobra.Command{Use: "root"}

	r, err := NewResolver(root, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.Config().DockerSource(); got != DockerSourceNone {
		t.Errorf("Config().DockerSource() = %d, want none", got)
	}
}

func TestResolverAgentsSandboxEnvPrefix(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	t.Setenv("AGENTS_SANDBOX_CPUS", "6")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 6 {
		t.Errorf("CPUs = %d; want 6 (AGENTS_SANDBOX_ env overrides config)", r.cfg.CPUs)
	}
	if got := r.LegacyEnvVars(); len(got) != 0 {
		t.Errorf("LegacyEnvVars = %v; want none", got)
	}
}

func TestResolverLegacyEnvPrefixStillWorks(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_CPUS", "6")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 6 {
		t.Errorf("CPUs = %d; want 6 (legacy env still resolves)", r.cfg.CPUs)
	}
	if got := r.LegacyEnvVars(); !slices.Contains(got, "OPENCODE_SANDBOX_CPUS") {
		t.Errorf("LegacyEnvVars = %v; want it to contain OPENCODE_SANDBOX_CPUS", got)
	}
}

func TestResolverAgentsEnvTakesPrecedenceOverLegacy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_CPUS", "7")
	t.Setenv("OPENCODE_SANDBOX_CPUS", "6")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 7 {
		t.Errorf("CPUs = %d; want 7 (AGENTS_ wins over legacy)", r.cfg.CPUs)
	}
	if got := r.LegacyEnvVars(); len(got) != 0 {
		t.Errorf("LegacyEnvVars = %v; want none when the new prefix is set", got)
	}
}

func TestResolverDottedKeyUsesAgentsEnvPrefix(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_PROFILE", "private")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !slices.Equal(r.cfg.Network.Profiles, []network.Profile{network.ProfilePrivate}) {
		t.Errorf("Network.Profiles = %v; want [private]", r.cfg.Network.Profiles)
	}
}

// Host-config provisioning defaults to on with no configuration at all.
func TestResolverProvisionHostConfigDefaultsOn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.ProvisionHostConfig() {
		t.Error("ProvisionHostConfig() = false, want the default true")
	}
}

// With no docker source at all, neither docker nor a legacy dind alias is
// reported.
func TestResolveDockerAliasWithoutAnySource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Config().Docker {
		t.Error("Config().Docker = true, want false with no source")
	}
	if got := r.DockerLegacyAlias(); got != "" {
		t.Errorf("DockerLegacyAlias() = %q, want empty", got)
	}
}

// A dind config value of false is not a docker source.
func TestDindConfigFalseDoesNotEnableDocker(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().ProjectConfigDir(), "config.yaml", map[string]any{"dind": false})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Config().Docker {
		t.Error("Config().Docker = true, want false for dind: false")
	}
}

// An explicit docker=false env beats a deprecated dind config.
func TestDockerExplicitEnvFalseBeatsDindConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_DOCKER", "false")
	testutil.WriteYAML(t, configpaths.Get().ProjectConfigDir(), "config.yaml", map[string]any{"dind": true})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Config().Docker {
		t.Error("Config().Docker = true, want the explicit docker=false to win")
	}
	if got := r.DockerLegacyAlias(); got != "" {
		t.Errorf("DockerLegacyAlias() = %q, want empty for the explicit docker env", got)
	}
}

// The changed --docker flag marks docker explicit, so the dind config is not
// surfaced.
func TestDockerFlagExplicitBeatsDindConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().ProjectConfigDir(), "config.yaml", map[string]any{"dind": true})
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().Bool(KeyDocker, false, "")
	if err := root.ParseFlags([]string{"--" + KeyDocker}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	r, err := NewResolver(root, configpaths.Get(), "some-slug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.Config().Docker {
		t.Error("Config().Docker = false, want the explicit --docker to win")
	}
	if got := r.DockerLegacyAlias(); got != "" {
		t.Errorf("DockerLegacyAlias() = %q, want empty for the explicit --docker flag", got)
	}
}

// A registered-but-unchanged --docker flag is not explicit, so the dind config
// still wins.
func TestDockerFlagUnchangedDoesNotBeatDind(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().ProjectConfigDir(), "config.yaml", map[string]any{"dind": true})
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().Bool(KeyDocker, false, "")
	if err := root.ParseFlags(nil); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	r, err := NewResolver(root, configpaths.Get(), "some-slug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.Config().Docker {
		t.Error("Config().Docker = false, want the dind config to win when --docker is unset")
	}
	if got := r.DockerLegacyAlias(); got != "the dind config key" {
		t.Errorf("DockerLegacyAlias() = %q, want the dind config key", got)
	}
}

// A .jsonc file is parsed as JSON5, so a comment does not break it.
func TestNewResolverJSONCWithComment(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.jsonc", "{\n\t// a comment\n\t\"cpus\": 2\n}")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 2 {
		t.Errorf("CPUs = %d, want 2 from the commented jsonc file", r.cfg.CPUs)
	}
}

// A "7d" interval is only understood by ParseHumanDuration, so the upgrade
// validator must fall back to it.
func TestResolverUpgradeIntervalHumanDuration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_INTERVAL", "7d")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.UpgradeInterval(); got != 7*24*time.Hour {
		t.Errorf("UpgradeInterval() = %v, want 7d", got)
	}
}

// An interval exactly at the minimum is accepted.
func TestResolverUpgradeIntervalAtMinimum(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_INTERVAL", upgrade.MinInterval.String())

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.UpgradeInterval(); got != upgrade.MinInterval {
		t.Errorf("UpgradeInterval() = %v, want the minimum %v", got, upgrade.MinInterval)
	}
}

// A valid first prune age must not stop the loop before the second, invalid
// one is checked.
func TestResolverPruneAgesChecksEveryKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"auto-prune-age":   "1h",
		"manual-prune-age": "-1h",
	})

	if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
		t.Fatal("expected error for the invalid second prune age")
	}
}

func TestResolverMounts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"mounts": map[string]any{
			"/home/dev/.m2": "~/.m2",
			"/home/dev/ref": map[string]any{"source": "/opt/company/reference", "readonly": true},
		},
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if len(r.cfg.Mounts) != 2 {
		t.Fatalf("Mounts = %+v", r.cfg.Mounts)
	}
	if got := r.cfg.Mounts["/home/dev/.m2"]; got.Source != "~/.m2" || got.Readonly {
		t.Errorf("Mounts = %+v", r.cfg.Mounts)
	}
	if got := r.cfg.Mounts["/home/dev/ref"]; got.Source != "/opt/company/reference" || !got.Readonly {
		t.Errorf("Mounts = %+v", r.cfg.Mounts)
	}
}

func TestResolverMountsMergeByTarget(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"mounts": map[string]any{
			"/home/dev/.m2":    "~/.m2",
			"/home/dev/shared": "/host/shared",
		},
	})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{
		"mounts": map[string]any{
			"/home/dev/.m2": map[string]any{"source": "/project/.m2", "readonly": true},
		},
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if len(r.cfg.Mounts) != 2 {
		t.Fatalf("Mounts = %+v", r.cfg.Mounts)
	}
	if got := r.cfg.Mounts["/home/dev/.m2"]; got.Source != "/project/.m2" || !got.Readonly {
		t.Errorf("project override = %+v", got)
	}
	if got := r.cfg.Mounts["/home/dev/shared"]; got.Source != "/host/shared" {
		t.Errorf("user mount = %+v", got)
	}
}

func TestResolverRejectsInvalidMountEntry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"mounts": map[string]any{
			"/home/dev/.m2": map[string]any{"readonly": "yes"},
		},
	})

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected invalid mount entry to fail")
	}
	for _, want := range []string{"decode launcher config", "/home/dev/.m2", "expected type 'bool'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

func TestResolverEnvPrecedenceOverConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	t.Setenv("AGENTS_SANDBOX_CPUS", "6")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 6 {
		t.Errorf("CPUs = %d; want 6 (env overrides config)", r.cfg.CPUs)
	}
}

func TestResolverConfigNoFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 3})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 3 {
		t.Errorf("CPUs = %d; want 3", r.cfg.CPUs)
	}
}

func TestResolverConfigSyntaxErrorIsFriendly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.yaml", "cpus: 2\n  memory: [\n")

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error for malformed config.yaml")
	}
	for _, want := range []string{"config.yaml", "invalid YAML", "line 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestResolverEnvKeyReplacement(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_AUTO_STOP_ON_ACTIVE_SESSIONS", "true")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.cfg.AutoStopOnActiveSessions {
		t.Error("expected AutoStopOnActiveSessions true from env")
	}
}

func TestResolverEnvInvalidCPUs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_CPUS", "300")

	if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
		t.Fatal("expected error for cpus=300 from env")
	}
}

// Flag-over-env-over-config precedence via a real cobra command.
func TestResolverFlagOverridesEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	t.Setenv("AGENTS_SANDBOX_CPUS", "4")

	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().Uint8("cpus", 0, "")
	if err := root.ParseFlags([]string{"--cpus", "6"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	r, err := NewResolver(root, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 6 {
		t.Errorf("CPUs = %d; want 6 (explicit flag overrides env/config)", r.cfg.CPUs)
	}
}

// An unspecified flag with a default must NOT override env/config.
func TestResolverUnspecifiedFlagDefaultDoesNotOverride(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": "8G"})

	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("memory", "4G", "") // default 4G, not changed
	r, err := NewResolver(root, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.Memory != "8G" {
		t.Errorf("Memory = %q; want 8G (config beats unspecified flag default)", r.cfg.Memory)
	}
}

// With no env/config, an unspecified flag's default is the resolution.
func TestResolverFlagDefaultUsedWhenNothingElse(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("memory", "4G", "")
	r, err := NewResolver(root, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.Memory != "4G" {
		t.Errorf("Memory = %q; want 4G (flag default)", r.cfg.Memory)
	}
}

// rebuild is not a config-backed key; a config file setting it is ignored.
func TestResolverIgnoresRebuildKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"rebuild": true, "cpus": 2})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 2 {
		t.Errorf("CPUs = %d; want 2", r.cfg.CPUs)
	}
	// There is no Rebuild getter; the field is dropped silently.
}

func TestResolverProjectOverridesUser(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"cpus":   2,
		"memory": "4G",
		"yes":    true,
	})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{
		"memory": "8G",
		"yes":    false,
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 2 {
		t.Errorf("CPUs = %d; want 2 from user config", r.cfg.CPUs)
	}
	if r.cfg.Memory != "8G" {
		t.Errorf("Memory = %q; want 8G from project override", r.cfg.Memory)
	}
	if r.Yes() {
		t.Error("expected yes=false from project override")
	}
}

func TestResolverInvalidPruneAgeFromFile(t *testing.T) {
	for _, tc := range []struct {
		key       string
		value     string
		errSuffix string
	}{
		{key: "auto-prune-age", value: "-1d", errSuffix: "auto-prune-age must be > 0"},
		{key: "manual-prune-age", value: "-10h", errSuffix: "manual-prune-age must be > 0"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			cp := configpaths.Get()
			testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{tc.key: tc.value})

			_, err := NewResolver(nil, configpaths.Get(), "")
			if err == nil {
				t.Fatalf("expected error for %s=%s", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.errSuffix) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errSuffix)
			}
		})
	}
}

func TestResolverInvalidAutoStopRetriesFromFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"auto-stop-max-session-retries": -1,
	})

	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error for negative auto-stop-max-session-retries")
	}
	if !strings.Contains(err.Error(), "auto-stop-max-session-retries") {
		t.Errorf("error %q does not mention auto-stop-max-session-retries", err.Error())
	}
}

func TestResolverJSON5Config(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.json5", `{
		// a comment
		"cpus": 2,
		"memory": "512M",
		"yes": true
	}`)

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 2 || r.cfg.Memory != "512M" || !r.Yes() {
		t.Errorf("unexpected config: cpus=%d memory=%q yes=%v", r.cfg.CPUs, r.cfg.Memory, r.Yes())
	}
}

func TestResolverJSONCConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), "config.jsonc", `{
		// a comment
		"cpus": 3,
		"memory": "256M",
		"yes": true
	}`)

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 3 || r.cfg.Memory != "256M" || !r.Yes() {
		t.Errorf("unexpected config: cpus=%d memory=%q yes=%v", r.cfg.CPUs, r.cfg.Memory, r.Yes())
	}
}

func TestResolverDiskSizeConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"disk-size": "24G"})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.DiskSize != "24G" {
		t.Errorf("DiskSize = %q; want 24G", r.cfg.DiskSize)
	}
}

func TestResolverWorkspaceQuotaConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"workspace-quota": "32G"})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.WorkspaceQuota != "32G" {
		t.Errorf("WorkspaceQuota = %q; want 32G", r.cfg.WorkspaceQuota)
	}
}

func TestResolverWorkspaceQuotaEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_WORKSPACE_QUOTA", "48G")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.WorkspaceQuota != "48G" {
		t.Errorf("WorkspaceQuota = %q; want 48G from env", r.cfg.WorkspaceQuota)
	}
}

func TestResolverReapPolicyDefaults(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	rp := options.NewReapPolicy(r.cfg.AutoStopOnActiveSessions, r.cfg.AutoStopMaxSessionRetries)
	if rp.AutoStopOnActiveSessions {
		t.Error("expected AutoStopOnActiveSessions false by default")
	}
	if rp.MaxSessionRetries != 10 {
		t.Errorf("expected MaxSessionRetries 10 by default, got %d", rp.MaxSessionRetries)
	}
}

// TestResolverIdleTimeoutUnsetFromConfig pins that the config layer does not
// substitute a default idle timeout: with nothing configured, AutoStopTimeout
// stays zero and the VM layer applies options.DefaultVMIdleTimeout.
func TestResolverIdleTimeoutUnsetFromConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.AutoStopTimeout != 0 {
		t.Errorf("AutoStopTimeout = %v; want 0 (default lives in the VM options layer)", r.cfg.AutoStopTimeout)
	}
}

func TestResolverAgentEnvVar(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_AGENT", "pi")
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Agent() != "pi" {
		t.Errorf("Agent = %q; want pi from env", r.Agent())
	}
}

func TestNetworkProfileEnvVar(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_PROFILE", "none")
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.cfg.Network.Effective(); !slices.Equal(got.Profiles, []network.Profile{network.ProfileNone}) {
		t.Fatalf("Network().Profiles = %v, want [none]", got.Profiles)
	}
}

func TestNetworkDefaultPublic(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	wantNetworkProfiles(t, r.cfg.Network, network.ProfilePublic)
}

func TestNetworkInvalidProfileRejected(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_PROFILE", "bogus")
	if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
		t.Fatal("expected error for invalid network profile")
	}
}

func TestNetworkDNSServersEnvVar(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_DNS_SERVERS", "1.1.1.1,8.8.8.8")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	got := r.cfg.Network.Effective()
	wantNetworkProfiles(t, got, network.ProfilePublic)
	if len(got.DNSServers) != 2 || got.DNSServers[0] != "1.1.1.1" || got.DNSServers[1] != "8.8.8.8" {
		t.Fatalf("Network().DNSServers = %v, want [1.1.1.1 8.8.8.8]", got.DNSServers)
	}
}

func TestNetworkDNSServersFromConfigFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"network": map[string]any{
			"profile":     "none",
			"dns-servers": []string{"1.1.1.1", "8.8.8.8:5353"},
		},
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	got := r.cfg.Network.Effective()
	wantNetworkProfiles(t, got, network.ProfileNone)
	if len(got.DNSServers) != 2 || got.DNSServers[0] != "1.1.1.1" || got.DNSServers[1] != "8.8.8.8:5353" {
		t.Fatalf("Network().DNSServers = %v, want [1.1.1.1 8.8.8.8:5353]", got.DNSServers)
	}
}

func TestNetworkDNSServersScalarConfigFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"network": map[string]any{"dns-servers": "1.1.1.1,8.8.8.8"},
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	got := r.cfg.Network.Effective().DNSServers
	if len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "8.8.8.8" {
		t.Fatalf("Network().DNSServers = %v, want [1.1.1.1 8.8.8.8]", got)
	}
}

func TestNetworkProfilesListFromConfigFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"network": map[string]any{
			"profile": []string{"public", "host"},
		},
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	wantNetworkProfiles(t, r.cfg.Network, network.ProfilePublic, network.ProfileHost)
}

func TestNetworkProfilesCommaEnvVar(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_PROFILE", "public,host")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	wantNetworkProfiles(t, r.cfg.Network, network.ProfilePublic, network.ProfileHost)
}

func TestNetworkProfilesSpacedCommaEnvVar(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_PROFILE", "public, host")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	wantNetworkProfiles(t, r.cfg.Network, network.ProfilePublic, network.ProfileHost)
}

func TestNetworkProfilesSpacedScalarConfigFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"network": map[string]any{"profile": "public, host"},
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	wantNetworkProfiles(t, r.cfg.Network, network.ProfilePublic, network.ProfileHost)
}

func TestNetworkProfilesSpacedListConfigFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"network": map[string]any{"profile": []string{"public", " host"}},
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	wantNetworkProfiles(t, r.cfg.Network, network.ProfilePublic, network.ProfileHost)
}

func TestNetworkProfilesNoneCombinedRejected(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"network": map[string]any{
			"profile": []string{"none", "public"},
		},
	})
	if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
		t.Fatal("expected error for none combined with another profile")
	}
}

func TestNetworkProfileInvalidTypeRejected(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"network": map[string]any{"profile": 42},
	})
	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error for a non-string network.profile")
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

func TestNetworkInvalidDNSServersRejected(t *testing.T) {
	cases := []struct {
		name string
		raw  any
	}{
		{name: "garbage from env", raw: "1.1.1.1,not a dns server"},
		{name: "host without port", raw: "1.1.1.1:"},
		{name: "empty entry from config", raw: []any{"1.1.1.1", ""}},
		{name: "non-string entry", raw: []any{42}},
		{name: "scalar number", raw: 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			if s, ok := tc.raw.(string); ok {
				t.Setenv("AGENTS_SANDBOX_NETWORK_DNS_SERVERS", s)
			} else {
				testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
					"network": map[string]any{"dns-servers": tc.raw},
				})
			}
			if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
				t.Fatal("expected error for invalid network.dns-servers")
			}
		})
	}
}

func TestPortsInvalidRejected(t *testing.T) {
	cases := []struct {
		name string
		raw  any
	}{
		{name: "garbage from env", raw: "3000,not a port"},
		{name: "port out of range", raw: "70000"},
		{name: "too many parts", raw: "8080:3000:5"},
		{name: "empty entry from config", raw: []any{"3000", ""}},
		{name: "non-string entry", raw: []any{42}},
		{name: "scalar number", raw: 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			if s, ok := tc.raw.(string); ok {
				t.Setenv("AGENTS_SANDBOX_PORTS", s)
			} else {
				testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
					"ports": tc.raw,
				})
			}
			if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
				t.Fatal("expected error for invalid ports")
			}
		})
	}
}

func TestPortsValidConfigAccepted(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"ports": []string{"3000", "8080:5173"},
	})
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	want := []string{"3000", "8080:5173"}
	if len(r.cfg.Ports) != len(want) {
		t.Fatalf("Ports = %v, want %v", r.cfg.Ports, want)
	}
	for i := range want {
		if r.cfg.Ports[i] != want[i] {
			t.Errorf("Ports[%d] = %q, want %q", i, r.cfg.Ports[i], want[i])
		}
	}
}

func TestPortsPublishFlagOverridesInvalidConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"ports": "not a port",
	})
	r, err := NewResolver(portsTestCmd(t, []string{"3000"}), configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !slices.Equal(r.cfg.Ports, []string{"3000"}) {
		t.Errorf("Ports = %v, want [3000] from the --port flag", r.cfg.Ports)
	}
}

func TestPortsInvalidPublishFlagRejected(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if _, err := NewResolver(portsTestCmd(t, []string{"not a port"}), configpaths.Get(), ""); err == nil {
		t.Fatal("expected error for an invalid --port flag value")
	}
}

func TestPortsUnspecifiedPublishFlagKeepsConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"ports": []string{"3000", "8080:5173"},
	})
	r, err := NewResolver(portsTestCmd(t, nil), configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	want := []string{"3000", "8080:5173"}
	if !slices.Equal(r.cfg.Ports, want) {
		t.Errorf("Ports = %v, want %v (the config values, untouched by the bound flag default)", r.cfg.Ports, want)
	}
}

func TestPortsDocumentedExampleLoads(t *testing.T) {
	// The example lives in docs/configuration/networking.md, under
	// "## Published ports". Loading it here keeps the docs honest: the bare
	// port must stay quoted, because an unquoted 3000 reaches validation as a
	// YAML int and is rejected (see the "non-string entry" case above).
	const docsPath = "../../docs/configuration/networking.md"
	raw, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatalf("read %s: %v", docsPath, err)
	}
	example, ok := documentedPortsExample(string(raw))
	if !ok {
		t.Fatalf("no ports example found in %s; keep the doc example and this test in sync", docsPath)
	}
	configpaths.WithMockConfigPaths(t)
	path := filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(example), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver(%q): %v", example, err)
	}
	want := []string{"3000", "8080:3000"}
	if !slices.Equal(r.cfg.Ports, want) {
		t.Errorf("Ports = %v, want %v", r.cfg.Ports, want)
	}
}

func TestDockerFromConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(configpaths.Get().ProjectConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configpaths.Get().ProjectConfigDir(), "config.yaml"),
		[]byte("docker: true\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	r, err := NewResolver(nil, configpaths.Get(), "some-slug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.Config().Docker {
		t.Error("Config().Docker = false, want true from config")
	}
	if r.DockerLegacyAlias() != "" {
		t.Errorf("DockerLegacyAlias() = %q, want empty for the canonical key", r.DockerLegacyAlias())
	}
}

// TestDindConfigAlias verifies the deprecated dind config key still enables
// docker and is surfaced as a legacy alias for a one-time warning.
func TestDindConfigAlias(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(configpaths.Get().ProjectConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configpaths.Get().ProjectConfigDir(), "config.yaml"),
		[]byte("dind: true\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	r, err := NewResolver(nil, configpaths.Get(), "some-slug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.Config().Docker {
		t.Error("Config().Docker = false, want true from the deprecated dind key")
	}
	if got := r.DockerLegacyAlias(); got != "the dind config key" {
		t.Errorf("DockerLegacyAlias() = %q, want the dind config key", got)
	}
}

// TestDockerConfigWinsOverDindAlias verifies the canonical key has precedence.
func TestDockerConfigWinsOverDindAlias(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(configpaths.Get().ProjectConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configpaths.Get().ProjectConfigDir(), "config.yaml"),
		[]byte("docker: false\ndind: true\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	r, err := NewResolver(nil, configpaths.Get(), "some-slug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Config().Docker {
		t.Error("Config().Docker = true, want the canonical docker key to win")
	}
}

// TestDockerExplicitFromEnv verifies AGENTS_SANDBOX_DOCKER marks docker as
// explicitly set, so the deprecated dind alias is not surfaced.
func TestDockerExplicitFromEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_DOCKER", "true")
	r, err := NewResolver(nil, configpaths.Get(), "some-slug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.Config().Docker {
		t.Error("Config().Docker = false, want true from AGENTS_SANDBOX_DOCKER")
	}
	if r.DockerLegacyAlias() != "" {
		t.Errorf("DockerLegacyAlias() = %q, want empty when docker is explicit", r.DockerLegacyAlias())
	}
}

// TestDockerExplicitFromLegacyEnv verifies the deprecated OPENCODE_SANDBOX_
// prefix is surfaced as explicit too, so a legacy value overrides a project
// Dockerfile in both directions instead of being read by viper then ignored.
func TestDockerExplicitFromLegacyEnv(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			t.Setenv("OPENCODE_SANDBOX_DOCKER", value)
			r, err := NewResolver(nil, configpaths.Get(), "some-slug")
			if err != nil {
				t.Fatalf("NewResolver: %v", err)
			}
			if !r.Config().DockerExplicit() {
				t.Error("Config().DockerExplicit() = false, want true from OPENCODE_SANDBOX_DOCKER")
			}
			if got, want := r.Config().Docker, value == "true"; got != want {
				t.Errorf("Config().Docker = %v, want %v from OPENCODE_SANDBOX_DOCKER", got, want)
			}
			if r.DockerLegacyAlias() != "" {
				t.Errorf("DockerLegacyAlias() = %q, want empty when docker is explicit", r.DockerLegacyAlias())
			}
		})
	}
}

// TestDockerExplicitFromFlag verifies the --docker flag marks docker as
// explicitly set.
func TestDockerExplicitFromFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().Bool(KeyDocker, false, "")
	if err := root.ParseFlags([]string{"--" + KeyDocker}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	r, err := NewResolver(root, configpaths.Get(), "some-slug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.Config().Docker {
		t.Error("Config().Docker = false, want true from --docker")
	}
	if r.DockerLegacyAlias() != "" {
		t.Errorf("DockerLegacyAlias() = %q, want empty for the --docker flag", r.DockerLegacyAlias())
	}
}

func TestDockerExplicit(t *testing.T) {
	t.Run("canonical config key", func(t *testing.T) {
		writeUserConfig(t, map[string]any{"docker": false})
		r, err := NewResolver(nil, configpaths.Get(), "")
		if err != nil {
			t.Fatalf("NewResolver: %v", err)
		}
		if !r.Config().DockerExplicit() {
			t.Error("docker key present must be explicit")
		}
		if r.Config().Docker {
			t.Error("docker key false must resolve off")
		}
	})
	t.Run("dind config key", func(t *testing.T) {
		writeUserConfig(t, map[string]any{"dind": false})
		r, err := NewResolver(nil, configpaths.Get(), "")
		if err != nil {
			t.Fatalf("NewResolver: %v", err)
		}
		if !r.Config().DockerExplicit() {
			t.Error("dind key present must be explicit")
		}
	})
	t.Run("nothing set", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		r, err := NewResolver(nil, configpaths.Get(), "")
		if err != nil {
			t.Fatalf("NewResolver: %v", err)
		}
		if r.Config().DockerExplicit() {
			t.Error("no docker source must not be explicit")
		}
	})
	t.Run("dind env", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		t.Setenv("AGENTS_SANDBOX_DIND", "true")
		r, err := NewResolver(nil, configpaths.Get(), "")
		if err != nil {
			t.Fatalf("NewResolver: %v", err)
		}
		if !r.Config().DockerExplicit() {
			t.Error("AGENTS_SANDBOX_DIND must be explicit")
		}
	})
}

// TestDindFromEnvAlias verifies AGENTS_SANDBOX_DIND enables docker and is
// reported as the deprecated source.
func TestDindFromEnvAlias(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_DIND", "true")
	r, err := NewResolver(nil, configpaths.Get(), "some-slug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.Config().Docker {
		t.Error("Config().Docker = false, want true from AGENTS_SANDBOX_DIND")
	}
	if got := r.DockerLegacyAlias(); got != "AGENTS_SANDBOX_DIND" {
		t.Errorf("DockerLegacyAlias() = %q, want AGENTS_SANDBOX_DIND", got)
	}
}

// TestDindFromLegacyEnvAlias verifies the deprecated OPENCODE_SANDBOX_DIND
// prefix enables docker and is reported with the generic dind source name.
func TestDindFromLegacyEnvAlias(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_DIND", "true")
	r, err := NewResolver(nil, configpaths.Get(), "some-slug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.Config().Docker {
		t.Error("Config().Docker = false, want true from OPENCODE_SANDBOX_DIND")
	}
	if got := r.DockerLegacyAlias(); got != "dind" {
		t.Errorf("DockerLegacyAlias() = %q, want the generic dind source for the legacy env prefix", got)
	}
}

func TestPerSlugConfigPrecedence(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// generic user: cpus=2; per-slug user: cpus=3; project: cpus=4
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	if err := os.MkdirAll(filepath.Join(cp.UserConfigDir(), "myslug"), 0o755); err != nil {
		t.Fatalf("mkdir per-slug: %v", err)
	}
	testutil.WriteYAML(t, filepath.Join(cp.UserConfigDir(), "myslug"), "config.yaml", map[string]any{"cpus": 3})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{"cpus": 4})

	r, err := NewResolver(nil, configpaths.Get(), "myslug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 4 {
		t.Fatalf("CPUs = %d; want 4 (project overrides per-slug user)", r.cfg.CPUs)
	}

	// Remove the project file; per-slug user must now win over generic user.
	os.Remove(filepath.Join(cp.ProjectConfigDir(), "config.yaml"))
	r2, err := NewResolver(nil, configpaths.Get(), "myslug")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r2.cfg.CPUs != 3 {
		t.Fatalf("CPUs = %d; want 3 (per-slug user overrides generic user)", r2.cfg.CPUs)
	}
}

func TestResolverUpgradeEnvVars(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_MODE", "notify")
	t.Setenv("AGENTS_SANDBOX_UPGRADE_INTERVAL", "2h")
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.UpgradeMode() != upgrade.ModeNotify {
		t.Fatalf("UpgradeMode = %q, want notify", r.UpgradeMode())
	}
	if r.UpgradeInterval() != 2*time.Hour {
		t.Fatalf("UpgradeInterval = %v, want 2h", r.UpgradeInterval())
	}
}

func TestResolverRejectsInvalidUpgradeMode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_MODE", "bogus")
	if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
		t.Fatal("expected error for invalid update mode")
	}
}

func TestResolverUpgradeChannelEnvVar(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_CHANNEL", "canary")
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.UpgradeChannel(); got != upgrade.ChannelCanary {
		t.Fatalf("UpgradeChannel = %q, want canary from AGENTS_SANDBOX_UPGRADE_CHANNEL", got)
	}
}

func TestResolverRejectsInvalidUpgradeChannel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_CHANNEL", "bogus")
	if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
		t.Fatal("expected error for invalid upgrade channel")
	}
}

func TestResolverRejectsTooSmallInterval(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_INTERVAL", "1m")
	if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
		t.Fatal("expected error for update interval below minimum")
	}
}

func TestResolverNotifyFromConfigFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"notify": map[string]any{
			"desktop":  true,
			"audio":    "bell",
			"on-input": false,
		},
	})
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	cfg := resolverNotify(r)
	if !cfg.Desktop || cfg.Audio != notify.AudioBell {
		t.Errorf("channels = desktop:%v audio:%q, want desktop true audio bell", cfg.Desktop, cfg.Audio)
	}
	if cfg.OnInput || !cfg.OnDone || !cfg.OnError {
		t.Errorf("triggers = %+v, want on-input off, on-done/on-error defaulted true", cfg)
	}
}

func TestResolverNotifyRejectsInvalidAudio(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"notify": map[string]any{"audio": "loud"},
	})
	if _, err := NewResolver(nil, configpaths.Get(), ""); err == nil {
		t.Fatal("expected error for invalid notify.audio mode")
	}
}

// Each per-trigger notify key is read independently and defaults to true.
func TestResolverNotifyTriggersFromConfigFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"notify": map[string]any{
			"desktop":  true,
			"on-input": false,
			"on-done":  false,
			"on-error": false,
		},
	})
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	cfg := resolverNotify(r)
	if !cfg.Desktop {
		t.Errorf("desktop = false, want true")
	}
	if cfg.OnInput || cfg.OnDone || cfg.OnError {
		t.Errorf("triggers = input:%v done:%v error:%v, want all false", cfg.OnInput, cfg.OnDone, cfg.OnError)
	}
}

// The notify.* dotted keys are bound explicitly (they are not derived from the
// Config struct because Notify is decoded outside viper), so their documented
// AGENTS_SANDBOX_NOTIFY_* env vars resolve without AutomaticEnv.
func TestResolverNotifyDottedEnvVars(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NOTIFY_DESKTOP", "true")
	t.Setenv("AGENTS_SANDBOX_NOTIFY_AUDIO", "bell")
	t.Setenv("AGENTS_SANDBOX_NOTIFY_ON_INPUT", "false")
	t.Setenv("AGENTS_SANDBOX_NOTIFY_ON_DONE", "false")
	t.Setenv("AGENTS_SANDBOX_NOTIFY_ON_ERROR", "false")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	cfg := resolverNotify(r)
	if !cfg.Desktop {
		t.Error("Desktop = false, want true from AGENTS_SANDBOX_NOTIFY_DESKTOP")
	}
	if cfg.Audio != notify.AudioBell {
		t.Errorf("Audio = %q, want bell from AGENTS_SANDBOX_NOTIFY_AUDIO", cfg.Audio)
	}
	if cfg.OnInput || cfg.OnDone || cfg.OnError {
		t.Errorf("triggers = input:%v done:%v error:%v, want all false from env", cfg.OnInput, cfg.OnDone, cfg.OnError)
	}
}

// The deprecated OPENCODE_SANDBOX_ prefix is bound for the notify dotted keys
// too, and surfaced as a legacy variable in effect.
func TestResolverNotifyLegacyEnvVars(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_NOTIFY_AUDIO", "bell")

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := resolverNotify(r).Audio; got != notify.AudioBell {
		t.Errorf("Audio = %q, want bell from OPENCODE_SANDBOX_NOTIFY_AUDIO", got)
	}
	if got := r.LegacyEnvVars(); !slices.Contains(got, "OPENCODE_SANDBOX_NOTIFY_AUDIO") {
		t.Errorf("LegacyEnvVars = %v, want it to contain OPENCODE_SANDBOX_NOTIFY_AUDIO", got)
	}
}

// A valid Go-style duration (parsed directly by GetDuration) passes
// validatePruneAges via the d > 0 fast path.
func TestResolverPruneAgeValidGoDuration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"auto-prune-age": "1h",
	})
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.AutoPruneAge() != time.Hour {
		t.Errorf("AutoPruneAge = %v, want 1h", r.AutoPruneAge())
	}
}

func TestResolverHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
		"home": map[string]any{".gitconfig": ""},
	})
	testutil.WriteYAML(t, cp.ProjectConfigDir(), "config.yaml", map[string]any{
		"home": map[string]any{".config/tool/cfg.toml": "./tool/cfg.toml"},
	})

	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	layers, has := r.Home()
	if !has {
		t.Fatal("expected has=true")
	}
	if len(layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(layers))
	}
	if _, ok := layers[0].Manifest[".gitconfig"]; !ok {
		t.Errorf("expected user .gitconfig in layer 0, got %v", layers[0].Manifest)
	}
	if _, ok := layers[1].Manifest[".config/tool/cfg.toml"]; !ok {
		t.Errorf("expected project cfg.toml in layer 1, got %v", layers[1].Manifest)
	}
}

func TestResolverHomeNoConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	r, err := NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	layers, has := r.Home()
	if has {
		t.Error("expected has=false when no home key configured")
	}
	for i, l := range layers {
		if len(l.Manifest) != 0 {
			t.Errorf("expected empty manifest in layer %d, got %v", i, l.Manifest)
		}
	}
}

func TestResolverHomeRejectsInvalidHomeValue(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{
		"home": 5,
	})
	_, err := NewResolver(nil, configpaths.Get(), "")
	if err == nil {
		t.Fatal("expected error for invalid home value")
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

// wantNetworkProfiles asserts the effective network policy's profile set.
func wantNetworkProfiles(t *testing.T, policy network.Policy, want ...network.Profile) {
	t.Helper()
	if got := policy.Effective().Profiles; !slices.Equal(got, want) {
		t.Fatalf("network profiles = %v, want %v", got, want)
	}
}

func portsTestCmd(t *testing.T, publishValues []string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "root"}
	root.Flags().StringSliceP("port", "p", nil, "")
	args := []string{}
	for _, v := range publishValues {
		args = append(args, "--port", v)
	}
	if err := root.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	return root
}

// documentedPortsExample extracts the fenced yaml block that follows the
// "## Published ports" heading in the networking docs.
func documentedPortsExample(docs string) (string, bool) {
	_, section, found := strings.Cut(docs, "## Published ports")
	if !found {
		return "", false
	}
	_, block, found := strings.Cut(section, "```yaml\n")
	if !found {
		return "", false
	}
	snippet, _, found := strings.Cut(block, "```")
	if !found {
		return "", false
	}
	return snippet, true
}

func writeUserConfig(t *testing.T, values map[string]any) {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", values)
}
