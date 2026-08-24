package viperconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/yamlfmt"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/titanous/json5"
)

// Config holds launcher-level defaults that can be set in
// ~/.config/opencode-sandbox/config.* and .opencode-sandbox/config.*.
type Config struct {
	AutoPruneAge   time.Duration `mapstructure:"auto-prune-age"`
	ManualPruneAge time.Duration `mapstructure:"manual-prune-age"`
	Memory         string        `mapstructure:"memory"`
	TmpSize        string        `mapstructure:"tmp-size"`
	DiskSize       string        `mapstructure:"disk-size"`
	WorkspaceQuota string        `mapstructure:"workspace-quota"`
	Yes            bool          `mapstructure:"yes"`
	Verbose        bool          `mapstructure:"verbose"`
	Error          bool          `mapstructure:"error"`
	CPUs           uint8         `mapstructure:"cpus"`

	AutoStopOnActiveSessions  bool          `mapstructure:"auto-stop-on-active-sessions"`
	AutoStopTimeout           time.Duration `mapstructure:"auto-stop-timeout"`
	AutoStopMaxSessionRetries int           `mapstructure:"auto-stop-max-session-retries"`

	// Network holds the egress policy. Only Profile is settable via env/flag.
	Network network.Policy `mapstructure:"network"`
}

// Resolver resolves launcher config with precedence flag > env > config > default.
type Resolver struct {
	cfg Config
}

// NewResolver builds a Resolver, loading config files, configuring the
// OPENCODE_SANDBOX_ env prefix, binding config-backed flags on cmd, and
// validating. cmd may be nil to skip flag binding.
func NewResolver(cmd *cobra.Command) (*Resolver, error) {
	v := viper.New()

	if err := mergeDir(v, configpaths.Get().UserConfigDir()); err != nil {
		return nil, err
	}
	if err := mergeDir(v, configpaths.Get().ProjectConfigDir()); err != nil {
		return nil, err
	}

	v.SetEnvPrefix("OPENCODE_SANDBOX")
	// Map both hyphens and nested-key dots to underscores so that keys like
	// "network.profile" resolve from OPENCODE_SANDBOX_NETWORK_PROFILE.
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	for _, key := range configEnvKeys {
		if err := v.BindEnv(key); err != nil {
			return nil, err
		}
	}

	if cmd != nil {
		if err := bindConfigFlags(v, cmd); err != nil {
			return nil, err
		}
	}

	if err := validate(v); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(
		mapstructure.ComposeDecodeHookFunc(
			durationDecodeHook(),
			mapstructure.StringToTimeDurationHookFunc(),
		),
	)); err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	return &Resolver{cfg: cfg}, nil
}

// NewResolverWithConfig builds a Resolver from an explicit Config. It is
// used by callers (notably cmd tests) that need a resolver with known values
// without touching config files or env.
func NewResolverWithConfig(cfg Config) *Resolver {
	return &Resolver{cfg: cfg}
}

const (
	extJSON5                     = ".json5"
	extJSONC                     = ".jsonc"
	keyAutoPruneAge              = "auto-prune-age"
	keyManualPruneAge            = "manual-prune-age"
	keyAutoStopOnActiveSessions  = "auto-stop-on-active-sessions"
	keyAutoStopTimeout           = "auto-stop-timeout"
	keyAutoStopMaxSessionRetries = "auto-stop-max-session-retries"
	keyNetworkProfile            = "network.profile"
)

//nolint:gochecknoglobals // package-level constant slice
var supportedExts = []string{".yaml", ".yml", ".json", extJSONC, extJSON5}

// configFlagKeys are the config-backed keys that are also exposed as CLI flags.
// Their env vars use the OPENCODE_SANDBOX_ prefix.
//
//nolint:gochecknoglobals,goconst // package-level constant slice
var configFlagKeys = []string{
	"cpus", "memory", "tmp-size", "disk-size", "workspace-quota",
	"yes", "verbose", "error",
}

