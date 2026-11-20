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

// manualProvisionWarning is the one confirmation the manual path asks before it
// turns on host-config provisioning: that path copies host credentials into the
// sandbox, so the unsafe opt-in is only set on an explicit yes.
const manualProvisionWarning = "Provisioning copies your host agent config, including credentials, into the sandbox. Continue?"

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
	if !Offer(f) {
		return Outcome{}, false
	}
	if ui == nil {
		return Outcome{}, false
	}
	if !ui.IsInteractive() {
		ui.Warn("first-run setup needs an interactive terminal; skipping the setup menu")
		return Outcome{}, false
	}

	ui.Info(firstRunConsent)
	key, err := ui.Select(firstRunQuestion, firstRunChoices(f), firstRunDefault(f))
	if err != nil {
		ui.Warnf("first-run setup: %v", err)
		return Outcome{}, false
	}
	provisionConfirmed := false
	if Choice(key) == ChoiceManual {
		provisionConfirmed, err = ui.Confirm(manualProvisionWarning, false)
		if err != nil {
			ui.Warnf("first-run setup: %v", err)
			return Outcome{}, false
		}
	}
	return outcomeFor(key, f.HostSourceHash, provisionConfirmed), true
}

// Apply sets the outcome's effect on the run options and records the decision.
// A learning choice records FirstRunLearned and leaves the config-resolved
// provisioning setting untouched (a learning session applies nothing); the
// manual choice records FirstRunManual and carries the warning's opt-in onto
// opts. The recorded source hash re-arms the menu on a host-config change.
func (o Outcome) Apply(opts *options.RunOptions, slug, agentName string) error {
	opts.Learn = o.Learn
	choice := state.FirstRunManual
	if o.Learn {
		choice = state.FirstRunLearned
	}
	if o.Choice == ChoiceManual {
		provisionHostConfig := o.ProvisionHostConfig
		opts.ProvisionHostConfig = &provisionHostConfig
	}
	return state.RecordFirstRun(slug, agentName, state.FirstRunRecord{
		Choice:     choice,
		SourceHash: o.sourceHash,
	})
}

// firstRunChoices builds the menu: the host seed is offered only when the host
// has agent config (Design decision 8); the manual path is always last.
func firstRunChoices(f Facts) []termio.Choice {
	var choices []termio.Choice
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
// so Apply records it. provisionConfirmed is the manual path's warning answer,
// so the manual outcome carries the opt-in only when the user confirmed it. The
// "Learn by using it" case is named to satisfy the exhaustive switch and returns
// the same unseeded learning outcome as the fallback, so Select only ever
// produces one of these anyway; an unexpected key must not drop the first run.
func outcomeFor(key, sourceHash string, provisionConfirmed bool) Outcome {
	switch Choice(key) {
	case ChoiceLearnFromHost:
		return Outcome{Choice: ChoiceLearnFromHost, Learn: true, ProvisionHostConfig: false, sourceHash: sourceHash}
	case ChoiceManual:
		return Outcome{
			Choice:              ChoiceManual,
			Learn:               false,
			ProvisionHostConfig: provisionConfirmed,
			sourceHash:          sourceHash,
		}
	case ChoiceLearnByUsing:
		// gomutants:disable-next-line BRANCH_CASE reason="the unseeded learning outcome below is identical, so removing this case falls through to the same value"
		return Outcome{Choice: ChoiceLearnByUsing, Learn: true, ProvisionHostConfig: false, sourceHash: sourceHash}
	}
	// An unexpected key falls back to the unseeded learning session rather than
	// dropping the first run.
	return Outcome{Choice: ChoiceLearnByUsing, Learn: true, ProvisionHostConfig: false, sourceHash: sourceHash}
}
