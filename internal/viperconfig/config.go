package viperconfig

import (
	"time"

	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
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
	// Docker bakes the tool's Docker engine block into the runner image.
	Docker bool  `mapstructure:"docker"`
	CPUs   uint8 `mapstructure:"cpus"`
	// ProvisionHostConfig controls whether the agent's host config files are
	// copied into the VM (drop-in provisioning). Default true.
	ProvisionHostConfig bool `mapstructure:"provision-host-config"`
	// OpenLinks lets processes in the VM open links on the host through the
	// xdg-open shim. Default true; false is the kill switch.
	OpenLinks bool `mapstructure:"open-links"`

	AutoStopOnActiveSessions  bool          `mapstructure:"auto-stop-on-active-sessions"`
	AutoStopTimeout           time.Duration `mapstructure:"auto-stop-timeout"`
	AutoStopMaxSessionRetries int           `mapstructure:"auto-stop-max-session-retries"`

	// Network holds the egress policy. Only Profiles is settable via env/flag.
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

	// dockerSource names the explicit docker or deprecated dind source the
	// resolved docker value came from (DockerSourceNone when none), so the
	// value overrides any project-Dockerfile content signal. A
	// present-but-false value is an explicit source.
	dockerSource DockerSource

	// Upgrade controls checking for and installing newer agents-sandbox
	// releases. Only Mode, Interval and Channel are settable via env.
	Upgrade UpgradeConfig `mapstructure:"upgrade"`
	// Notify holds the resolved notify config. It is decoded separately (not
	// via viper.Unmarshal) from dotted keys so the AGENTS_SANDBOX_NOTIFY env
	// override — the bare "notify" key — cannot collide with the nested
	// notify.desktop/audio/... keys.
	Notify NotifyConfig `mapstructure:"-"`
}

// NotifyConfig is the resolved notify setting for a session.
//
//exhaustruct:ignore
type NotifyConfig = notify.Config

// UpgradeConfig holds self-upgrade settings.
type UpgradeConfig struct {
	Mode     string        `mapstructure:"mode"`
	Interval time.Duration `mapstructure:"interval"`
	Channel  string        `mapstructure:"channel"`
}

// Resolver resolves launcher config with precedence flag > env > config > default.
type Resolver struct {
	cfg Config
	// legacyEnv holds deprecated-prefix variables that were in effect when the
	// resolver was built. The CLI surfaces them once as a deprecation warning.
	legacyEnv []string
	// dockerLegacy names the deprecated dind source in effect (empty when the
	// canonical docker setting is used). The CLI surfaces it once.
	dockerLegacy string
	// higherScope records the config keys set above the generic user config
	// (per-slug user config, project config, env, or flags), which onboarding
	// uses to avoid re-asking a value the user already set at a more specific
	// scope.
	higherScope map[string]bool
}

// ConfigLayer is one launcher-config source. The three file layers carry the
// directory a config file would live in and whether one exists there; the env
// and flag layers have no path.
type ConfigLayer struct {
	Name    string
	Path    string
	Present bool
}

// Config layer names, shown verbatim by `config paths`. Exported so the CLI can
// label the env and flag layers without duplicating the strings.
const (
	LayerNameGenericUserConfig = "generic user config"
	LayerNamePerSlugUserConfig = "per-project user config"
	LayerNameProjectConfig     = "project config"
	LayerNameLegacyEnv         = "legacy env"
	LayerNameEnv               = "env"
	LayerNameFlags             = "flags"
)

const (
	strTrue                      = "true"
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
	// KeyDocker is the config key and CLI flag name for the docker capability.
	KeyDocker = "docker"
	// KeyDind is the deprecated alias for the docker capability.
	KeyDind                = "dind"
	keyCPUs                = "cpus"
	keyQuiet               = "quiet"
	keyYes                 = "yes"
	keyUpgradeMode         = "upgrade.mode"
	keyUpgradeInterval     = "upgrade.interval"
	keyUpgradeChannel      = "upgrade.channel"
	keyProvisionHostConfig = "provision-host-config"
	// KeyOpenLinks is the config key and CLI flag name for the open-links setting.
	KeyOpenLinks     = "open-links"
	keyNotifyDesktop = "notify.desktop"
	keyNotifyAudio   = "notify.audio"
	keyNotifyOnInput = "notify.on-input"
	keyNotifyOnDone  = "notify.on-done"
	keyNotifyOnError = "notify.on-error"
)

// The env-var and CLI-flag key sets (configEnvKeys, configBindKeys) are derived
// from the Config struct tags in configkeys.go.
