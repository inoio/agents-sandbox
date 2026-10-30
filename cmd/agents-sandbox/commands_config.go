package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configmigration"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configwizard"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

func buildConfigCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdConfig,
		Aliases: cmdConfigAliases,
		Short:   "Inspect agent and home configuration",
	}

	cmd.AddCommand(buildConfigAgentCmd(ui))
	cmd.AddCommand(buildConfigMigrateCmd(ui))
	cmd.AddCommand(buildConfigWizardCmd(ui))

	cmd.AddCommand(buildConfigPathsCmd(ui))

	cmd.AddCommand(&cobra.Command{
		Use:   cmdHome,
		Args:  cobra.NoArgs,
		Short: "List home-file mappings from the config home: key",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, _ := agent.Lookup("")
			r := resolverFromContext(cmd.Context())
			layers, has := r.Home()
			if !has {
				ui.Out("No home configuration found.")
				return nil
			}
			pairs, err := homeconfig.DescribeLayers(
				layers,
				"/home/dev",
				reservedHomeConfigTargets(a, "/home/dev"),
			)
			if err != nil {
				return err
			}
			if len(pairs) == 0 {
				ui.Out("No home mappings.")
				return nil
			}
			for _, p := range pairs {
				ui.Outf("%s  <-  %s", p[0], p[1])
			}
			return nil
		},
	})

	return cmd
}

//nolint:gochecknoglobals // command seams restored by tests
var (
	buildMigrationPlan = configmigration.Build
	applyMigrationPlan = configmigration.Apply
	migrationHomeDir   = os.UserHomeDir
)

// buildConfigMigrateCmd creates the host-only safe migration command. It never
// starts a VM and never enables native host-config provisioning implicitly.
//
//nolint:gocognit // command flow keeps migration decisions visible
func buildConfigMigrateCmd(
	ui termio.UI,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdMigrate,
		Args:  cobra.MaximumNArgs(1),
		Short: "Migrate native agent config without copying raw credentials",
		RunE: func(c *cobra.Command, args []string) error {
			name, err := resolveConfigAgentName(c, args)
			if err != nil {
				return err
			}
			a, ok := agent.Lookup(name)
			if !ok {
				return fmt.Errorf("unknown agent %q: must be one of %s", name, strings.Join(agent.Names(), ", "))
			}
			hostHome, err := migrationHomeDir()
			if err != nil {
				return fmt.Errorf("resolve host home: %w", err)
			}
			identity, err := git.ResolveIdentity(".")
			if err != nil {
				return fmt.Errorf("resolve project identity: %w", err)
			}
			resolver, err := viperconfig.NewResolver(c, effectiveConfigSlug(identity))
			if err != nil {
				return err
			}
			runOptions, err := runoptions.Build(resolver.Config(), c, ui)
			if err != nil {
				return err
			}
			plan, err := buildMigrationPlan(a, hostHome, runOptions.Network)
			if err != nil {
				if errors.Is(err, configmigration.ErrUnsupported) {
					ui.Warnf("safe migration is not available for agent %q; configure it manually", a.Name())
					return nil
				}
				return err
			}
			if !plan.HasNativeConfig && !plan.SetupOnly {
				ui.Outf("No native %s configuration found.", a.Name())
				return nil
			}
			if plan.HasManagedConfig && !plan.AlreadyConfigured {
				ui.Warnf("managed %s configuration already exists; refusing to overwrite it", a.Name())
				return nil
			}
			if len(plan.Files) == 0 && !plan.SetupOnly {
				handled, err := migrateLoginOnly(plan, a, ui)
				if err != nil {
					return err
				}
				if handled {
					return nil
				}
				ui.Warnf(
					"native %s configuration was found, but no supported credential migration is available",
					a.Name(),
				)
				for _, warning := range plan.Warnings {
					ui.Warn(warning)
				}
				return nil
			}
			if plan.AlreadyConfigured {
				ui.Outf("A safe %s migration is already configured.", a.Name())
				return nil
			}
			if plan.Dismissed {
				ui.Outf(
					"Migration was previously dismissed for the current native configuration; applying it explicitly now.",
				)
			}
			if plan.Manual || plan.LoginPending {
				ui.Outf(
					"Onboarding was previously deferred for the current native configuration; applying it explicitly now.",
				)
			}
			ui.Outf("Migrating %s configuration without copying raw credentials.", a.Name())
			if err := configmigration.Review(plan, ui); err != nil {
				return err
			}
			if err := applyMigrationPlan(plan, ui); err != nil {
				return err
			}
			ui.Outf("Safe migration completed. Secret values are stored in %s.", plan.SecretPath)
			return nil
		},
	}
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile")
	return cmd
}

