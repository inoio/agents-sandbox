package learn

import (
	"context"
	"fmt"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/analyser"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// The exit prompt's three choices: review each proposal, save every proposal,
// or write nothing.
const (
	exitReview  = "r"
	exitSaveAll = "s"
	exitNotNow  = "n"
)

// saveArtefact persists a scrubbed observation. It is a seam so a test can drive
// Finish's save failure branch.
var saveArtefact = channel.Save

// exitPrompt is the question the launcher asks at teardown.
const exitPrompt = "Save what agents-sandbox learned from this session?"

// exitChoices lists the choices, Not now default and last.
func exitChoices() []termio.Choice {
	return []termio.Choice{
		{Key: exitReview, Label: "Review and save", Description: "review each proposal"},
		{Key: exitSaveAll, Label: "Save all", Description: "save every proposal without asking"},
		{Key: exitNotNow, Label: "Not now", Description: "write nothing"},
	}
}

// interactive reports whether ui can answer a prompt.
func interactive(ui termio.UI) bool { return ui != nil && ui.IsInteractive() }

// chosenExit returns the user's choice, or Not now on a non-interactive terminal
// (which then lets the applier print its one-line notice). The prompt follows
// the agent's full-screen session, so input typed for it and not consumed — a
// surplus Ctrl-D after the one that quit it, read as end of input — is dropped
// first.
func chosenExit(ui termio.UI) (string, error) {
	if !interactive(ui) {
		return exitNotNow, nil
	}
	ui.DiscardTypeAhead()
	return ui.Select(exitPrompt, exitChoices(), exitNotNow)
}

// runExitPrompt resolves the exit choice and drives the analysis. Not now on an
// interactive terminal writes nothing and records nothing; a non-interactive
// terminal falls through to the applier, which prints its one-line notice. Every
// other choice runs the analysis, with AcceptAll set for Save all.
func runExitPrompt(
	ctx context.Context,
	a Analysis,
	ui termio.UI,
	mode options.Mode,
	setByFlag func(string) bool,
) (Summary, error) {
	choice, err := chosenExit(ui)
	if err != nil {
		return Summary{}, fmt.Errorf("ask how to save the learned config: %w", err)
	}
	if choice == exitNotNow && interactive(ui) {
		return summarize(a, nil), nil
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	return analyse(a, ui, mode, setByFlag, choice == exitSaveAll)
}

// FinishInput is what the live after-run needs beyond the Run itself.
type FinishInput struct {
	Key       state.Key
	Slug      string // repo slug
	Agent     string
	Paths     configpaths.ConfigPaths
	Home      string
	Base      network.Policy // pre-overlay policy
	UI        termio.UI
	Mode      options.Mode
	SetByFlag func(string) bool
}

// Finish tears the channel down, saves the artefact, runs the analysis in this
// process, prints the summary, and clears the session record. A passive Run
// saves no artefact.
func (r *Run) Finish(ctx context.Context, in FinishInput) error {
	observation := r.Stop()

	artefactPath := ""
	if !r.Passive() {
		path, err := saveArtefact(in.Key.Slug, r.startedAt.Unix(), observation)
		if err != nil {
			return fmt.Errorf("save the learn capture: %w", err)
		}
		artefactPath = path
		// Record the path so config learn can load the capture if this analysis
		// does not finish (a crash or a review that never resolves).
		r.record.ArtefactPath = path
		if err := recordLearnSession(in.Key, r.record); err != nil {
			return fmt.Errorf("record the learn capture path: %w", err)
		}
	}

	//exhaustruct:ignore // ReadBack is filled by the analysis from the home volume
	analysis := Analysis{
		Key:          in.Key,
		Slug:         in.Slug,
		CheckoutSlug: in.Key.Slug,
		Agent:        in.Agent,
		Paths:        in.Paths,
		Home:         in.Home,
		Baseline:     r.baseline,
		Observation:  observation,
		Current:      in.Base,
	}

	summary, err := runExitPrompt(ctx, analysis, in.UI, in.Mode, in.SetByFlag)
	if err != nil {
		return err
	}

	if err := state.ClearLearnSession(in.Key); err != nil {
		in.UI.Warnf("learn mode: clearing the session record failed: %v", err)
	}
	if artefactPath != "" {
		if err := channel.Delete(artefactPath); err != nil {
			in.UI.Warnf("learn mode: deleting the capture failed: %v", err)
		}
	}
	printSummary(in.UI, summary)
	return nil
}

// printSummary reports what the after-run learned, with the denied destinations
// as a separate note, and warns with each report-only finding's guidance: the
// applier asks nothing for such a finding, so the summary is where it shows.
func printSummary(ui termio.UI, summary Summary) {
	ui.Infof(
		"agents-sandbox learned: %d services reached, %d keys used.",
		summary.ServicesReached,
		summary.KeysUsed,
	)
	if len(summary.Denied) > 0 {
		ui.Infof("Denied destinations (never auto-allowed): %s", strings.Join(summary.Denied, ", "))
	}
	for _, result := range summary.Results {
		if result.Outcome != analyser.OutcomeNotApplicable {
			continue
		}
		ui.Warnf("%s", reportedFindingText(result.Finding))
	}
}

// reportedFindingText joins a report-only finding's evidence summary and its
// writes' guidance into one warning.
func reportedFindingText(finding analyser.Finding) string {
	lines := []string{finding.Evidence.Summary}
	for _, write := range finding.Writes {
		if write.Text != "" {
			lines = append(lines, write.Text)
		}
	}
	return strings.Join(lines, " ")
}
