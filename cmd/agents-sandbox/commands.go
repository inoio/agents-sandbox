package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/upgrade"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

// launcherConfigKey is the context key type for storing the built
// viperconfig.Resolver between PersistentPreRunE and command RunE.
type launcherConfigKey struct{}

// extractRunOptions extracts shared run/shell flags from the given command
// and returns a populated options.RunOptions.
//
//nolint:gocognit,funlen // TODO refactor
func extractRunOptions(cmd *cobra.Command, ui termio.UI) (options.RunOptions, error) {
	opts := options.RunOptions{}
	rawWorktree, _ := cmd.Flags().GetString(flagWorktree)
	worktree, err := sandbox.ResolveWorktreeSpec(rawWorktree)
	if err != nil {
		return options.RunOptions{}, err
	}
	opts.Worktree = worktree
	opts.Rebuild, _ = cmd.Flags().GetBool(flagRebuild)
	opts.DryRun, _ = cmd.Flags().GetBool(flagDryRun)
	opts.DryRunVM, _ = cmd.Flags().GetBool(flagDryRunVM)
	if opts.DryRun {
		opts.DryRunVM = true
		ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")
	}
	opts.ServeOnly, _ = cmd.Flags().GetBool(flagServeOnly)
	if cmd.Flags().Lookup(flagRoot) != nil {
		opts.Root, _ = cmd.Flags().GetBool(flagRoot)
	}

	opts.Agent = defaultAgentName
	if r := resolverFromContext(cmd.Context()); r != nil && r.Agent() != "" {
		opts.Agent = r.Agent()
	}
	if name, _ := cmd.Flags().GetString(flagAgent); name != "" && cmd.Flags().Changed(flagAgent) {
		opts.Agent = name
	}
	resolvedAgent, err := resolveAgent(opts.Agent)
	if err != nil {
		return options.RunOptions{}, err
	}
	if agentErr := validateAgentFlags(resolvedAgent, opts); agentErr != nil {
		return options.RunOptions{}, agentErr
	}

	nc, err := resolveNotifyConfig(cmd)
	if err != nil {
		return options.RunOptions{}, err
	}
	opts.Notify = nc
	if nc.Active() {
		a, _ := agent.Lookup(opts.Agent)
		if _, ok := agent.AsDaemonProvider(a); !ok {
			if cmd.Flags().Changed(flagNotify) || os.Getenv(notifyEnvVar) != "" {
				return options.RunOptions{}, fmt.Errorf(
					"--notify is not supported by agent %q (no daemon/event stream)",
					a.Name(),
				)
			}
			ui.Warnf("notifications not supported by agent %q (no daemon/event stream); ignoring", a.Name())
			opts.Notify = notify.Config{Audio: notify.AudioOff} //nolint:exhaustruct // channels disabled
		}
	}

	r := resolverFromContext(cmd.Context())
	if r != nil {
		opts.CPUs = r.CPUs()
		opts.Memory = r.Memory()
		opts.TmpSize = r.TmpSize()
		opts.DiskSize = r.DiskSize()
		opts.WorkspaceQuota = r.WorkspaceQuota()
		opts.ReapPolicy = options.NewReapPolicy(r.AutoStopOnActiveSessions(), r.AutoStopMaxSessionRetries())
		opts.IdleTimeout = r.IdleTimeout()
		opts.Dind = r.Dind()
		opts.Mounts, err = mounts.ResolveBindMounts(r.Mounts())
		if err != nil {
			return options.RunOptions{}, err
		}
		provisionHostConfig := r.ProvisionHostConfig()
		opts.ProvisionHostConfig = &provisionHostConfig
	}

	// CLI flag wins over resolver/env/config; otherwise use the resolver's
	// resolved policy (which defaults to public when unset).
	if raw, _ := cmd.Flags().GetString(flagNetwork); raw != "" {
		prof, err := network.ParseProfile(raw)
		if err != nil {
			return options.RunOptions{}, err
		}
		opts.Network = network.Policy{Profile: prof, EgressAllow: nil, EgressDeny: nil}
	} else if r := resolverFromContext(cmd.Context()); r != nil {
		opts.Network = r.Network()
	}

	if opts.TmpSize != "" {
		if _, ok := options.ParseMemoryOK(opts.TmpSize); !ok {
			return options.RunOptions{}, fmt.Errorf(
				"invalid --tmp-size %q: expected a size like 4G, 512M, or 2048",
				opts.TmpSize,
			)
		}
	}
	if opts.DiskSize != "" {
		if _, ok := options.ParseMemoryOK(opts.DiskSize); !ok {
			return options.RunOptions{}, fmt.Errorf(
				"invalid --disk-size %q: expected a size like 16G, 512M, or 4096",
				opts.DiskSize,
			)
		}
	}
	if opts.WorkspaceQuota != "" {
		if _, ok := options.ParseMemoryOK(opts.WorkspaceQuota); !ok {
			return options.RunOptions{}, fmt.Errorf(
				"invalid --workspace-quota %q: expected a size like 16G, 512M, or 4096",
				opts.WorkspaceQuota,
			)
		}
	}
	return opts, nil
}

