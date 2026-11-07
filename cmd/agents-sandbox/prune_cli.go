package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/cliout"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/view"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// confirmPrune gates a destructive prune behind confirmation. A dry-run only
// previews, so it is authorized without prompting. The global --yes flag
// authorizes non-interactively through ui.Confirm's assume-yes path; a
// non-interactive UI without --yes fails fast via ui.Confirm rather than
// blocking.
func confirmPrune(ui termio.Prompter, dryRun bool) error {
	if dryRun {
		return nil
	}
	return termio.ConfirmAction(ui, "Prune stale runtime objects?")
}

// resolvePruneAge returns the effective prune threshold for a manual prune:
// --older-than (or the deprecated --age) if set, else manual-prune-age from
// config, else the 7d default.
func resolvePruneAge(cmd *cobra.Command) (time.Duration, error) {
	olderThanFlag := cmd.Flags().Lookup(flagOlderThan)
	ageFlag := cmd.Flags().Lookup(flagAge)
	if olderThanFlag.Changed && ageFlag.Changed {
		return 0, fmt.Errorf("use either --%s or --%s, not both", flagOlderThan, flagAge)
	}
	if !olderThanFlag.Changed && !ageFlag.Changed {
		if r := resolverFromContext(cmd.Context()); r != nil && r.ManualPruneAge() > 0 {
			return r.ManualPruneAge(), nil
		}
		return 7 * 24 * time.Hour, nil
	}
	ageStr := olderThanFlag.Value.String()
	if ageFlag.Changed {
		ageStr = ageFlag.Value.String()
	}
	d, ok := viperconfig.ParseHumanDuration(ageStr)
	if !ok {
		return 0, fmt.Errorf("invalid age %q: use a Go duration or suffix d/w (e.g. 7d, 2w)", ageStr)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--%s must be > 0, got %v", flagOlderThan, ageStr)
	}
	return d, nil
}

// runGroupPrune runs one group pruner behind the shared prune flag handling:
// output mode and confirmation are resolved (so bad flags fail fast), then the
// report is rendered even when the pruner returns an error, so a partial run
// still reports what it removed before the error surfaces.
func runGroupPrune[R any](
	cmd *cobra.Command,
	ui termio.UI,
	pruneLambda func(context.Context, pruning.PruneState, bool, termio.UI) (R, error),
	render func(report R, dryRun bool),
	jsonValue func(report R, dryRun bool) any,
) error {
	age, dryRun, err := resolvePruneFlags(cmd)
	if err != nil {
		return err
	}
	mode, err := resolvePruneOutputMode(cmd, ui)
	if err != nil {
		return err
	}
	if err = confirmPrune(ui, dryRun); err != nil {
		return err
	}
	report, pruneErr := pruning.InvokePruneFunc(cmd.Context(), pruneLambda, age, dryRun, ui)
	cliout.RenderPruneReport(ui, mode,
		func() { render(report, dryRun) },
		jsonValue(report, dryRun))
	if pruneErr != nil {
		return pruneErr
	}
	return nil
}

func resolvePruneFlags(cmd *cobra.Command) (time.Duration, bool, error) {
	age, err := resolvePruneAge(cmd)
	if err != nil {
		return 0 * time.Second, false, err
	}
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	return age, dryRun, nil
}

func resolvePruneOutputMode(cmd *cobra.Command, ui termio.TerminalState) (cliout.Mode, error) {
	format, _ := cmd.Flags().GetString(flagFormat)
	return cliout.ResolveMode(format, ui.Quiet())
}

func setPruneFlags(cmd *cobra.Command) {
	cmd.Flags().String(flagOlderThan, "7d", "Prune threshold (default: manualPruneAge from config)")
	cmd.Flags().StringP(flagAge, flagAge[:1], "", "Deprecated alias for --older-than")
	_ = cmd.Flags().MarkDeprecated(flagAge, "use --older-than")
	cmd.Flags().BoolP(flagDryRun, flagDryRunShort, false, "Show what would be pruned without deleting")
	cmd.Flags().String(flagFormat, "", "Output format (json)")
}

// purgeCwdTarget is the --purge value that selects the current checkout's
// project. A bare --purge yields it, so the target can be omitted while
// --purge=<target> names one explicitly.
const purgeCwdTarget = "cwd"

