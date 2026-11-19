package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	config "github.com/inoio/agents-sandbox/internal/configmerge"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configread"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/analyser"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/learn"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
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

	cmd.AddCommand(buildConfigLearnCmd(ui))

	cmd.AddCommand(buildConfigHardenCmd(ui))

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

// Learn-mode seams: the config learn command reads launcher state, resolves the
// home volume, and runs the same analysis the live after-run does. They are
// package variables so a test drives the command without a live VM.
var (
	learnReadSession    = state.ReadLearnSession
	learnClearSession   = state.ClearLearnSession
	learnHomeVolume     = readback.HomeVolumePath
	learnDeleteArtefact = channel.Delete
	learnAnalyse        = learn.Analyse
)

// buildConfigLearnCmd returns the config learn subcommand: it replays the last
// recorded learning session for this checkout and agent, when the owning process
// is gone.
func buildConfigLearnCmd(ui termio.UI) *cobra.Command {
	return &cobra.Command{
		Use:   cmdLearn,
		Args:  cobra.NoArgs,
		Short: "Review what the last learning session needed",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigLearn(cmd, ui)
		},
	}
}

func runConfigLearn(cmd *cobra.Command, ui termio.UI) error {
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		return err
	}
	agentName := defaultAgentName
	r := resolverFromContext(cmd.Context())
	if r != nil && r.Agent() != "" {
		agentName = r.Agent()
	}
	key := state.Key{Slug: identity.CheckoutSlug, Agent: agentName}

	record, ok, err := learnReadSession(key)
	if err != nil {
		return err
	}
	if !ok {
		ui.Infof("No learning session to review.")
		return nil
	}

	homeState, err := state.ReadState(key)
	if err != nil {
		return fmt.Errorf("read state for %s/%s: %w", key.Slug, key.Agent, err)
	}
	// A recreated volume makes the recorded baseline meaningless, so the session
	// is reported and discarded rather than analysed against a different home.
	if record.HomeVolume != homeState.HomeVolume {
		ui.Infof("The last learning session's home volume was recreated since it ran; discarding the recorded session.")
		return learnClearSession(key)
	}

	home, err := learnHomeVolume(cmd.Context(), key)
	if err != nil {
		return err
	}

	//exhaustruct:ignore // a session with no saved artefact has an empty observation
	observation := channel.Observation{}
	if record.ArtefactPath != "" {
		observation, err = channel.Load(record.ArtefactPath)
		if err != nil {
			return err
		}
	}

	//exhaustruct:ignore // the zero policy resolves to the default (public) profile
	current := network.Policy{}
	if r != nil {
		current = r.Config().Network.Effective()
	}

	//exhaustruct:ignore // ReadBack is filled by the analysis from the home volume
	analysis := learn.Analysis{
		Key:          key,
		Slug:         identity.RepoSlug,
		CheckoutSlug: identity.CheckoutSlug,
		Agent:        agentName,
		Paths:        configpaths.Get(),
		Home:         home,
		Baseline:     manifestFromState(record.Baseline),
		Observation:  observation,
		Current:      current,
	}
	_, err = learnAnalyse(cmd.Context(), analysis, ui, options.ModeRun, nil)
	if err != nil {
		return err
	}
	// The replay is the after-run for a dead process: consume the record and its
	// capture, so a second run does not re-prompt.
	if clearErr := learnClearSession(key); clearErr != nil {
		ui.Warnf("config learn: clearing the session record failed: %v", clearErr)
	}
	if record.ArtefactPath != "" {
		if delErr := learnDeleteArtefact(record.ArtefactPath); delErr != nil {
			ui.Warnf("config learn: deleting the capture failed: %v", delErr)
		}
	}
	return nil
}

// manifestFromState rebuilds the read-back manifest from a recorded baseline, so
// the replay reads the home back against the same entries the session started
// with.
func manifestFromState(entries []state.LearnManifestEntry) readback.Manifest {
	if len(entries) == 0 {
		return readback.Manifest{}
	}
	manifest := readback.Manifest{Entries: make([]readback.ManifestEntry, 0, len(entries))}
	for _, entry := range entries {
		manifest.Entries = append(manifest.Entries, readback.ManifestEntry{
			Key:     entry.Key,
			Size:    entry.Size,
			ModTime: entry.ModTime,
			Hash:    entry.Hash,
		})
	}
	return manifest
}

// buildConfigHardenCmd returns the config harden subcommand: it audits the
// effective configuration and applies the config-only proposals interactively.
// It needs no sandbox — it edits host files only — and is re-runnable.
func buildConfigHardenCmd(ui termio.UI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdHarden,
		Args:  cobra.NoArgs,
		Short: "Audit the configuration and tighten the risky parts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigHarden(cmd, ui)
		},
	}
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would change without writing")
	return cmd
}

