package viperconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/upgrade"
	"github.com/inoio/agents-sandbox/internal/yamlfmt"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/titanous/json5"
)

// Config holds launcher-level defaults that can be set in
// ~/.config/agents-sandbox/config.* and .agents-sandbox/config.*.
type Config struct {
	AutoPruneAge   time.Duration `mapstructure:"auto-prune-age"`
	ManualPruneAge time.Duration `mapstructure:"manual-prune-age"`
	Memory         string        `mapstructure:"memory"`
	TmpSize        string        `mapstructure:"tmp-size"`
	DiskSize       string        `mapstructure:"disk-size"`
	WorkspaceQuota string        `mapstructure:"workspace-quota"`
	Yes            bool          `mapstructure:"yes"`
	LogLevel       string        `mapstructure:"log-level"`
	Quiet          bool          `mapstructure:"quiet"`
	Agent          string        `mapstructure:"agent"`
	// Dind appends the tool's Docker-in-Docker block to the runner image.
	Dind bool  `mapstructure:"dind"`
	CPUs uint8 `mapstructure:"cpus"`
	// ProvisionHostConfig controls whether the agent's host config files are
	// copied into the VM (drop-in provisioning). Default true.
	ProvisionHostConfig bool `mapstructure:"provision-host-config"`

	AutoStopOnActiveSessions  bool          `mapstructure:"auto-stop-on-active-sessions"`
	AutoStopTimeout           time.Duration `mapstructure:"auto-stop-timeout"`
	AutoStopMaxSessionRetries int           `mapstructure:"auto-stop-max-session-retries"`

	// Network holds the egress policy. Only Profile is settable via env/flag.
	Network network.Policy `mapstructure:"network"`
	// Mounts is excluded from viper.Unmarshal (mapstructure:"-") and decoded
	// from v.Get("mounts") instead: viper flattens dotted guest paths such as
	// /home/dev/.m2 into nested keys, which would corrupt the map keys.
	Mounts mounts.Mounts `mapstructure:"-"`
	// Home holds the per-layer home manifests from the config files' home:
	// key. It is decoded separately (not via viper.Unmarshal) so each layer
	// keeps its own directory for relative-source resolution.
	Home homeconfig.Layers `mapstructure:"-"`
	// hasHome reports whether any config file declared a home key.
	hasHome bool

	// Upgrade controls checking for and installing newer agents-sandbox
	// releases. Only Mode and Interval are settable via env.
	Upgrade UpgradeConfig `mapstructure:"upgrade"`
	// Notify holds the resolved notify config. It is decoded separately (not
	// via viper.Unmarshal) from dotted keys so the OPENCODE_SANDBOX_NOTIFY env
	// override — the bare "notify" key — cannot collide with the nested
	// notify.desktop/audio/... keys.
	Notify NotifyConfig `mapstructure:"-"`
}

// NotifyConfig is the resolved notify setting for a session.
type NotifyConfig = notify.Config

// Exported flag names that BuildRunOptions reads directly from the command.
const (
	FlagWorktree   = "worktree"
	FlagRebuild    = "rebuild"
	FlagDryRun     = "dry-run"
	FlagDryRunVM   = "dry-run-vm"
	FlagServeOnly  = "serve-only"
	FlagRoot       = "root"
	FlagAgent      = "agent"
	FlagNetwork    = "network"
	FlagDNSServers = "dns"
	FlagNotify     = "notify"
)

// notifyEnvVar is the environment variable override for --notify.
const notifyEnvVar = "OPENCODE_SANDBOX_NOTIFY"

// defaultAgentName is the fallback agent used when --agent is not provided.
const defaultAgentName = "opencode"

// UpgradeConfig holds self-upgrade settings.
type UpgradeConfig struct {
	Mode     string        `mapstructure:"mode"`
	Interval time.Duration `mapstructure:"interval"`
}

// Resolver resolves launcher config with precedence flag > env > config > default.
type Resolver struct {
	cfg Config
}