// runPruneBatch runs the safe-tier batch prune, then lists the retired projects
// the artifact union still observes without deleting them. It never removes a
// home volume or a retired project's state: full removal is only the explicit
// purge.
func runPruneBatch(
	cmd *cobra.Command,
	ui termio.UI,
	mode cliout.Mode,
	age time.Duration,
	dryRun bool,
) error {
	if err := confirmPrune(ui, dryRun); err != nil {
		return err
	}
	report, pruneErr := pruning.Prune(cmd.Context(), docker.Get(), pruning.PruneOptions{
		Age:    age,
		DryRun: dryRun,
		Retain: nil,
	}, ui)
	var retired []string
	var retiredErr error
	if report != nil {
		retired, retiredErr = retiredProjectSlugs(cmd.Context(), age)
		jsonReport := view.NewPruneReportJSON(report, dryRun)
		jsonReport.RetiredProjects = retired
		cliout.RenderPruneReport(ui, mode,
			func() {
				pruning.RenderPruneSummary(ui, report, dryRun)
				view.RenderRetiredProjects(ui, retired)
			},
			jsonReport)
	}
	return errors.Join(pruneErr, retiredErr)
}

// runPrunePurge runs a targeted purge: it resolves the target, plans the
// removal, refuses a running sandbox until --force, names the plan, confirms,
// and executes. A dry run previews the plan without prompting or deleting.
func runPrunePurge(
	cmd *cobra.Command,
	ui termio.UI,
	mode cliout.Mode,
	dryRun bool,
	target string,
) error {
	slugs, agent, err := resolvePurgeSlugs(target)
	if err != nil {
		return err
	}
	plan, err := pruning.PlanRemoval(cmd.Context(), slugs, agent)
	if err != nil {
		return err
	}
	force, _ := cmd.Flags().GetBool(flagForce)
	if !force && !dryRun && plan.HasRunningSandbox() {
		return errors.New("a purge target has a running sandbox: stop it first or pass --force to purge it")
	}
	cliout.RenderPruneReport(ui, mode,
		func() { view.RenderRemovalPlan(plan, ui, dryRun) },
		view.NewRemovalPlanJSON(plan, dryRun))
	if err := confirmPrune(ui, dryRun); err != nil {
		return err
	}
	return plan.Execute(cmd.Context(), force, dryRun, ui)
}

// purgeTarget returns the --purge target: the flag's explicit value, or a bare
// --purge's positional argument when one is given. A bare --purge with no
// argument returns the empty, cwd-implicit target.
func purgeTarget(cmd *cobra.Command, args []string) string {
	target, _ := cmd.Flags().GetString(flagPurge)
	if target != purgeCwdTarget {
		return target
	}
	if len(args) == 1 {
		return args[0]
	}
	return ""
}

// resolvePurgeSlugs resolves a --purge target into the slugs to purge and the
// agent to scope them to ("" purges every agent of every slug). An explicit
// target is a project slug or a <slug>-<agent> key, its agent separated by the
// slug's embedded 14-character hash. An empty target is the cwd-implicit form:
// the current checkout's slug plus any legacy repo slugs a bare-root migration
// still carries, and it is an error outside a checkout.
func resolvePurgeSlugs(target string) ([]string, string, error) {
	if target != "" {
		slug, agent, ok := naming.ParseProjectTarget(target)
		if !ok {
			return nil, "", fmt.Errorf("invalid purge target %q: use a project slug or <slug>-<agent>", target)
		}
		return []string{slug}, agent, nil
	}
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		return nil, "", fmt.Errorf("resolve project identity: %w", err)
	}
	if identity.WorktreeRoot == "" {
		return nil, "", errors.New("not inside a checkout: pass an explicit project slug to purge")
	}
	return append([]string{identity.CheckoutSlug}, identity.LegacyRepoSlugs...), "", nil
}

// retiredProjectSlugs returns the slugs the artifact union still observes and
// the age threshold retires, ordered by slug. It returns an empty slice (never
// nil) alongside an error, so the caller's retired_projects JSON always marshals
// as an array.
func retiredProjectSlugs(ctx context.Context, age time.Duration) ([]string, error) {
	candidates, err := pruning.EnumerateCandidates(ctx, age)
	if err != nil {
		return []string{}, err
	}
	now := time.Now()
	retired := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Retired(now, age) {
			retired = append(retired, candidate.Slug)
		}
	}
	return retired, nil
}
