package configwizard

import (
	"github.com/inoio/agents-sandbox/internal/configfile"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const userConfigFileMode = 0o600

type Selection struct {
	FirstRun bool
	Tier     int
}

func Run(scope ScopeResolver, ui termio.UI, sel Selection) error {
	if !ui.IsInteractive() {
		return nil
	}
	state := LoadState()
	if sel.FirstRun && state.FirstRunDone {
		return nil
	}
	path, data, err := configfile.LoadDir(configpaths.Get().UserConfigDir())
	if err != nil {
		return err
	}
	cfg, err := configfile.Parse(path, data)
	if err != nil {
		return err
	}
	askedBefore := len(state.Asked)
	wizard := &runner{
		questions: DefaultCatalog(),
		tiers:     selectedTiers(sel),
		firstRun:  sel.FirstRun,
		scope:     scope,
		state:     state,
		ui:        ui,
		config:    cfg,
		changed:   false,
	}
	if _, err := wizard.run(); err != nil {
		return err
	}
	if wizard.tiers[TierEssentials] {
		wizard.state.FirstRunDone = true
	}
	if err := writeAnswers(path, wizard); err != nil {
		return err
	}
	if len(wizard.state.Asked) != askedBefore || wizard.state.FirstRunDone != state.FirstRunDone {
		return SaveState(wizard.state)
	}
	return nil
}

func selectedTiers(sel Selection) map[int]bool {
	if sel.Tier > 0 {
		return map[int]bool{sel.Tier: true}
	}
	tiers := map[int]bool{
		TierEssentials: true,
		TierResources:  true,
		TierRuntime:    true,
	}
	if !sel.FirstRun {
		tiers[TierIntegrations] = true
	}
	return tiers
}

func writeAnswers(path string, wizard *runner) error {
	if !wizard.Changed() {
		return nil
	}
	data, err := wizard.config.Marshal()
	if err != nil {
		return err
	}
	if err := configfile.WriteAtomic(path, data, userConfigFileMode); err != nil {
		return err
	}
	wizard.ui.Infof("Wrote %d setting(s) to %s", len(wizard.state.Asked), path)
	return nil
}