// resolverFromContext returns the viperconfig.Resolver stored on the context,
// or nil if absent.
func resolverFromContext(ctx context.Context) *launcherconfig.Resolver {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value((*launcherConfigKey)(nil)).(*launcherconfig.Resolver)
	return r
}

// notifyEnvVar is the environment variable override for --notify.
const notifyEnvVar = "OPENCODE_SANDBOX_NOTIFY"

// resolveNotifyConfig resolves the effective notify config with precedence
// flag > env > config, then validates the value and agent support.
func resolveNotifyConfig(cmd *cobra.Command) (notify.Config, error) {
	cfg := notify.Config{Audio: notify.AudioOff} //nolint:exhaustruct // zero channels, populated below
	if r := resolverFromContext(cmd.Context()); r != nil {
		cfg = r.Notify()
	}
	if raw := os.Getenv(notifyEnvVar); raw != "" {
		override, err := notify.ParseOverride(raw)
		if err != nil {
			return notify.Config{}, fmt.Errorf("%s: %w", notifyEnvVar, err)
		}
		cfg = notify.ApplyOverride(cfg, override)
	}
	if raw, _ := cmd.Flags().GetString(flagNotify); cmd.Flags().Changed(flagNotify) && raw != "" {
		override, err := notify.ParseOverride(raw)
		if err != nil {
			return notify.Config{}, err
		}
		cfg = notify.ApplyOverride(cfg, override)
	}
	return cfg, nil
}

// defaultAgentName is the fallback agent used when --agent is not provided.
const defaultAgentName = "opencode"

// resolveAgent validates a resolved agent name (from flag, env, or config)
// against the registered agents, returning the matching agent and rejecting
// unknown names.
func resolveAgent(name string) (agent.Agent, error) {
	if !slices.Contains(agent.Names(), name) {
		return nil, fmt.Errorf(
			"unknown agent %q: must be one of %s",
			name,
			strings.Join(agent.Names(), ", "),
		)
	}
	a, _ := agent.Lookup(name)
	return a, nil
}

// bindAgentFlag registers the shared --agent flag on the given command.
func bindAgentFlag(cmd *cobra.Command) {
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile to run")
}

// resolveAgentFlag reads the --agent flag (defaulting to opencode) and returns
// the matching agent, rejecting unknown names.
func resolveAgentFlag(cmd *cobra.Command) (agent.Agent, error) {
	name, _ := cmd.Flags().GetString(flagAgent)
	return resolveAgent(name)
}

