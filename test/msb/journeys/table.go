package journeys

import "slices"

// Journeys is the journey table: one entry per scenario, each run once per
// harness it lists. Every registered agent a journey does not drive carries a
// recorded exemption for that journey (Exemptions).
var Journeys = []Journey{
	uc1Journey,
	uc4Journey,
	configLearnJourney,
	attributionJourney,
	coreSessionJourney,
	proxySpikeJourney,
}

// learnSliceHarnesses are the harnesses of the #852 slice; learnRolloutHarnesses
// follow in the full rollout (#794). The lists are explicit, so a newly
// registered agent is in neither and fails the completeness contract until it
// is placed.
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

// learnBlockedHarnesses records why a slice harness is not driven by the
// learn-mode journeys yet: in each, the read-back finds no host-scoped
// credential to learn, so every learn journey's credential assertion would
// fail on what the product does.
var learnBlockedHarnesses = map[string]string{
	"claude-code": "its read-back reads only the OAuth login store; settings.json, where an endpoint and key live, " +
		"is neither read back nor seeded until #821",
	"codex": "its read-back reads only the auth.json login store, whose value is never read, and not " +
		"config.toml, so no host-scoped credential is learned (#873)",
	"pi": "its read-back withholds models.json, where a custom provider's key lives, until provider headers " +
		"are classified (#820, #849)",
}

// learnExemptions records why a learn-mode journey does not run on a harness:
// the blocker for a slice harness, or the rollout that will cover it.
func learnExemptions() map[string]string {
	reasons := map[string]string{}
	for _, name := range learnSliceHarnesses {
		if blocker, blocked := learnBlockedHarnesses[name]; blocked {
			reasons[name] = "learn mode on this harness waits for the product: " + blocker
		}
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

// proxySpikeExemptions records why the proxy spike probes opencode2 only: it is
// a route question, not a harness one.
func proxySpikeExemptions() map[string]string {
	reasons := map[string]string{}
	for _, name := range slices.Concat(learnSliceHarnesses, learnRolloutHarnesses) {
		reasons[name] = "the proxy spike asks whether a stub route passes the learn proxy, which one harness answers (#793)"
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
	proxySpikeJourney.Name:  proxySpikeExemptions(),
}
