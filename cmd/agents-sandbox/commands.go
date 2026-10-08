package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	msbruntime "github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/upgrade"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

// launcherConfigKey is the context key type for storing the built
// viperconfig.Resolver between PersistentPreRunE and command RunE.
type launcherConfigKey struct{}

// resolverFromContext returns the viperconfig.Resolver stored on the context,
// or nil if absent.
func resolverFromContext(ctx context.Context) *launcherconfig.Resolver {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value((*launcherConfigKey)(nil)).(*launcherconfig.Resolver)
	return r
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
		if settingsErr := applyCLISettings(cmd, ui, r); settingsErr != nil {
			return settingsErr
		}
		if !commandNeedsMSBRuntime(cmd) {
			return nil
		}
		prepared, err := prepareMSBRuntime(cmd.Context(), ui, version)
		if err != nil {
			return err
		}
		if prepared.Restart {
			return &sandbox.ExitError{Code: 0}
		}
		return nil
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

var prepareMSBRuntime = msbruntime.PrepareRuntime

// commandNeedsMSBRuntime reports whether the command can reach the
// microsandbox SDK. Commands that only inspect launcher state must remain
// usable when the selected microsandbox runtime is broken.
func commandNeedsMSBRuntime(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	for current := cmd; current != nil; current = current.Parent() {
		if current.Name() == cmdConfig || current.Name() == cmdCompletion {
			return false
		}
	}
	switch cmd.Name() {
	case cmdVersion, cmdUpgrade, cmdTree, cmdHelp, cmdDockerfile:
		return false
	default:
		return true
	}
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