// validateAgentFlags rejects --worktree for agents without a worktree provider
// and --serve-only for agents without a daemon provider.
func validateAgentFlags(a agent.Agent, opts options.RunOptions) error {
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

// printItems renders a list of items as an aligned table with a styled header
// row. It uses the termio.Table renderer, which sizes each column to the
// widest cell and matches microsandbox's table output.
func printItems[T any](
	items []T,
	emptyMsg string,
	headers []string,
	ui termio.UI,
	funcs ...func(T) string,
) {
	if len(items) == 0 {
		ui.Info(emptyMsg)
		return
	}
	tbl := ui.NewTable(headers...)
	for _, item := range items {
		cells := make([]string, len(funcs))
		for i, f := range funcs {
			cells[i] = f(item)
		}
		tbl.AddRow(cells...)
	}
	tbl.Print()
}

func buildMinimalRootFlagsCmd() *cobra.Command {
	rootFlagsCmd := &cobra.Command{
		Use:   naming.Prefix,
		Short: "Run opencode inside an ephemeral microsandbox VM",
		Long: "Run opencode inside an ephemeral microsandbox VM.\n\n" +
			"When invoked without a subcommand, the \"run\" command is implied.",
	}

	rootFlagsCmd.PersistentFlags().BoolP(pFlagYes, pFlagYes[:1], false, "Assume yes to all prompts")
	rootFlagsCmd.PersistentFlags().BoolP(pFlagQuiet, pFlagQuiet[:1], false, "Suppress stdout output")
	rootFlagsCmd.PersistentFlags().
		StringP(pFlagLogLevel, pFlagLogLevel[:1], "info", "Minimum log level to show (error, warning, info, verbose)")

	return rootFlagsCmd
}

type autoPruneOutToVerboseRedirect struct {
	termio.UI
}

func (v *autoPruneOutToVerboseRedirect) Out(msg string) {
	v.Verbose(msg)
}
func (v *autoPruneOutToVerboseRedirect) Outf(format string, args ...any) {
	v.Verbosef(format, args...)
}

func buildRootCmd(ui termio.UI) *cobra.Command {
	rootCmd := buildMinimalRootFlagsCmd()

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		r, err := launcherconfig.NewResolver(cmd, git.ProjectSlug())
		if err != nil {
			return err
		}
		cmd.SetContext(context.WithValue(cmd.Context(), (*launcherConfigKey)(nil), r))
		return applyCLISettings(cmd, ui, r)
	}
	extendRunCmd(ui, rootCmd)

	rootCmd.AddCommand(buildRunCmd(ui))
	rootCmd.AddCommand(buildTreeCmd(rootCmd, ui))
	rootCmd.AddCommand(buildVersionCmd(rootCmd, ui))
	rootCmd.AddCommand(buildUpgradeCmd(ui))
	rootCmd.AddCommand(buildDoctorCmd(ui))
	rootCmd.AddCommand(buildBuildCmd(ui))
	rootCmd.AddCommand(buildListCmd(ui))
	rootCmd.AddCommand(buildShellCmd(ui))
	rootCmd.AddCommand(buildConfigCmd(ui))
	rootCmd.AddCommand(buildImageCmd(ui))
	rootCmd.AddCommand(buildVolumeCmd(ui))
	rootCmd.AddCommand(buildSandboxCmd(ui))
	rootCmd.AddCommand(buildStopCmd(ui))
	rootCmd.AddCommand(buildKillCmd(ui))
	rootCmd.AddCommand(buildPruneCmd(ui))

	rootCmd.SetOut(ui.StdOut())
	rootCmd.SetErr(ui.StdErr())

	return rootCmd
}

func buildTreeCmd(rootCmd *cobra.Command, ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdTree,
		Args:  cobra.NoArgs,
		Short: "Print the full command tree",
		Run: func(_ *cobra.Command, _ []string) {
			printTree(rootCmd, ui)
		},
	}
	return cmd
}

func buildVersionCmd(rootCmd *cobra.Command, ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdVersion,
		Args:  cobra.NoArgs,
		Short: "Print version",
		Run: func(_ *cobra.Command, _ []string) {
			ui.Outf("%s %s\n", rootCmd.Name(), version)
		},
	}
	return cmd
}

func buildUpgradeCmd(ui termio.UI) *cobra.Command {
	return &cobra.Command{
		Use:   cmdUpgrade,
		Args:  cobra.NoArgs,
		Short: "Check for and install the latest release",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return upgrade.Upgrade(cmd.Context(), ui, version)
		},
	}
}
