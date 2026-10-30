// Package runoptions resolves the shared run/shell options from a command's
// flags and the resolved launcher config, with precedence flag > env > config >
// default. ResolveDockerSetting is shared with the build command, so the two
// paths cannot resolve the docker capability differently.
package runoptions

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// Exported flag names that Build reads directly from the command.
const (
	FlagWorktree     = "worktree"
	FlagRebuild      = "rebuild"
	FlagDryRun       = "dry-run"
	FlagDryRunVM     = "dry-run-vm"
	FlagServeOnly    = "serve-only"
	FlagRoot         = "root"
	FlagAgent        = "agent"
	FlagAgentVersion = "agent-version"
	FlagNetwork      = "network"
	FlagDNSServers   = "dns"
	FlagNotify       = "notify"
	// FlagOpenLinks is the --open-links flag name, shared with the config key.
	FlagOpenLinks = viperconfig.KeyOpenLinks
)

// defaultAgentName is the fallback agent used when --agent is not provided.
const defaultAgentName = agent.DefaultAgentName

// notifyEnvVar is the primary environment variable override for --notify.
const notifyEnvVar = viperconfig.EnvPrefix + "_NOTIFY"

// legacyNotifyEnvVar is the deprecated --notify override.
const legacyNotifyEnvVar = viperconfig.LegacyEnvPrefix + "_NOTIFY"

// notifyEnvOverride returns the effective --notify environment override and the
// variable name it came from, preferring AGENTS_SANDBOX_NOTIFY over the
// deprecated OPENCODE_SANDBOX_NOTIFY. Both are empty when neither is set.
func notifyEnvOverride() (string, string) {
	for _, candidate := range []string{notifyEnvVar, legacyNotifyEnvVar} {
		if v := os.Getenv(candidate); v != "" {
			return v, candidate
		}
	}
	return "", ""
}

// Build resolves the shared run/shell options from the command's flags and the
// resolved launcher config, with precedence flag > env > config > default. A
// nil config resolves the options from the command's flags alone, still applying
// the agent default and the notify env override but leaving the config-derived
// fields (policy, sizes, mounts, provisioning) at their zero values.
func Build(cfg *viperconfig.Config, cmd *cobra.Command, ui termio.UI) (options.RunOptions, error) {
	opts, err := resolveFlags(cmd)
	if err != nil {
		return options.RunOptions{}, err
	}

	if err = resolveAgentAndValidate(cfg, cmd, &opts); err != nil {
		return options.RunOptions{}, err
	}

	if err = resolveNotify(cfg, cmd, ui, &opts); err != nil {
		return options.RunOptions{}, err
	}

	if cfg != nil {
		opts.CPUs = cfg.CPUs
		opts.Memory = cfg.Memory
		opts.TmpSize = cfg.TmpSize
		opts.DiskSize = cfg.DiskSize
		opts.WorkspaceQuota = cfg.WorkspaceQuota
		opts.ReapPolicy = options.NewReapPolicy(cfg.AutoStopOnActiveSessions, cfg.AutoStopMaxSessionRetries)
		opts.IdleTimeout = cfg.AutoStopTimeout
		opts.Mounts, err = mounts.ResolveBindMounts(cfg.Mounts)
		if err != nil {
			return options.RunOptions{}, err
		}
		provisionHostConfig := cfg.ProvisionHostConfig
		opts.ProvisionHostConfig = &provisionHostConfig
		opts.OpenLinks = cfg.OpenLinks
	} else {
		opts.OpenLinks, _ = cmd.Flags().GetBool(FlagOpenLinks)
	}

	opts.Docker = ResolveDockerSetting(cmd, cfg)

	if err := resolveNetwork(cfg, cmd, &opts); err != nil {
		return options.RunOptions{}, err
	}

	if err := resolveSizes(&opts); err != nil {
		return options.RunOptions{}, err
	}

	return opts, nil
}

// ResolveDockerSetting returns the effective docker setting for cmd's changed
// --docker/--dind flags folded over cfg, the resolved launcher config, through
// the shared docker/dind precedence. Both the run/shell path (Build) and the
// build command resolve the setting here, so the two cannot drift. A nil config
// leaves the flag result (or zero) in place, so --docker is not dropped in
// flag-only runs.
func ResolveDockerSetting(cmd *cobra.Command, cfg *viperconfig.Config) options.DockerSetting {
	sources := flagDockerSources(cmd)
	// An explicit config source contributes its value ranked by the layer it
	// came from, so a canonical config key or AGENTS_SANDBOX_DOCKER beats the
	// deprecated --dind flag.
	if cfg != nil {
		if cfg.DockerExplicit() {
			sources = append(sources, viperconfig.DockerSourceValue{
				Source: cfg.DockerSource(),
				Value:  cfg.Docker,
			})
		}
	}
	winner := viperconfig.EffectiveDockerSource(sources...)
	if winner.Source != viperconfig.DockerSourceNone {
		return options.DockerSetting{Enabled: winner.Value, Explicit: true}
	}
	// No explicit source: a config value that is set but not explicit still
	// reaches the boundary with Explicit false, so the project Dockerfile's
	// content signals decide.
	enabled := false
	if cfg != nil {
		enabled = cfg.Docker
	}
	return options.DockerSetting{Enabled: enabled, Explicit: false}
}

