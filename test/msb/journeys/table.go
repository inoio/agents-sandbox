package journeys

// Journeys is the journey table: one entry per scenario, each run once per
// harness it lists. Every registered agent a journey does not drive carries a
// recorded exemption for that journey (Exemptions).
var Journeys = []Journey{
	uc1Journey,
	uc4Journey,
	configLearnJourney,
	attributionJourney,
	coreSessionJourney,
}

// learnSliceHarnesses are the harnesses the learn-mode journeys extend to in
// the next slice (#852); learnRolloutHarnesses follow in the full rollout
// (#794). The lists are explicit, so a newly registered agent is in neither and
// fails the completeness contract until it is placed.
var (
	learnSliceHarnesses   = []string{"claude-code", "codex", "oh-my-pi", "pi"}
	learnRolloutHarnesses = []string{
		"goose",
		"grok-build",
		"hermes",
		"kimi-code",
		"mistral-vibe",
		"opencode",
		"qwen-code",
	}
)

// learnExemptions records why a learn-mode journey does not yet run on a
// harness: the slice that will cover it.
func learnExemptions() map[string]string {
	reasons := map[string]string{}
	for _, name := range learnSliceHarnesses {
		reasons[name] = "learn mode on this harness lands with #852"
	}
	for _, name := range learnRolloutHarnesses {
		reasons[name] = "learn mode on this harness lands with the full rollout, #794"
	}
	return reasons
}

// coreSessionExemptions records why the core-session journey does not yet run
// on a harness: the rest follow in #794.
func coreSessionExemptions() map[string]string {
	reasons := map[string]string{}
	for _, name := range learnRolloutHarnesses {
		reasons[name] = "core session on this harness lands with the full rollout, #794"
	}
	return reasons
}

// Exemptions records, per journey, why a registered harness is not driven by
// it. The completeness contract requires every (journey, registered agent) pair
// to be covered or exempted with a non-empty reason, so a newly registered
// harness fails the default suite until it is placed.
var Exemptions = map[string]map[string]string{
	uc1Journey.Name:         learnExemptions(),
	uc4Journey.Name:         learnExemptions(),
	configLearnJourney.Name: learnExemptions(),
	attributionJourney.Name: learnExemptions(),
	coreSessionJourney.Name: coreSessionExemptions(),
}
