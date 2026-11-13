package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	config "github.com/inoio/agents-sandbox/internal/configmerge"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
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
func printConfigLayers(ui termio.Output, slug, repoSlug string) {
	ui.Outf("config layers (lowest to highest precedence):")
	for _, layer := range viperconfig.ConfigLayers(configpaths.Get(), slug) {
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
			merged, sources, hostFiles, mirrorFiles, skipped, source, err := reprovision.Describe(
				a,
				configSlugFromContext(c.Context()),
				hostHome,
				reprovision.VMHomeDir,
				ui,
				provision,
			)
			if err != nil {
				return err
			}
			printConfigAgent(ui, a, source, merged, sources, skipped, hostFiles, mirrorFiles, provision)
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
	skipped []config.SkippedSnippet,
	hostFiles []reprovision.HostFile,
	mirrorFiles []reprovision.MirrorFile,
	provision bool,
) {
	for _, s := range skipped {
		ui.Errorf("skipping agent config snippet %s: %v", s.Path, s.Err)
	}
	userDirs, projectDir := reprovision.SourceDirs(source)
	sourceKind := "primary"
	if source.Fallback != "" {
		sourceKind = fmt.Sprintf("legacy fallback %q", source.Fallback)
	}
	ui.Outf("agent: %s", a.Name())
	ui.Outf("config dirs (%s):", sourceKind)
	for _, dir := range userDirs {
		ui.Outf("  user: %s", describeConfigDir(dir))
	}
	ui.Outf("  project: %s", describeConfigDir(projectDir))
	if len(sources) == 0 {
		if len(skipped) > 0 {
			ui.Out(
				"No snippet file could be merged; every matching snippet failed to read or parse (see the errors above).",
			)
		} else {
			ui.Out("No snippet files found; no merged config is provisioned.")
		}
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