// configEnvKeys are all launcher config keys bound to OPENCODE_SANDBOX_ env vars.
//
//nolint:gochecknoglobals // package-level constant slice
var configEnvKeys = []string{
	"cpus", "memory", "tmp-size", "disk-size", "workspace-quota",
	"yes", "verbose", "error",
	keyAutoPruneAge, keyManualPruneAge,
	keyAutoStopOnActiveSessions, keyAutoStopTimeout, keyAutoStopMaxSessionRetries,
	keyNetworkProfile,
}

// bindConfigFlags binds each config-backed flag found on cmd (local or
// inherited) to viper and mirrors its declared default so that an
// unspecified flag with a default does not override env/config.
func bindConfigFlags(v *viper.Viper, cmd *cobra.Command) error {
	for _, key := range configFlagKeys {
		flag := findFlag(cmd, key)
		if flag == nil {
			continue
		}
		if err := v.BindPFlag(key, flag); err != nil {
			return fmt.Errorf("bind flag %q: %w", key, err)
		}
		v.SetDefault(key, flagTypedDefault(key, flag))
	}
	return nil
}

func findFlag(cmd *cobra.Command, name string) *pflag.Flag {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}
	return cmd.InheritedFlags().Lookup(name)
}

func flagTypedDefault(key string, flag *pflag.Flag) any {
	switch key {
	case "cpus":
		n, _ := strconv.ParseUint(flag.DefValue, 10, 8)
		return uint8(n)
	case "yes", "verbose", "error":
		return flag.DefValue == "true"
	default:
		return flag.DefValue
	}
}

// ParseHumanDuration parses duration strings like "7d", "2w", "6h", "30m"
// into time.Duration. Go's time.ParseDuration supports ns/us/ms/s/m/h
// but not "d" (days) or "w" (weeks).
func ParseHumanDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasSuffix(s, "w"):
		num, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, false
		}
		return time.Duration(num) * 7 * 24 * time.Hour, true
	case strings.HasSuffix(s, "d"):
		num, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil {
			return 0, false
		}
		return time.Duration(num) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, true
	}
	return 0, false
}

func durationDecodeHook() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}
		if t.Kind() != reflect.Interface && t != reflect.TypeFor[time.Duration]() {
			return data, nil
		}
		str, ok := data.(string)
		if !ok {
			return data, nil
		}
		if d, ok := ParseHumanDuration(str); ok {
			return d, nil
		}
		if d, err := time.ParseDuration(str); err == nil {
			return d, nil
		}
		return data, nil
	}
}

func mergeDir(v *viper.Viper, dir string) error {
	path, ext, ok := findConfigFile(dir)
	if !ok {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read launcher config %s: %w", path, err)
	}
	ct := configType(ext)
	if ext == extJSON5 || ext == extJSONC {
		var m map[string]any
		if unmarshalErr := json5.Unmarshal(data, &m); unmarshalErr != nil {
			return fmt.Errorf("parse launcher config %s: %w", path, unmarshalErr)
		}
		data, err = json.Marshal(m)
		if err != nil {
			return fmt.Errorf("normalize launcher config %s: %w", path, err)
		}
		ct = "json"
	}
	v.SetConfigType(ct)
	if err := v.MergeConfig(bytes.NewReader(data)); err != nil {
		if ct == "yaml" {
			return yamlfmt.WrapErr(path, err)
		}
		return fmt.Errorf("load launcher config %s: %w", path, err)
	}
	return nil
}

func findConfigFile(dir string) (string, string, bool) {
	if dir == "" {
		return "", "", false
	}
	for _, ext := range supportedExts {
		path := filepath.Join(dir, "config"+ext)
		if _, err := os.Stat(path); err == nil {
			return path, ext, true
		}
	}
	return "", "", false
}

func configType(ext string) string {
	switch strings.ToLower(ext) {
	case ".yaml", ".yml":
		return "yaml"
	case ".json", ".jsonc", ".json5":
		return "json"
	}
	return ""
}