// NewResolver builds a Resolver, loading config files, configuring the
// OPENCODE_SANDBOX_ env prefix, binding config-backed flags on cmd, and
// validating. Config precedence (lowest to highest): generic user dir,
// per-slug user dir (when slug is non-empty), project dir, env, flags.
// cmd may be nil to skip flag binding.
func NewResolver(cmd *cobra.Command, slug string) (*Resolver, error) {
	v := viper.New()

	if err := mergeDir(v, configpaths.Get().UserConfigDir()); err != nil {
		return nil, err
	}
	if slug != "" {
		if err := mergeDir(v, filepath.Join(configpaths.Get().UserConfigDir(), slug)); err != nil {
			return nil, err
		}
	}
	if err := mergeDir(v, configpaths.Get().ProjectConfigDir()); err != nil {
		return nil, err
	}

	v.SetEnvPrefix("OPENCODE_SANDBOX")
	// Env-var keys are all top-level and dash-separated; only the nested
	// "network.profile" key uses a dot, which maps to "_" here. This is a
	// repo-wide replacement, so a future dotted top-level key (e.g.
	// "auto.stop") would collide with a dashed one ("auto-stop") and must be
	// avoided.
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

	// Non-flag defaults: host config provisioning is on unless opted out.
	v.SetDefault(keyProvisionHostConfig, true)

	if err := validate(v); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(
		mapstructure.ComposeDecodeHookFunc(
			durationDecodeHook(),
			mapstructure.StringToTimeDurationHookFunc(),
			// Split comma-separated strings (env vars, CLI StringSlice flags)
			// into []string fields such as network.egress-allow and
			// network.dns-servers. It also leniently accepts a scalar string
			// for those list fields in config files.
			mapstructure.StringToSliceHookFunc(","),
		),
	)); err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	decodedMounts, err := mounts.DecodeMounts(v.Get("mounts"))
	if err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	cfg.Mounts = decodedMounts
	cfg.Notify = decodeNotify(v)

	homeLayers, hasHome, err := homeconfig.LoadLayers([]string{
		configpaths.Get().UserConfigDir(),
		configpaths.Get().ProjectConfigDir(),
	})
	if err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	cfg.Home = homeLayers
	cfg.hasHome = hasHome
	return &Resolver{cfg: cfg}, nil
}

// NewResolverWithConfig builds a Resolver from an explicit Config. It is
// used by callers (notably cmd tests) that need a resolver with known values
// without touching config files or env.
func NewResolverWithConfig(cfg Config) *Resolver {
	return &Resolver{cfg: cfg}
}

const (
	strTrue                      = "true"
	extJSON5                     = ".json5"
	extJSONC                     = ".jsonc"
	ctJSON                       = "json"
	ctYAML                       = "yaml"
	keyAutoPruneAge              = "auto-prune-age"
	keyManualPruneAge            = "manual-prune-age"
	keyAutoStopOnActiveSessions  = "auto-stop-on-active-sessions"
	keyAutoStopTimeout           = "auto-stop-timeout"
	keyAutoStopMaxSessionRetries = "auto-stop-max-session-retries"
	keyNetworkProfile            = "network.profile"
	keyNetworkDNSServers         = "network.dns-servers"
	keyAgent                     = "agent"
	keyDind                      = "dind"
	keyUpgradeMode               = "upgrade.mode"
	keyUpgradeInterval           = "upgrade.interval"
	keyProvisionHostConfig       = "provision-host-config"
	keyNotifyDesktop             = "notify.desktop"
	keyNotifyAudio               = "notify.audio"
	keyNotifyOnInput             = "notify.on-input"
	keyNotifyOnDone              = "notify.on-done"
	keyNotifyOnError             = "notify.on-error"
)

//nolint:gochecknoglobals // package-level constant slice
var supportedExts = []string{".yaml", ".yml", ".json", extJSONC, extJSON5}

