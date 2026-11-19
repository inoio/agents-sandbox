package firstrun

import (
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// firstRunConsent is the one consent sentence the hub puts above the menu. The
// tool is the subject; the object of "learn" is the setup, never the user.
const firstRunConsent = "Let agents-sandbox learn from this session: set up your providers here, " +
	"use the agent normally, and when you exit it offers to save what it needed."

// firstRunQuestion heads the menu choices.
const firstRunQuestion = "How would you like to set up this checkout?"

// Outcome is the resolved first-run choice applied to the run options.
type Outcome struct {
	Choice              Choice
	Learn               bool // the session starts in learn mode (UC1/UC4)
	ProvisionHostConfig bool // "Configure it myself" set the unsafe opt-in

	// sourceHash is the host-config hash the decision is recorded against, so a
	// later host-config change re-arms the menu. Prompt fills it from Facts; it
	// is unexported because callers only need Apply to persist it.
	sourceHash string
}

// Prompt asks the first-run question and returns the outcome. offered is false
// when the run is non-interactive or Offer(f) is false; a non-interactive run
// warns and returns the zero Outcome (invariant 6: never hang).
func Prompt(ui termio.UI, f Facts) (Outcome, bool) {
	var noOutcome Outcome
	if !Offer(f) {
		return noOutcome, false
	}
	if ui == nil {
		return noOutcome, false
	}
	if !ui.IsInteractive() {
		ui.Warn("first-run setup needs an interactive terminal; skipping the setup menu")
		return noOutcome, false
	}

	ui.Info(firstRunConsent)
	key, err := ui.Select(firstRunQuestion, firstRunChoices(f), firstRunDefault(f))
	if err != nil {
		ui.Warnf("first-run setup: %v", err)
		return noOutcome, false
	}
	return outcomeFor(key, f.HostSourceHash), true
}

// Apply sets the outcome's effect on the run options and records the decision.
// A learning choice records FirstRunLearned; the manual choice records
// FirstRunManual. The recorded source hash re-arms the menu on a host-config
// change.
func (o Outcome) Apply(opts *options.RunOptions, slug, agentName string) error {
	opts.Learn = o.Learn
	choice := state.FirstRunManual
	if o.Learn {
		choice = state.FirstRunLearned
	}
	return state.RecordFirstRun(slug, agentName, state.FirstRunRecord{
		Choice:     choice,
		SourceHash: o.sourceHash,
	})
}

// firstRunChoices builds the menu: the host seed is offered only when the host
// has agent config (Design decision 8); the manual path is always last.
func firstRunChoices(f Facts) []termio.Choice {
	choices := make([]termio.Choice, 0, 3)
	choices = append(choices, termio.Choice{
		Key:         string(ChoiceLearnByUsing),
		Label:       "Learn by using it",
		Description: "sign in and use the agent; save what it needed when you exit",
	})
	if len(f.HostFiles) > 0 {
		choices = append(choices, termio.Choice{
			Key:         string(ChoiceLearnFromHost),
			Label:       "Learn from my host setup",
			Description: "recommended: seed from your existing host config first",
		})
	}
	choices = append(choices, termio.Choice{
		Key:         string(ChoiceManual),
		Label:       "Configure it myself",
		Description: "configure the checkout by hand",
	})
	return choices
}

// firstRunDefault recommends the host seed when the host has config, else the
// unseeded learning session (the hub's "recommended in that case").
func firstRunDefault(f Facts) string {
	if len(f.HostFiles) > 0 {
		return string(ChoiceLearnFromHost)
	}
	return string(ChoiceLearnByUsing)
}

// outcomeFor maps a selected menu key to its outcome, carrying the source hash
// so Apply records it.
func outcomeFor(key, sourceHash string) Outcome {
	switch Choice(key) {
	case ChoiceLearnFromHost:
		return Outcome{Choice: ChoiceLearnFromHost, Learn: true, ProvisionHostConfig: false, sourceHash: sourceHash}
	case ChoiceManual:
		return Outcome{Choice: ChoiceManual, Learn: false, ProvisionHostConfig: false, sourceHash: sourceHash}
	case ChoiceLearnByUsing:
		return Outcome{Choice: ChoiceLearnByUsing, Learn: true, ProvisionHostConfig: false, sourceHash: sourceHash}
	}
	// Select only returns a listed key; an unexpected key falls back to the
	// unseeded learning session rather than dropping the first run.
	return Outcome{Choice: ChoiceLearnByUsing, Learn: true, ProvisionHostConfig: false, sourceHash: sourceHash}
}
