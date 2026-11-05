package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	msbruntime "github.com/inoio/agents-sandbox/internal/sandbox/runtime"
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

// configSlugFromContext returns the resolver's effective per-slug config slug, or
// "" when no resolver is present.
func configSlugFromContext(ctx context.Context) string {
	if r := resolverFromContext(ctx); r != nil {
		return r.Config().ConfigSlug
	}
	return ""
}

// defaultAgentName is the fallback agent used when --agent is not provided.
const defaultAgentName = agent.DefaultAgentName

// warnLegacyEnvVars emits a one-time deprecation warning for each
// OPENCODE_SANDBOX_ variable still in effect.
func warnLegacyEnvVars(ui termio.Logger, r *launcherconfig.Resolver) {
	if legacy := r.LegacyEnvVars(); len(legacy) > 0 {
		ui.Warnf(
			"deprecated environment variable(s) %s in use; switch to the AGENTS_SANDBOX_ prefix",
			strings.Join(legacy, ", "),
		)
	}
	if alias := r.DockerLegacyAlias(); alias != "" {
		ui.Warnf("deprecated %s in use; switch to the docker/AGENTS_SANDBOX_DOCKER spelling", alias)
	}
}

// effectiveConfigSlug returns the slug whose per-slug user config directory the
// launcher should read. It is the repo slug — shared by every clone of a remote —
// except for a bare root whose legacy config directory has not been migrated:
// its legacy config lives under the path-derived legacy slug
// (Identity.LegacyRepoSlugs), which adoption renames onto the repo slug. The
// resolver is built before adoption runs, so without this the triggering run
// would load defaults instead of the user's saved settings. The fallback is
// read-only, so a dry run stays side-effect-free.
func effectiveConfigSlug(identity git.Identity) string {
	if slugHasUserInputs(identity.RepoSlug) {
		return identity.RepoSlug
	}
	for _, legacy := range identity.LegacyRepoSlugs {
		if slugHasUserInputs(legacy) {
			return legacy
		}
	}
	return identity.RepoSlug
}

// slugHasUserInputs reports whether the per-slug user config dir for slug holds
// any per-slug user input the launcher consumes: a launcher config file (found
// with the same discovery NewResolver merges from), an env/secret file, or a
// registered agent's config dir. A secrets-only dir counts, so a bare root is
// not routed to its legacy slug just because its per-slug dir has no config
// file yet (#578). An empty slug has no per-slug scope, so it reports false.
func slugHasUserInputs(slug string) bool {
	for _, scope := range configpaths.ConfigScopes(configpaths.Get(), slug) {
		if scope.Kind != configpaths.ScopePerSlugUser {
			continue
		}
		if _, _, present := configpaths.FindConfigFile(scope.Dir); present {
			return true
		}
		if pathExists(scope.EnvFile()) || pathExists(scope.EnvSecretFile()) || pathExists(scope.EnvSecretYAMLFile()) {
			return true
		}
		for _, name := range agent.Names() {
			a, _ := agent.Lookup(name)
			// The built-in registry validates its own config-dir names, so an
			// invalid name simply contributes no candidate; there is no error
			// branch to handle here.
			dirNames, _ := agent.ConfigDirNames(a)
			for _, dirName := range dirNames {
				if dirExists(scope.AgentConfigDir(dirName)) {
					return true
				}
			}
		}
	}
	return false
}

// pathExists reports whether path exists; any stat error (including a missing
// file) reports false.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dirExists reports whether path names an existing directory. A regular file
// with an agent-config-dir name must not count as a populated per-slug dir,
// matching the directory requirement reprovision's anyDirExists enforces.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

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