// runConfigHarden audits the effective config: it derives the config-only
// findings at the effective slug's scopes and applies them through the same
// applier a learning session uses. It reads the project layer only when the
// trust gate left it in effect — the gate already reports an unapproved layer
// once and excludes it — so an unapproved project secret is never read or
// moved.
func runConfigHarden(cmd *cobra.Command, ui termio.UI) error {
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		return err
	}
	slug := configSlugFromContext(cmd.Context())
	if slug == "" {
		slug = effectiveConfigSlug(identity)
	}
	paths := configpaths.Get()
	scopes := configpaths.ConfigScopes(paths, slug)

	//exhaustruct:ignore // a config-only run carries no session or read-back
	facts := analyser.ConfigFacts{
		Paths:  paths,
		Slug:   slug,
		Config: resolverConfig(cmd),
		Scopes: scopes,
		Logger: ui,
	}
	findings := analyser.ConfigFindings(facts)
	// A clean configuration is quiet: the applier prints a notice even with no
	// findings, so it is not reached here.
	if len(findings) == 0 {
		return nil
	}

	//exhaustruct:ignore // SecretValue is set below; a config-only run accepts no --yes
	opts := analyser.ApplyOptions{
		Paths:        paths,
		Slug:         slug,
		CheckoutSlug: identity.CheckoutSlug,
		UI:           ui,
		Mode:         runoptions.ResolveMode(cmd),
		SetByFlag:    func(string) bool { return false },
		SecretValue:  hardenSecretValue(scopes, agentNameForHarden(cmd), ui),
	}
	results := analyser.Apply(findings, opts)
	switch {
	case opts.Mode.Previews():
		// A dry run previews each finding's proposed change without writing; the
		// applier already reported that it made no change.
		printHardenPreview(ui, results)
	case ui.IsInteractive():
		printHardenSummary(ui, results)
	}
	return nil
}

// resolverConfig returns the effective launcher config, or nil when no resolver
// is present (a bare command); the finder then skips the policy and inline
// findings and audits the secret scopes alone.
func resolverConfig(cmd *cobra.Command) *viperconfig.Config {
	if r := resolverFromContext(cmd.Context()); r != nil {
		return r.Config()
	}
	return nil
}

// agentNameForHarden returns the agent whose config dirs the finder and the
// secret-value lookup scan, defaulting like config learn.
func agentNameForHarden(cmd *cobra.Command) string {
	if r := resolverFromContext(cmd.Context()); r != nil && r.Agent() != "" {
		return r.Agent()
	}
	return defaultAgentName
}

// hardenSecretValue resolves a proposed static secret's value in memory, from
// both places harden reads a secret it is about to re-write: the scopes'
// env.secret.yaml files (a scope secret that is re-scoped) and the agent config
// files under each scope's agent config dir (an inline config secret, C7). The
// value is returned to the writer and never logged, carried in a finding, or
// persisted anywhere else.
func hardenSecretValue(
	scopes []configpaths.ConfigScope,
	agentName string,
	ui termio.Logger,
) func(analyser.SecretWrite) (string, bool) {
	dirNames := agentConfigDirNames(agentName)
	return func(write analyser.SecretWrite) (string, bool) {
		if value, ok := secretValueFromSource(write.Source, write.EnvVar, ui); ok {
			return value, true
		}
		if value, ok := secretValueFromScopes(scopes, write.EnvVar, ui); ok {
			return value, true
		}
		return secretValueFromAgentConfigs(scopes, dirNames, write.EnvVar)
	}
}

// secretValueFromSource resolves envVar from the exact location a finding came
// from: a scope directory's env.secret.yaml, or an agent config file's inline
// credential. Resolving from the source rather than by precedence keeps a move's
// value with its own entry when the same env var is declared in another scope.
func secretValueFromSource(source, envVar string, ui termio.Logger) (string, bool) {
	info, err := os.Stat(source)
	if err != nil {
		return "", false
	}
	if !info.IsDir() {
		return inlineCredentialValueInFile(source, envVar)
	}
	specs := envsecret.ParseSecretSpecYAML(filepath.Join(source, configpaths.EnvSecretFileName+".yaml"), ui)
	// An absent env var yields the zero spec, whose value is empty, so the
	// value check alone covers it.
	spec := specs[envVar]
	if spec.Value == "" {
		return "", false
	}
	return spec.Value, true
}

// secretValueFromScopes returns the literal value of envVar from the highest-
// precedence scope whose env.secret.yaml declares it, preferring a non-empty
// value. It walks the scopes in reverse (project → per-slug → generic), matching
// the effective-config precedence, so a same-named lower-scope key does not
// supply the wrong value. Each file is read through envsecret, which routes the
// project scope through the project guard, so an unapproved project secret is
// never read.
func secretValueFromScopes(
	scopes []configpaths.ConfigScope,
	envVar string,
	ui termio.Logger,
) (string, bool) {
	for _, scope := range slices.Backward(scopes) {
		specs := envsecret.ParseSecretSpecYAML(scope.EnvSecretYAMLFile(), ui)
		// An absent env var yields the zero spec, whose value is empty.
		if spec := specs[envVar]; spec.Value != "" {
			return spec.Value, true
		}
	}
	return "", false
}