// flagDockerSources lists the changed --docker and --dind flags on cmd, each
// with the value it carries. A command that does not register one of the flags
// contributes nothing for it.
func flagDockerSources(cmd *cobra.Command) []viperconfig.DockerSourceValue {
	var sources []viperconfig.DockerSourceValue
	if flag := cmd.Flags().Lookup(viperconfig.KeyDocker); flag != nil && flag.Changed {
		value, _ := cmd.Flags().GetBool(viperconfig.KeyDocker)
		sources = append(sources, viperconfig.DockerSourceValue{
			Source: viperconfig.DockerSourceDockerFlag, Value: value,
		})
	}
	if flag := cmd.Flags().Lookup(viperconfig.KeyDind); flag != nil && flag.Changed {
		value, _ := cmd.Flags().GetBool(viperconfig.KeyDind)
		sources = append(sources, viperconfig.DockerSourceValue{
			Source: viperconfig.DockerSourceDindFlag, Value: value,
		})
	}
	return sources
}

// resolveFlags reads the direct-run flags off the command and returns the
// starting RunOptions, resolving the worktree spec.
func resolveFlags(cmd *cobra.Command) (options.RunOptions, error) {
	opts := options.RunOptions{}
	rawWorktree, _ := cmd.Flags().GetString(FlagWorktree)
	worktree, err := options.ResolveWorktreeSpec(rawWorktree)
	if err != nil {
		return options.RunOptions{}, err
	}
	opts.Worktree = worktree
	opts.Rebuild, _ = cmd.Flags().GetBool(FlagRebuild)
	opts.AgentVersion, _ = cmd.Flags().GetString(FlagAgentVersion)
	opts.ServeOnly, _ = cmd.Flags().GetBool(FlagServeOnly)
	if cmd.Flags().Lookup(FlagRoot) != nil {
		opts.Root, _ = cmd.Flags().GetBool(FlagRoot)
	}
	return opts, nil
}

// ResolveMode reports the execution mode the command's dry-run flags select.
// --dry-run implies --dry-run-vm, so it maps to the preview mode alone.
func ResolveMode(cmd *cobra.Command) options.Mode {
	if dryRun, _ := cmd.Flags().GetBool(FlagDryRun); dryRun {
		return options.ModePreview
	}
	if dryRunVM, _ := cmd.Flags().GetBool(FlagDryRunVM); dryRunVM {
		return options.ModeSkipVM
	}
	return options.ModeRun
}

// resolveAgentAndValidate resolves the agent (default > config > flag) and
// rejects unknown names and unsupported --worktree/--serve-only combinations.
func resolveAgentAndValidate(cfg *viperconfig.Config, cmd *cobra.Command, opts *options.RunOptions) error {
	opts.Agent = defaultAgentName
	if cfg != nil && cfg.Agent != "" {
		opts.Agent = cfg.Agent
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
func resolveNotify(cfg *viperconfig.Config, cmd *cobra.Command, ui termio.UI, opts *options.RunOptions) error {
	nc, err := resolveNotifyConfig(cfg, cmd)
	if err != nil {
		return err
	}
	opts.Notify = nc
	if nc.Active() {
		a, _ := agent.Lookup(opts.Agent)
		if _, ok := agent.AsDaemonProvider(a); !ok {
			if override, _ := notifyEnvOverride(); cmd.Flags().Changed(FlagNotify) || override != "" {
				return fmt.Errorf(
					"--notify is not supported by agent %q (no daemon/event stream)",
					a.Name(),
				)
			}
			ui.Warnf("notifications not supported by agent %q (no daemon/event stream); ignoring", a.Name())
			opts.Notify = notify.Config{Audio: notify.AudioOff}
		}
	}
	return nil
}

// resolveNotifyConfig resolves the effective notify config with precedence
// flag > env > config, then validates the value.
func resolveNotifyConfig(cfg *viperconfig.Config, cmd *cobra.Command) (notify.Config, error) {
	resolved := notify.Config{Audio: notify.AudioOff}
	if cfg != nil {
		resolved = cfg.Notify
		if resolved.Audio == "" {
			resolved.Audio = notify.AudioOff
		}
	}
	if raw, name := notifyEnvOverride(); raw != "" {
		override, err := notify.ParseOverride(raw)
		if err != nil {
			return notify.Config{}, fmt.Errorf("%s: %w", name, err)
		}
		resolved = notify.ApplyOverride(resolved, override)
	}
	// The --notify flag defaults to the empty string, so a non-empty value
	// implies it was set; an explicit empty value is ignored.
	if raw, _ := cmd.Flags().GetString(FlagNotify); raw != "" {
		override, err := notify.ParseOverride(raw)
		if err != nil {
			return notify.Config{}, err
		}
		resolved = notify.ApplyOverride(resolved, override)
	}
	return resolved, nil
}

// resolveNetwork resolves the egress policy: the --network flag wins over the
// resolved config; --dns then replaces the DNS servers while keeping any
// profile and egress lists.
func resolveNetwork(cfg *viperconfig.Config, cmd *cobra.Command, opts *options.RunOptions) error {
	if raw, _ := cmd.Flags().GetStringSlice(FlagNetwork); len(raw) > 0 {
		profiles, err := network.ParseProfiles(raw)
		if err != nil {
			return err
		}
		opts.Network = network.Policy{Profiles: profiles, EgressAllow: nil, EgressDeny: nil, DNSServers: nil}
	} else if cfg != nil {
		opts.Network = cfg.Network.Effective()
	}
	if dns, _ := cmd.Flags().GetStringSlice(FlagDNSServers); len(dns) > 0 {
		opts.Network.DNSServers = dns
	}
	return nil
}

// resolveSizes validates the resolved tmp-size, disk-size, and workspace-quota
// values against the shared size grammar.
func resolveSizes(opts *options.RunOptions) error {
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
