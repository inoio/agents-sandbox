package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/cliout"
	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// confirmPrune gates a destructive prune behind confirmation. A dry-run only
// previews, so it is authorized without prompting. The global --yes flag
// authorizes non-interactively through ui.Confirm's assume-yes path; a
// non-interactive UI without --yes fails fast via ui.Confirm rather than
// blocking.
func confirmPrune(ui termio.UI, dryRun bool) error {
	if dryRun {
		return nil
	}
	ok, err := ui.Confirm("Prune stale runtime objects?", false)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("prune cancelled")
	}
	return nil
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
	return pruneErr
}

func resolvePruneFlags(cmd *cobra.Command) (time.Duration, bool, error) {
	age, err := resolvePruneAge(cmd)
	if err != nil {
		return 0 * time.Second, false, err
	}
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	return age, dryRun, nil
}

func resolvePruneOutputMode(cmd *cobra.Command, ui termio.UI) (cliout.Mode, error) {
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
