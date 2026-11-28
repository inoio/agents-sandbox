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

// PreserveForReplay stops an active channel and saves its observation for
// config learn without running the interactive after-run. It is used when the
// owning run context was cancelled after the baseline had been recorded.
func (r *Run) PreserveForReplay(ui termio.Logger) error {
	observation := r.Stop()
	PrintProxyDiagnostics(ui, observation)
	if r.Passive() || !r.sessionRecorded || r.record.ArtefactPath != "" {
		return nil
	}

	path, err := saveArtefact(r.key.Slug, r.startedAt.Unix(), observation)
	if err != nil {
		return fmt.Errorf("save the learn capture for replay: %w", err)
	}
	r.record.ArtefactPath = path
	if err := recordLearnSession(r.key, r.record); err != nil {
		r.record.ArtefactPath = ""
		return fmt.Errorf("record the learn capture path for replay: %w", err)
	}
	return nil
}

// Finish tears the channel down, saves the artefact, runs the analysis in this
// process, prints the summary, and clears the session record. A passive Run
// saves no artefact.
func (r *Run) Finish(ctx context.Context, in FinishInput) error {
	observation := r.Stop()
	PrintProxyDiagnostics(in.UI, observation)

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
// as a separate note, and the report-only findings (PrintReported).
func printSummary(ui termio.UI, summary Summary) {
	ui.Infof(
		"agents-sandbox learned: %d services reached, %d keys used.",
		summary.ServicesReached,
		summary.KeysUsed,
	)
	if len(summary.Denied) > 0 {
		ui.Infof("Denied destinations (never auto-allowed): %s", strings.Join(summary.Denied, ", "))
	}
	PrintReported(ui, summary.Results)
}

// PrintProxyDiagnostics reports observation failures before later analysis or
// capture storage can fail. The observation records only a value-free log path
// and destination/handshake facts.
func PrintProxyDiagnostics(ui termio.Logger, observation channel.Observation) {
	if ui == nil {
		return
	}
	handshakeHosts := map[string]bool{}
	for _, destination := range observation.Destinations {
		if destination.HandshakeFailed() && destination.Host != "" {
			handshakeHosts[destination.Host] = true
		}
	}
	if hosts := sortedKeys(handshakeHosts); len(hosts) > 0 {
		ui.Warnf(
			"TLS interception failed for %s; successful use was not verified. Possible causes include certificate pinning or missing sandbox CA trust.",
			strings.Join(hosts, ", "),
		)
	}
	if observation.UnattributedHandshakes > 0 {
		ui.Warnf(
			"%d TLS handshake failure(s) could not be tied to a destination without guessing.",
			observation.UnattributedHandshakes,
		)
	}
	if observation.ProxyWarnings == 0 {
		return
	}
	if observation.ProxyLog == "" {
		ui.Warnf("The learn proxy reported %d warning(s), but its log could not be created.", observation.ProxyWarnings)
		return
	}
	status := ""
	switch {
	case observation.ProxyLogWriteFailed && observation.ProxyLogTruncated:
		status = " (log is incomplete and truncated)"
	case observation.ProxyLogWriteFailed:
		status = " (log may be incomplete)"
	case observation.ProxyLogTruncated:
		status = " (log was truncated)"
	}
	ui.Warnf("The learn proxy logged %d warning(s): %s%s", observation.ProxyWarnings, observation.ProxyLog, status)
}

// PrintReported warns with each report-only finding's evidence and guidance.
// The applier asks nothing for such a finding, so every caller that applies
// findings prints them: the live after-run and the config learn replay.
func PrintReported(ui termio.Logger, results []analyser.ApplyResult) {
	for _, result := range results {
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
