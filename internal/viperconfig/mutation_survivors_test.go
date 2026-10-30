package viperconfig

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/upgrade"
)

// TestDecodeNotifyReadsEveryKeyIndependently sets one notify key at a time so
// each operand of the "is any notify key set" guard is load-bearing: dropping
// an operand, inverting it, or removing the early return changes the result.
func TestDecodeNotifyReadsEveryKeyIndependently(t *testing.T) {
	off := notify.AudioOff
	cases := []struct {
		name string
		set  func(v *viper.Viper)
		want NotifyConfig
	}{
		{
			name: "no key set",
			set:  func(*viper.Viper) {},
			want: NotifyConfig{Audio: off},
		},
		{
			name: "desktop only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyDesktop, true) },
			want: NotifyConfig{Desktop: true, Audio: off, OnInput: true, OnDone: true, OnError: true},
		},
		{
			name: "audio only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyAudio, string(notify.AudioBell)) },
			want: NotifyConfig{Audio: notify.AudioBell, OnInput: true, OnDone: true, OnError: true},
		},
		{
			name: "on-input only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyOnInput, false) },
			want: NotifyConfig{Audio: off, OnInput: false, OnDone: true, OnError: true},
		},
		{
			name: "on-done only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyOnDone, false) },
			want: NotifyConfig{Audio: off, OnInput: true, OnDone: false, OnError: true},
		},
		{
			name: "on-error only",
			set:  func(v *viper.Viper) { v.Set(keyNotifyOnError, false) },
			want: NotifyConfig{Audio: off, OnInput: true, OnDone: true, OnError: false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			tc.set(v)
			if got := decodeNotify(v); got != tc.want {
				t.Errorf("decodeNotify() = %+v, want %+v", got, tc.want)
			}
		})
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

// With no docker source at all, neither docker nor a legacy dind alias is
// reported.
func TestResolveDockerAliasWithoutAnySource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	r, err := NewResolver(nil, "")
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

	r, err := NewResolver(nil, "")
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

	r, err := NewResolver(nil, "")
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

	r, err := NewResolver(root, "some-slug")
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

	r, err := NewResolver(root, "some-slug")
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

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.cfg.CPUs != 2 {
		t.Errorf("CPUs = %d, want 2 from the commented jsonc file", r.cfg.CPUs)
	}
}

func TestValidateCPUsRange(t *testing.T) {
	for _, tc := range []struct {
		cpus    int
		wantErr bool
	}{
		{-1, true},
		{0, false},
		{255, false},
		{256, true},
	} {
		v := viper.New()
		v.Set(keyCPUs, tc.cpus)
		err := validate(v)
		if (err != nil) != tc.wantErr {
			t.Errorf("validate cpus=%d err = %v, wantErr %v", tc.cpus, err, tc.wantErr)
		}
	}
}

func TestValidateDNSServersWrapsCause(t *testing.T) {
	entryType := viper.New()
	entryType.Set(keyNetworkDNSServers, 42)
	if err := validateNetworkDNSServers(entryType); err == nil || errors.Unwrap(err) == nil {
		t.Errorf("entries error = %v, want it to wrap its cause", err)
	}

	normalize := viper.New()
	normalize.Set(keyNetworkDNSServers, "1.1.1.1,not a dns server")
	if err := validateNetworkDNSServers(normalize); err == nil || errors.Unwrap(err) == nil {
		t.Errorf("normalize error = %v, want it to wrap its cause", err)
	}
}

// A "7d" interval is only understood by ParseHumanDuration, so the upgrade
// validator must fall back to it.
func TestResolverUpgradeIntervalHumanDuration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_UPGRADE_INTERVAL", "7d")

	r, err := NewResolver(nil, "")
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

	r, err := NewResolver(nil, "")
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

	if _, err := NewResolver(nil, ""); err == nil {
		t.Fatal("expected error for the invalid second prune age")
	}
}

func TestLegacyEnvVarsInUseScansEveryKey(t *testing.T) {
	for _, key := range configEnvKeys {
		t.Setenv(envVarName(LegacyEnvPrefix, key), "")
		t.Setenv(envVarName(EnvPrefix, key), "")
	}
	t.Setenv("OPENCODE_SANDBOX_AGENT", "pi")

	if got := legacyEnvVarsInUse(); !slices.Contains(got, "OPENCODE_SANDBOX_AGENT") {
		t.Fatalf("legacyEnvVarsInUse() = %v, want OPENCODE_SANDBOX_AGENT", got)
	}
}

func TestLegacyEnvVarsInUseSkipsShadowedAndContinues(t *testing.T) {
	for _, key := range configEnvKeys {
		t.Setenv(envVarName(LegacyEnvPrefix, key), "")
		t.Setenv(envVarName(EnvPrefix, key), "")
	}
	t.Setenv("OPENCODE_SANDBOX_CPUS", "6")
	t.Setenv("AGENTS_SANDBOX_CPUS", "7")
	t.Setenv("OPENCODE_SANDBOX_AGENT", "pi")

	got := legacyEnvVarsInUse()
	if slices.Contains(got, "OPENCODE_SANDBOX_CPUS") {
		t.Error("a shadowed legacy variable should be skipped")
	}
	if !slices.Contains(got, "OPENCODE_SANDBOX_AGENT") {
		t.Fatalf("legacyEnvVarsInUse() = %v, want OPENCODE_SANDBOX_AGENT", got)
	}
}

// The cpus default is parsed with base 10 and 8 bits; both literals are
// observable at values whose base-9/base-11 or 7/9-bit interpretation differs.
func TestFlagTypedDefaultCPUsLimits(t *testing.T) {
	for _, tc := range []struct {
		def  string
		want uint8
	}{
		{"200", 200},
		{"256", 255},
	} {
		flag := &pflag.Flag{Name: "cpus", DefValue: tc.def}
		if got := flagTypedDefault("cpus", flag); got != tc.want {
			t.Errorf("flagTypedDefault(cpus, %q) = %#v, want %v", tc.def, got, tc.want)
		}
	}
}