func validate(v *viper.Viper) error {
	// prune-age validation does not gate on cpus being set, so run it
	// before the cpus early-exit below.
	if err := validatePruneAges(v); err != nil {
		return err
	}
	if err := validateAutoStop(v); err != nil {
		return err
	}
	if err := validateAutoStopTimeout(v); err != nil {
		return err
	}
	if err := validateNetworkProfile(v); err != nil {
		return err
	}
	if !v.IsSet("cpus") {
		return nil
	}
	cpus := v.GetInt("cpus")
	if cpus < 0 || cpus > 255 {
		return fmt.Errorf("launcher config cpus must be between 0 and 255, got %d", cpus)
	}
	return nil
}

func validateNetworkProfile(v *viper.Viper) error {
	if !v.IsSet(keyNetworkProfile) {
		return nil
	}
	profileStr := v.GetString(keyNetworkProfile)
	if _, err := network.ParseProfile(profileStr); err != nil {
		return err
	}
	return nil
}

func validatePruneAges(v *viper.Viper) error {
	for _, key := range []string{keyAutoPruneAge, keyManualPruneAge} {
		if !v.IsSet(key) {
			continue
		}
		d := v.GetDuration(key)
		if d > 0 {
			continue
		}
		// viper's GetDuration returned 0 — check if the raw value is a
		// "7d"-style string that the decode hook failed to convert.
		if s, ok := v.Get(key).(string); ok {
			if parsed, ok := ParseHumanDuration(s); ok {
				d = parsed
			}
		}
		if d <= 0 {
			return fmt.Errorf("launcher config %s must be > 0, got %v", key, d)
		}
	}
	return nil
}

func validateAutoStop(v *viper.Viper) error {
	if !v.IsSet(keyAutoStopMaxSessionRetries) {
		return nil
	}
	retries := v.GetInt(keyAutoStopMaxSessionRetries)
	if retries >= 0 {
		return nil
	}
	return fmt.Errorf("launcher config %s must be >= 0, got %d", keyAutoStopMaxSessionRetries, retries)
}

func validateAutoStopTimeout(v *viper.Viper) error {
	if !v.IsSet(keyAutoStopTimeout) {
		return nil
	}
	d := v.GetDuration(keyAutoStopTimeout)
	if d > 0 {
		return nil
	}
	if s, ok := v.Get(keyAutoStopTimeout).(string); ok {
		if parsed, ok := ParseHumanDuration(s); ok {
			d = parsed
		}
	}
	if d <= 0 {
		return fmt.Errorf("launcher config %s must be > 0, got %v", keyAutoStopTimeout, d)
	}
	return nil
}

func (c Config) IdleTimeout() time.Duration {
	if c.AutoStopTimeout > 0 {
		return c.AutoStopTimeout
	}
	return 10 * time.Second
}

func (r *Resolver) CPUs() uint8                    { return r.cfg.CPUs }
func (r *Resolver) Memory() string                 { return r.cfg.Memory }
func (r *Resolver) TmpSize() string                { return r.cfg.TmpSize }
func (r *Resolver) DiskSize() string               { return r.cfg.DiskSize }
func (r *Resolver) WorkspaceQuota() string         { return r.cfg.WorkspaceQuota }
func (r *Resolver) Yes() bool                      { return r.cfg.Yes }
func (r *Resolver) Verbose() bool                  { return r.cfg.Verbose }
func (r *Resolver) Error() bool                    { return r.cfg.Error }
func (r *Resolver) AutoPruneAge() time.Duration    { return r.cfg.AutoPruneAge }
func (r *Resolver) ManualPruneAge() time.Duration  { return r.cfg.ManualPruneAge }
func (r *Resolver) AutoStopOnActiveSessions() bool { return r.cfg.AutoStopOnActiveSessions }
func (r *Resolver) AutoStopTimeout() time.Duration { return r.cfg.AutoStopTimeout }
func (r *Resolver) AutoStopMaxSessionRetries() int { return r.cfg.AutoStopMaxSessionRetries }
func (r *Resolver) IdleTimeout() time.Duration     { return r.cfg.IdleTimeout() }

// Network returns the configured network policy, or an empty policy when no
// network config is set. Callers fall back to the default public profile when
// the policy is Empty.
func (r *Resolver) Network() network.Policy {
	return r.cfg.Network
}
