package viperconfig

import (
	"maps"
	"slices"
	"time"

	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/upgrade"
)

// LegacyEnvVars returns the deprecated-prefix environment variables that were
// in effect (their AGENTS_SANDBOX_ counterpart was unset) when the resolver
// was built, so the CLI can warn once.
func (r *Resolver) LegacyEnvVars() []string {
	return r.legacyEnv
}

// DockerLegacyAlias returns the deprecated dind source that was in effect
// (empty when the canonical docker setting is used), so the CLI can warn once.
func (r *Resolver) DockerLegacyAlias() string {
	return r.dockerLegacy
}

// DockerSource reports which explicit source the config's docker value came
// from, or DockerSourceNone when no explicit source is present. It carries the
// layer information the run and build paths need to fold their flags into the
// same precedence. Both docker reads live on Config, which Resolver.Config
// exposes: a Resolver wrapper for the same value would be test-only, and the
// resolver's any-typed context value hides that from the dead-code gate.
func (c Config) DockerSource() DockerSource { return c.dockerSource }

// DockerExplicit reports whether the config's docker value came from an
// explicit source, so it overrides any project-Dockerfile content signal.
func (c Config) DockerExplicit() bool { return c.dockerSource != DockerSourceNone }

func (r *Resolver) Yes() bool { return r.cfg.Yes }

func (r *Resolver) Quiet() bool { return r.cfg.Quiet }

func (r *Resolver) LogLevel() string { return r.cfg.LogLevel }

func (r *Resolver) Agent() string { return r.cfg.Agent }

func (r *Resolver) ProvisionHostConfig() bool { return r.cfg.ProvisionHostConfig }

func (r *Resolver) AutoPruneAge() time.Duration { return r.cfg.AutoPruneAge }

func (r *Resolver) ManualPruneAge() time.Duration { return r.cfg.ManualPruneAge }

// Config returns a deep copy of the resolved launcher config, or nil when the
// resolver itself is nil. runoptions.Build treats a nil config as flag-only,
// with no config, env, or defaults applied. The copy owns its slices and maps,
// so a caller mutating the returned config cannot change later resolver reads.
func (r *Resolver) Config() *Config {
	if r == nil {
		return nil
	}
	return r.cfg.clone()
}

// clone returns a deep copy of the config, cloning its reference fields (the
// network policy slices, mounts, and home layers) so the copy shares no mutable
// state with the original.
func (c Config) clone() *Config {
	out := c
	out.Network.Profiles = slices.Clone(c.Network.Profiles)
	out.Network.EgressAllow = slices.Clone(c.Network.EgressAllow)
	out.Network.EgressDeny = slices.Clone(c.Network.EgressDeny)
	out.Network.DNSServers = slices.Clone(c.Network.DNSServers)
	out.Mounts = maps.Clone(c.Mounts)
	out.Home = slices.Clone(c.Home)
	for i := range out.Home {
		out.Home[i].Manifest = maps.Clone(c.Home[i].Manifest)
	}
	return &out
}

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

// UpgradeChannel returns the configured release channel, defaulting to stable.
// An unparsable value (possible via NewResolverWithConfig, which skips
// validation) also falls back to stable.
func (r *Resolver) UpgradeChannel() upgrade.Channel {
	ch, err := upgrade.ParseChannel(r.cfg.Upgrade.Channel)
	if err != nil {
		return upgrade.ChannelStable
	}
	return ch
}

// UpgradeInterval returns the configured upgrade-check interval, defaulting to
// a day and never returning less than the rate-limit guard.
func (r *Resolver) UpgradeInterval() time.Duration {
	switch {
	case r.cfg.Upgrade.Interval <= 0:
		return upgrade.DefaultInterval
	case r.cfg.Upgrade.Interval < upgrade.MinInterval: // gomutants:disable CONDITIONALS_BOUNDARY reason="at Interval == MinInterval both this case and the default return MinInterval"
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