func buildMinimalRootFlagsCmd() *cobra.Command {
	rootFlagsCmd := &cobra.Command{
		Use:   naming.Prefix,
		Short: "Run opencode inside an ephemeral microsandbox VM",
		Long: "Run opencode inside an ephemeral microsandbox VM.\n\n" +
			"When invoked without a subcommand, the \"run\" command is implied.",
	}

	rootFlagsCmd.PersistentFlags().BoolP(pFlagYes, pFlagYes[:1], false, "Assume yes to all prompts")
	rootFlagsCmd.PersistentFlags().
		BoolP(pFlagQuiet, pFlagQuiet[:1], false, "Suppress stdout; on list commands print identifiers only")
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
		// Config stays on the repo slug: the per-slug user config dir is shared by
		// every clone of one origin, so it must not follow the checkout slug. A
		// bare root before its config migration is the one exception, resolved by
		// effectiveConfigSlug.
		identity, err := git.ResolveIdentity(".")
		if err != nil {
			return fmt.Errorf("resolve project identity: %w", err)
		}
		r, err := launcherconfig.NewResolver(cmd, configpaths.Get(), effectiveConfigSlug(identity))
		if err != nil {
			return err
		}
		warnLegacyEnvVars(ui, r)
		cmd.SetContext(context.WithValue(cmd.Context(), (*launcherConfigKey)(nil), r))
		if settingsErr := applyCLISettings(cmd, ui, r); settingsErr != nil {
			return settingsErr
		}
		if !commandNeedsMSBRuntime(cmd) {
			return nil
		}
		// A dry-run preview is documented to make no network request and no
		// runtime download/install, so it must not provision the runtime. It still
		// points the SDK at the private home so its read-only inspection
		// (volumes, sandboxes) reports the objects a real run would use.
		if runoptions.ResolveMode(cmd).Previews() {
			return applyPreviewHome()
		}
		return prepareMSBRuntime(cmd.Context(), ui, version)
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
	rootCmd.AddCommand(buildDFCmd(ui))
	rootCmd.AddCommand(buildStorageCmd(ui))
	rootCmd.AddCommand(buildStateCmd(ui))

	rootCmd.SetOut(ui.StdOut())
	rootCmd.SetErr(ui.StdErr())

	return rootCmd
}

var prepareMSBRuntime = msbruntime.PrepareRuntime

// applyPreviewHome is the preview path's home redirection, a seam so a test can
// drive its error branch.
var applyPreviewHome = msbruntime.ApplyPreviewHome

// commandNeedsMSBRuntime reports whether the root preflight should provision the
// microsandbox runtime before the command body runs. Commands that only inspect
// launcher state must remain usable when the runtime is broken; that includes
// `state repair`, the recovery path for a corrupt launcher state file. `doctor` is
// included among those: it runs the runtime check itself, so an install failure
// is reported with its troubleshooting link alongside the KVM and Docker results
// instead of aborting the preflight with a raw error.
func commandNeedsMSBRuntime(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	for current := cmd; current != nil; current = current.Parent() {
		if current.Name() == cmdConfig || current.Name() == cmdCompletion || current.Name() == cmdState {
			return false
		}
	}
	switch cmd.Name() {
	case cmdVersion, cmdUpgrade, cmdTree, cmdHelp, cmdDockerfile, cmdDoctor:
		return false
	default:
		return true
	}
}

func buildTreeCmd(rootCmd *cobra.Command, ui termio.Logger) *cobra.Command {
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

func buildVersionCmd(rootCmd *cobra.Command, ui termio.Output) *cobra.Command {
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

func buildUpgradeCmd(ui termio.Logger) *cobra.Command {
	var channelFlag string
	cmd := &cobra.Command{
		Use:   cmdUpgrade,
		Args:  cobra.NoArgs,
		Short: "Check for and install the latest release",
		RunE: func(cmd *cobra.Command, _ []string) error {
			channel := upgrade.ChannelStable
			if r := resolverFromContext(cmd.Context()); r != nil {
				channel = r.UpgradeChannel()
			}
			if cmd.Flags().Changed(flagChannel) {
				parsed, err := upgrade.ParseChannel(channelFlag)
				if err != nil {
					return err
				}
				channel = parsed
			}
			return upgrade.Upgrade(cmd.Context(), ui, upgrade.UpgradeOptions{
				CurrentVersion:  version,
				Channel:         channel,
				AllowAnyVersion: upgradeAnyVersionEnabled(),
				// Only an explicit --channel may move a prerelease install to an
				// older stable release; a configured channel never downgrades.
				AllowChannelSwitch: cmd.Flags().Changed(flagChannel),
			})
		},
	}
	cmd.Flags().StringVar(&channelFlag, flagChannel, "", "release channel to upgrade from (stable or canary)")
	return cmd
}