// configFlagKeys are the config-backed keys that are also exposed as CLI flags.
// Their env vars use the OPENCODE_SANDBOX_ prefix.
//
//nolint:gochecknoglobals,goconst // package-level constant slice
var configFlagKeys = []string{
	"cpus", "memory", "tmp-size", "disk-size", "workspace-quota",
	"yes", "quiet", "log-level", "agent", "dind",
}

// configEnvKeys are all launcher config keys bound to OPENCODE_SANDBOX_ env vars.
//
//nolint:gochecknoglobals // package-level constant slice
var configEnvKeys = []string{
	"cpus", "memory", "tmp-size", "disk-size", "workspace-quota",
	"yes", "quiet", "log-level",
	keyAutoPruneAge, keyManualPruneAge,
	keyAutoStopOnActiveSessions, keyAutoStopTimeout, keyAutoStopMaxSessionRetries,
	keyNetworkProfile, keyNetworkDNSServers,
	keyAgent,
	keyDind,
	keyUpgradeMode, keyUpgradeInterval,
	keyProvisionHostConfig,
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
	case "yes":
		return flag.DefValue == strTrue
	case "quiet":
		return flag.DefValue == strTrue
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
		ct = ctJSON
	}
	v.SetConfigType(ct)
	if err := v.MergeConfig(bytes.NewReader(data)); err != nil {
		if ct == ctYAML {
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
		return ctYAML
	case ".json", ".jsonc", ".json5":
		return ctJSON
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
	if err := validateNetworkDNSServers(v); err != nil {
		return err
	}
	if err := validateUpgrade(v); err != nil {
		return err
	}
	if err := validateNotify(v); err != nil {
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

// validateNetworkDNSServers rejects malformed network.dns-servers values at
// config-load time. The raw value may be a comma-separated string (env/flag) or
// a YAML/JSON list (config file).
func validateNetworkDNSServers(v *viper.Viper) error {
	if !v.IsSet(keyNetworkDNSServers) {
		return nil
	}
	entries, err := dnsServerEntries(v.Get(keyNetworkDNSServers))
	if err != nil {
		return fmt.Errorf("launcher config %s: %w", keyNetworkDNSServers, err)
	}
	if _, err := network.NormalizeDNSServers(entries); err != nil {
		return fmt.Errorf("launcher config %s: %w", keyNetworkDNSServers, err)
	}
	return nil
}

// dnsServerEntries converts a raw network.dns-servers value (comma-separated
// string or list) into the individual entries.
func dnsServerEntries(raw any) ([]string, error) {
	switch val := raw.(type) {
	case string:
		if val == "" {
			return nil, errors.New("must not be empty")
		}
		return strings.Split(val, ","), nil
	case []string:
		return val, nil
	case []any:
		entries := make([]string, 0, len(val))
		for _, e := range val {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("entries must be strings, got %T", e)
			}
			entries = append(entries, s)
		}
		return entries, nil
	default:
		return nil, fmt.Errorf("must be a list of DNS servers, got %T", raw)
	}
}

// validateUpgrade validates the upgrade.mode and upgrade.interval config keys.
func validateUpgrade(v *viper.Viper) error {
	if v.IsSet(keyUpgradeMode) {
		if _, err := upgrade.ParseMode(v.GetString(keyUpgradeMode)); err != nil {
			return err
		}
	}
	if !v.IsSet(keyUpgradeInterval) {
		return nil
	}
	d := v.GetDuration(keyUpgradeInterval)
	if s, ok := v.Get(keyUpgradeInterval).(string); ok {
		if parsed, ok := ParseHumanDuration(s); ok {
			d = parsed
		}
	}
	if d < upgrade.MinInterval {
		return fmt.Errorf("launcher config %s must be >= %s, got %v", keyUpgradeInterval, upgrade.MinInterval, d)
	}
	return nil
}

func validateNotify(v *viper.Viper) error {
	if !v.IsSet(keyNotifyAudio) {
		return nil
	}
	if _, err := notify.ParseAudioMode(v.GetString(keyNotifyAudio)); err != nil {
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

func (r *Resolver) Yes() bool                     { return r.cfg.Yes }
func (r *Resolver) Quiet() bool                   { return r.cfg.Quiet }
func (r *Resolver) LogLevel() string              { return r.cfg.LogLevel }
func (r *Resolver) Agent() string                 { return r.cfg.Agent }
func (r *Resolver) Dind() bool                    { return r.cfg.Dind }
func (r *Resolver) ProvisionHostConfig() bool     { return r.cfg.ProvisionHostConfig }
func (r *Resolver) AutoPruneAge() time.Duration   { return r.cfg.AutoPruneAge }
func (r *Resolver) ManualPruneAge() time.Duration { return r.cfg.ManualPruneAge }

// UpgradeMode returns the configured upgrade mode, defaulting to prompt. An
// unparsable value (possible via NewResolverWithConfig, which skips
// validation) also falls back to prompt.
func (r *Resolver) UpgradeMode() upgrade.Mode {
	m, err := upgrade.ParseMode(r.cfg.Upgrade.Mode)
	if err != nil {
		return upgrade.ModePrompt
	}
	return m
}

// UpgradeInterval returns the configured upgrade-check interval, defaulting to
// a day and never returning less than the rate-limit guard.
func (r *Resolver) UpgradeInterval() time.Duration {
	switch {
	case r.cfg.Upgrade.Interval <= 0:
		return upgrade.DefaultInterval
	case r.cfg.Upgrade.Interval < upgrade.MinInterval:
		return upgrade.MinInterval
	default:
		return r.cfg.Upgrade.Interval
	}
}

// Home returns the per-layer home manifests read from the config files' home:
// key, and whether any config file declared a home key.
func (r *Resolver) Home() (homeconfig.Layers, bool) {
	return r.cfg.Home, r.cfg.hasHome
}

// BuildRunOptions resolves the shared run/shell options from the command's
// flags and this resolver's config, with precedence flag > env > config >
// default. A nil resolver resolves flag-only options with zero-value policy
// fields (no config, env, or defaults applied).
func (r *Resolver) BuildRunOptions(cmd *cobra.Command, ui termio.UI) (options.RunOptions, error) {
	opts, err := r.resolveFlags(cmd, ui)
	if err != nil {
		return options.RunOptions{}, err
	}

	if err = r.resolveAgentAndValidate(cmd, &opts); err != nil {
		return options.RunOptions{}, err
	}

	if err = r.resolveNotify(cmd, ui, &opts); err != nil {
		return options.RunOptions{}, err
	}

	if r != nil {
		opts.CPUs = r.cfg.CPUs
		opts.Memory = r.cfg.Memory
		opts.TmpSize = r.cfg.TmpSize
		opts.DiskSize = r.cfg.DiskSize
		opts.WorkspaceQuota = r.cfg.WorkspaceQuota
		opts.ReapPolicy = options.NewReapPolicy(r.cfg.AutoStopOnActiveSessions, r.cfg.AutoStopMaxSessionRetries)
		opts.IdleTimeout = r.cfg.IdleTimeout()
		opts.Dind = r.cfg.Dind
		opts.Mounts, err = mounts.ResolveBindMounts(r.cfg.Mounts)
		if err != nil {
			return options.RunOptions{}, err
		}
		provisionHostConfig := r.cfg.ProvisionHostConfig
		opts.ProvisionHostConfig = &provisionHostConfig
	}

	if err := r.resolveNetwork(cmd, &opts); err != nil {
		return options.RunOptions{}, err
	}

	if err := r.resolveSizes(&opts); err != nil {
		return options.RunOptions{}, err
	}

	return opts, nil
}

// resolveFlags reads the direct-run flags off the command and returns the
// starting RunOptions, resolving the worktree spec and the dry-run auto-enable.
func (r *Resolver) resolveFlags(cmd *cobra.Command, ui termio.UI) (options.RunOptions, error) {
	opts := options.RunOptions{}
	rawWorktree, _ := cmd.Flags().GetString(FlagWorktree)
	worktree, err := options.ResolveWorktreeSpec(rawWorktree)
	if err != nil {
		return options.RunOptions{}, err
	}
	opts.Worktree = worktree
	opts.Rebuild, _ = cmd.Flags().GetBool(FlagRebuild)
	opts.DryRun, _ = cmd.Flags().GetBool(FlagDryRun)
	opts.DryRunVM, _ = cmd.Flags().GetBool(FlagDryRunVM)
	if opts.DryRun {
		opts.DryRunVM = true
		ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")
	}
	opts.ServeOnly, _ = cmd.Flags().GetBool(FlagServeOnly)
	if cmd.Flags().Lookup(FlagRoot) != nil {
		opts.Root, _ = cmd.Flags().GetBool(FlagRoot)
	}
	return opts, nil
}

// resolveAgentAndValidate resolves the agent (default > config > flag) and
// rejects unknown names and unsupported --worktree/--serve-only combinations.
func (r *Resolver) resolveAgentAndValidate(cmd *cobra.Command, opts *options.RunOptions) error {
	opts.Agent = defaultAgentName
	if r != nil && r.cfg.Agent != "" {
		opts.Agent = r.cfg.Agent
	}
	if name, _ := cmd.Flags().GetString(FlagAgent); name != "" && cmd.Flags().Changed(FlagAgent) {
		opts.Agent = name
	}
	if !slices.Contains(agent.Names(), opts.Agent) {
		return fmt.Errorf(
			"unknown agent %q: must be one of %s",
			opts.Agent,
			strings.Join(agent.Names(), ", "),
		)
	}
	a, _ := agent.Lookup(opts.Agent)
	if opts.Worktree.Name != "" {
		if _, ok := agent.AsWorktreeProvider(a); !ok {
			return fmt.Errorf("--worktree is not supported by agent %q", a.Name())
		}
	}
	if opts.ServeOnly {
		if _, ok := agent.AsDaemonProvider(a); !ok {
			return fmt.Errorf("--serve-only is not supported by agent %q", a.Name())
		}
	}
	return nil
}

// resolveNotify resolves the effective notify config (flag > env > config),
// then validates agent support, warning or failing for agents without a daemon.
func (r *Resolver) resolveNotify(cmd *cobra.Command, ui termio.UI, opts *options.RunOptions) error {
	nc, err := r.resolveNotifyConfig(cmd)
	if err != nil {
		return err
	}
	opts.Notify = nc
	if nc.Active() {
		a, _ := agent.Lookup(opts.Agent)
		if _, ok := agent.AsDaemonProvider(a); !ok {
			if cmd.Flags().Changed(FlagNotify) || os.Getenv(notifyEnvVar) != "" {
				return fmt.Errorf(
					"--notify is not supported by agent %q (no daemon/event stream)",
					a.Name(),
				)
			}
			ui.Warnf("notifications not supported by agent %q (no daemon/event stream); ignoring", a.Name())
			opts.Notify = notify.Config{Audio: notify.AudioOff} //nolint:exhaustruct_v5 // channels disabled
		}
	}
	return nil
}

// resolveNotifyConfig resolves the effective notify config with precedence
// flag > env > config, then validates the value.
func (r *Resolver) resolveNotifyConfig(cmd *cobra.Command) (notify.Config, error) {
	cfg := notify.Config{Audio: notify.AudioOff} //nolint:exhaustruct_v5 // zero channels, populated below
	if r != nil {
		cfg = r.cfg.Notify
		if cfg.Audio == "" {
			cfg.Audio = notify.AudioOff
		}
	}
	if raw := os.Getenv(notifyEnvVar); raw != "" {
		override, err := notify.ParseOverride(raw)
		if err != nil {
			return notify.Config{}, fmt.Errorf("%s: %w", notifyEnvVar, err)
		}
		cfg = notify.ApplyOverride(cfg, override)
	}
	if raw, _ := cmd.Flags().GetString(FlagNotify); cmd.Flags().Changed(FlagNotify) && raw != "" {
		override, err := notify.ParseOverride(raw)
		if err != nil {
			return notify.Config{}, err
		}
		cfg = notify.ApplyOverride(cfg, override)
	}
	return cfg, nil
}

// resolveNetwork resolves the egress policy: the --network flag wins over the
// resolver's policy; --dns then replaces the DNS servers while keeping any
// profile and egress lists.
func (r *Resolver) resolveNetwork(cmd *cobra.Command, opts *options.RunOptions) error {
	if raw, _ := cmd.Flags().GetString(FlagNetwork); raw != "" {
		prof, err := network.ParseProfile(raw)
		if err != nil {
			return err
		}
		opts.Network = network.Policy{Profile: prof, EgressAllow: nil, EgressDeny: nil, DNSServers: nil}
	} else if r != nil {
		opts.Network = r.cfg.Network.Effective()
	}
	if dns, _ := cmd.Flags().GetStringSlice(FlagDNSServers); len(dns) > 0 {
		opts.Network.DNSServers = dns
	}
	return nil
}

// resolveSizes validates the resolved tmp-size, disk-size, and workspace-quota
// values against the shared size grammar.
func (r *Resolver) resolveSizes(opts *options.RunOptions) error {
	if opts.TmpSize != "" {
		if _, ok := options.ParseMemoryOK(opts.TmpSize); !ok {
			return fmt.Errorf(
				"invalid --tmp-size %q: expected a size like 4G, 512M, or 2048",
				opts.TmpSize,
			)
		}
	}
	if opts.DiskSize != "" {
		if _, ok := options.ParseMemoryOK(opts.DiskSize); !ok {
			return fmt.Errorf(
				"invalid --disk-size %q: expected a size like 16G, 512M, or 4096",
				opts.DiskSize,
			)
		}
	}
	if opts.WorkspaceQuota != "" {
		if _, ok := options.ParseMemoryOK(opts.WorkspaceQuota); !ok {
			return fmt.Errorf(
				"invalid --workspace-quota %q: expected a size like 16G, 512M, or 4096",
				opts.WorkspaceQuota,
			)
		}
	}
	return nil
}

// decodeNotify reads the notify: section from dotted viper keys. It returns an
// inactive config when no notify key is set; otherwise channels are read as-is
// and triggers default to true.
func decodeNotify(v *viper.Viper) NotifyConfig {
	cfg := NotifyConfig{Audio: notify.AudioOff} //nolint:exhaustruct_v5 // remaining fields zeroed and set below
	if !v.IsSet(keyNotifyDesktop) && !v.IsSet(keyNotifyAudio) &&
		!v.IsSet(keyNotifyOnInput) && !v.IsSet(keyNotifyOnDone) && !v.IsSet(keyNotifyOnError) {
		return cfg
	}
	cfg.Desktop = v.GetBool(keyNotifyDesktop)
	if s := v.GetString(keyNotifyAudio); s != "" {
		if m, err := notify.ParseAudioMode(s); err == nil {
			cfg.Audio = m
		}
	}
	cfg.OnInput = true
	cfg.OnDone = true
	cfg.OnError = true
	if v.IsSet(keyNotifyOnInput) {
		cfg.OnInput = v.GetBool(keyNotifyOnInput)
	}
	if v.IsSet(keyNotifyOnDone) {
		cfg.OnDone = v.GetBool(keyNotifyOnDone)
	}
	if v.IsSet(keyNotifyOnError) {
		cfg.OnError = v.GetBool(keyNotifyOnError)
	}
	return cfg
}