// buildConfigWizardCmd returns the interactive setup-wizard command. It runs
// without an msb runtime because commandNeedsMSBRuntime exempts the config tree.
func buildConfigWizardCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdWizard,
		Args:  cobra.NoArgs,
		Short: "Interactively set launcher configuration",
		RunE: func(c *cobra.Command, _ []string) error {
			tier, _ := c.Flags().GetInt(flagTier)
			if tier < 0 || tier > configwizard.TierIntegrations {
				return fmt.Errorf("invalid tier %d: must be between 0 and %d", tier, configwizard.TierIntegrations)
			}
			return configwizard.Run(
				resolverFromContext(c.Context()),
				ui,
				configwizard.Selection{FirstRun: false, Tier: tier},
			)
		},
	}
	cmd.Flags().Int(flagTier, 0, "Configuration tier to ask (0 asks all unanswered tiers)")
	return cmd
}

// migrateLoginOnly re-arms the in-sandbox login for a login-only native
// configuration and reports whether it handled the plan.
func migrateLoginOnly(plan *configmigration.Plan, a agent.Agent, ui termio.UI) (bool, error) {
	if !plan.HasLogin() {
		return false, nil
	}
	if plan.Manual || plan.LoginPending || plan.Dismissed {
		ui.Out(
			"Onboarding was previously deferred for the current native configuration; re-arming the in-sandbox login.",
		)
	}
	if err := configmigration.RecordLoginPending(plan); err != nil {
		return false, err
	}
	ui.Outf(
		"Refreshable %s credentials are not copied; start the sandbox and authenticate inside it.",
		a.Name(),
	)
	return true, nil
}

// buildConfigPathsCmd returns the config paths subcommand: it prints the
// launcher-config layers in precedence order and names the resolved repo slug.
// The per-slug user config dir is keyed by the repo slug, so every clone of one
// origin shares it; the hint explains where repo-wide settings belong.
func buildConfigPathsCmd(ui termio.UI) *cobra.Command {
	return &cobra.Command{
		Use:   cmdPaths,
		Args:  cobra.NoArgs,
		Short: "Print the config layers in precedence order",
		RunE: func(_ *cobra.Command, _ []string) error {
			identity, err := git.ResolveIdentity(".")
			if err != nil {
				return err
			}
			// The layer block goes through Outf, which the quiet printer
			// suppresses; the hint goes through Infof so it survives --quiet.
			if !ui.Quiet() {
				printConfigLayers(ui, effectiveConfigSlug(identity), identity.RepoSlug)
			}
			ui.Infof(
				"Repo-wide settings belong in the committed project config (%s) so every clone shares them; "+
					"machine-specific settings belong in the generic user config (%s).",
				projectConfigDisplayPath,
				configpaths.Get().UserConfigDir(),
			)
			return nil
		},
	}
}

// projectConfigDisplayPath is the committed project config the hint names.
const projectConfigDisplayPath = ".agents-sandbox/config.yaml"

// printConfigLayers writes the launcher-config layers lowest precedence first,
// naming the resolved dir of each file layer and flagging the missing ones. It
// reads the per-slug layer from slug (the effective config slug) but labels the
// repo slug separately, so a bare root's pre-migration layer dir is not misread
// as the repo slug.
func printConfigLayers(ui termio.UI, slug, repoSlug string) {
	ui.Outf("config layers (lowest to highest precedence):")
	for _, layer := range viperconfig.ConfigLayers(slug) {
		if layer.Path == "" {
			ui.Outf("  %s%s", layer.Name, envLayerGlob(layer.Name))
			continue
		}
		missing := ""
		if !layer.Present {
			missing = "  (missing)"
		}
		ui.Outf("  %s  %s%s", layer.Name, layer.Path, missing)
	}
	ui.Outf("repo slug:  %s", repoSlug)
}

// envLayerGlob returns the variable-name glob shown as the second column of an
// env layer, or "" for the flags layer.
func envLayerGlob(name string) string {
	switch name {
	case viperconfig.LayerNameLegacyEnv:
		return "  OPENCODE_SANDBOX_*"
	case viperconfig.LayerNameEnv:
		return "  AGENTS_SANDBOX_*"
	default:
		return ""
	}
}

// reservedHomeConfigTargets returns the home-relative VM paths the agent
// reserves for its merged config, or nil for an agent without a ConfigMerger.
func reservedHomeConfigTargets(a agent.Agent, home string) []string {
	cm, ok := agent.AsConfigMerger(a)
	if !ok {
		return nil
	}
	rel, err := filepath.Rel(home, cm.VMConfigPath(home))
	if err != nil {
		return nil
	}
	return []string{rel}
}

