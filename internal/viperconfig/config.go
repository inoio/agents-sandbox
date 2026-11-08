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

	// Secrets holds secret-global settings that apply to every secret.
	Secrets Secrets `mapstructure:"secrets"`

	// Ports are user-published VM ports ("<port>" or "<host>:<guest>"), exposed
	// on the host loopback at VM creation.
	Ports []string `mapstructure:"ports"`
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
	// ConfigSlug is the effective per-slug user config directory slug the
	// resolver was built with (empty when no per-slug scope was selected). It is
	// resolved at the run boundary, never read from a config file or env.
	ConfigSlug string `mapstructure:"-"`

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

// Secrets holds secret-global launcher settings.
type Secrets struct {
	// Common holds settings shared by every secret.
	Common SecretCommon `mapstructure:"common"`
}

// SecretCommon holds settings that apply to every secret.
//
//exhaustruct:ignore
type SecretCommon struct {
	// AllowPlaceholderFor lists hosts that may receive any secret's placeholder
	// unchanged (the allow-placeholder-for key). It is config-file-only: no env
	// var and no flag, so an LLM model host is trusted deliberately. A host a
	// secret already allows keeps substitution (see docs/configuration/secrets.md).
	AllowPlaceholderFor []string `mapstructure:"allow-placeholder-for" env:"-"`
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
	// slug is the config slug the resolver was built for (the CLI's
	// effectiveConfigSlug): the per-slug user config directory it reads is
	// keyed by it, and a config write must target the same slug.
	slug string
	// legacyEnv holds deprecated-prefix variables that were in effect when the
	// resolver was built. The CLI surfaces them once as a deprecation warning.
	legacyEnv []string
	// dockerLegacy names the deprecated dind source in effect (empty when the
	// canonical docker setting is used). The CLI surfaces it once.
	dockerLegacy string
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
	keyAutoPruneAge              = "auto-prune-age"
	keyManualPruneAge            = "manual-prune-age"
	keyAutoStopOnActiveSessions  = "auto-stop-on-active-sessions"
	keyAutoStopTimeout           = "auto-stop-timeout"
	keyAutoStopMaxSessionRetries = "auto-stop-max-session-retries"
	keyNetworkProfile            = "network.profile"
	keyNetworkDNSServers         = "network.dns-servers"
	keyPorts                     = "ports"
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