// secretValueFromAgentConfigs returns the literal value of envVar from the
// highest-precedence scope whose agent config dir declares it inline. It walks
// the scopes in reverse, matching the effective-config precedence. A login store
// or the dotenv is skipped, and the read goes through the project guard, so an
// unapproved project file is not read.
func secretValueFromAgentConfigs(
	scopes []configpaths.ConfigScope,
	dirNames []string,
	envVar string,
) (string, bool) {
	for _, scope := range slices.Backward(scopes) {
		for _, name := range dirNames {
			if value, ok := inlineCredentialValue(scope.AgentConfigDir(name), envVar); ok {
				return value, true
			}
		}
	}
	return "", false
}

// inlineCredentialValue returns envVar's inline value from the agent config
// files directly under dir.
func inlineCredentialValue(dir, envVar string) (string, bool) {
	entries, err := os.ReadDir(dir)
	// gomutants:disable-next-line BRANCH_IF reason="a failed ReadDir leaves entries nil, so the loop finds no file and the fall-through returns the same empty result"
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		// gomutants:disable-next-line BRANCH_IF reason="a directory reaching the file read fails to open, so the fall-through returns no value either"
		if entry.IsDir() {
			continue
		}
		if value, ok := inlineCredentialValueInFile(filepath.Join(dir, entry.Name()), envVar); ok {
			return value, true
		}
	}
	return "", false
}

// inlineCredentialValueInFile returns envVar's inline value from one agent
// config file, skipping a login store or the dotenv and reading through the
// project guard.
func inlineCredentialValueInFile(path, envVar string) (string, bool) {
	if readback.SkipsInlineCredentials(path) {
		return "", false
	}
	data, err := projectguard.ReadFile(path, configread.ReadFile)
	// gomutants:disable-next-line BRANCH_IF reason="a read failure leaves data nil, which decodes to no credentials through the same path, so the early return changes nothing"
	if err != nil {
		return "", false
	}
	for _, credential := range readback.InlineCredentialsIn(path, data) {
		if credential.EnvVar == envVar && credential.Value != "" {
			return credential.Value, true
		}
	}
	return "", false
}

// agentConfigDirNames returns the agent's config dir names, or nil for an agent
// without a resolved name; the finder and the value lookup then scan no dirs.
// A registered profile's dir names are validated at registration, so the error
// is unreachable here (see allAgentConfigDirNames); the names are dropped on it
// the same way a nil name is.
func agentConfigDirNames(agentName string) []string {
	profile, ok := agent.Lookup(agentName)
	if !ok {
		return nil
	}
	dirNames, _ := agent.ConfigDirNames(profile)
	return dirNames
}

// printHardenSummary reports a one-line outcome summary — how many findings
// were applied, declined, reported, or skipped — and a line for each finding
// that was not applied, so an interactive run shows what it wrote and why a
// finding stayed.
func printHardenSummary(ui termio.UI, results []analyser.ApplyResult) {
	var applied, declined, skipped, notApplicable, failed int
	for _, result := range results {
		switch result.Outcome {
		case analyser.OutcomeApplied:
			applied++
		case analyser.OutcomeDeclined:
			declined++
		case analyser.OutcomeSkipped:
			skipped++
		case analyser.OutcomeFailed:
			failed++
		case analyser.OutcomeNotApplicable:
			notApplicable++
		}
	}
	ui.Infof("config harden: %d findings: %d applied, %d declined, %d skipped, %d reported, %d failed.",
		len(results), applied, declined, skipped, notApplicable, failed)
	for _, result := range results {
		switch result.Outcome {
		case analyser.OutcomeSkipped:
			ui.Infof("config harden: skipped %s: %s", findingNoun(result.Finding), result.Note)
		case analyser.OutcomeNotApplicable:
			ui.Infof("config harden: reported %s: %s", findingNoun(result.Finding), result.Note)
		case analyser.OutcomeFailed:
			ui.Warnf("config harden: %s failed: %s", findingNoun(result.Finding), result.Err)
		case analyser.OutcomeApplied, analyser.OutcomeDeclined:
		}
	}
}

// printHardenPreview renders each finding's proposed change under --dry-run: its
// evidence summary and every write's Text, which carries no secret value. The
// applier writes nothing and asks nothing, so this is what a preview shows.
func printHardenPreview(ui termio.UI, results []analyser.ApplyResult) {
	for _, result := range results {
		ui.Outf("config harden: would change %s", findingNoun(result.Finding))
		for _, write := range result.Finding.Writes {
			if write.Text != "" {
				ui.Outf("  %s", write.Text)
			}
		}
	}
}

// findingNoun names a finding by its evidence summary, so the summary line says
// what was written without repeating the proposed change.
func findingNoun(finding analyser.Finding) string {
	if finding.Evidence.Summary != "" {
		return finding.Evidence.Summary
	}
	return string(finding.Kind)
}