// buildConfigAgentCmd returns the config agent subcommand, which prints the
// agent's merged snippet config, the host drop-in files, and the verbatim
// mirror files that provisioning would copy into the VM.
func buildConfigAgentCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdAgent,
		Args:  cobra.MaximumNArgs(1),
		Short: "Show the merged agent config and the host files provisioned into the VM",
		RunE: func(c *cobra.Command, args []string) error {
			a, err := configAgentFor(c, args)
			if err != nil {
				return err
			}
			provision := provisionHostConfigEnabled(c)
			hostHome, _ := configpaths.Get().UserHomeDir()
			merged, sources, hostFiles, mirrorFiles, source, err := reprovision.Describe(
				a,
				hostHome,
				reprovision.VMHomeDir,
				ui,
				provision,
			)
			if err != nil {
				return err
			}
			printConfigAgent(ui, a, source, merged, sources, hostFiles, mirrorFiles, provision)
			return nil
		},
	}
	cmd.Flags().String(flagAgent, defaultAgentName, "Coding agent profile")
	return cmd
}

// configAgentFor resolves the agent for the config agent command, or errors if
// the resolved name is unknown.
func configAgentFor(c *cobra.Command, args []string) (agent.Agent, error) {
	name, err := resolveConfigAgentName(c, args)
	if err != nil {
		return nil, err
	}
	a, ok := agent.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q: must be one of %s", name, strings.Join(agent.Names(), ", "))
	}
	return a, nil
}

// provisionHostConfigEnabled reports whether host config provisioning is on,
// defaulting to true when no resolver is present.
func provisionHostConfigEnabled(c *cobra.Command) bool {
	if r := resolverFromContext(c.Context()); r != nil {
		return r.ProvisionHostConfig()
	}
	return true
}

// printConfigAgent renders the merged config, mirror files, and drop-in host
// files that provisioning would copy into the VM.
func printConfigAgent(
	ui termio.UI,
	a agent.Agent,
	source reprovision.ConfigSource,
	merged []byte,
	sources []string,
	hostFiles []reprovision.HostFile,
	mirrorFiles []reprovision.MirrorFile,
	provision bool,
) {
	userDir, projectDir := reprovision.SourceDirs(source)
	sourceKind := "primary"
	if source.Fallback != "" {
		sourceKind = fmt.Sprintf("legacy fallback %q", source.Fallback)
	}
	ui.Outf("agent: %s", a.Name())
	ui.Outf(
		"config dir: user=%s  project=%s  (%s)",
		describeConfigDir(userDir),
		describeConfigDir(projectDir),
		sourceKind,
	)
	if len(sources) == 0 {
		ui.Out("No snippet files found; no merged config is provisioned.")
	} else {
		ui.Out("merged files:")
		for _, src := range sources {
			ui.Outf("  %s", src)
		}
		ui.Out("merged agent config:")
		for line := range strings.SplitSeq(string(merged), "\n") {
			ui.Outf("  %s", line)
		}
	}
	if len(mirrorFiles) > 0 {
		ui.Out("mirror files:")
		for _, mf := range mirrorFiles {
			ui.Outf("  %s  ->  %s", mf.HostPath, mf.VMPath)
		}
	}
	ui.Outf("host files (drop-in, provision-host-config=%v):", provision)
	for _, hf := range hostFiles {
		status := "not merged"
		if hf.Merged {
			status = "merged"
		}
		ui.Outf("  %-9s %s  ->  %s", status, hf.HostPath, hf.VMPath)
	}
}

// resolveConfigAgentName resolves the agent name for the config agent command:
// --agent flag, then the positional arg, then the resolver, then the default.
// An explicit --agent that conflicts with a positional arg is ambiguous.
func resolveConfigAgentName(c *cobra.Command, args []string) (string, error) {
	name := defaultAgentName
	flagName, _ := c.Flags().GetString(flagAgent)
	if c.Flags().Changed(flagAgent) {
		if len(args) > 0 && args[0] != flagName {
			return "", fmt.Errorf("ambiguous agent: --agent %q conflicts with positional %q", flagName, args[0])
		}
		return flagName, nil
	}
	if len(args) > 0 {
		return args[0], nil
	}
	if r := resolverFromContext(c.Context()); r != nil {
		if rn := r.Agent(); rn != "" {
			return rn, nil
		}
	}
	return name, nil
}

// describeConfigDir quotes dir and marks it "(missing)" when it does not exist,
// so a fresh install shows where launcher config would go.
func describeConfigDir(dir string) string {
	if reprovision.IsDir(dir) {
		return fmt.Sprintf("%q", dir)
	}
	return fmt.Sprintf("%q (missing)", dir)
}
