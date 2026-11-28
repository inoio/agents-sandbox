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
	loginJourney,
	mcpLoginJourney,
	proxySpikeJourney,
	exposedLoginKeyJourney,
	secretSpikeJourney,
}

// placedHarnesses lists every registered agent the journey table has placed.
// It is explicit, so a newly registered agent is in no journey's exemptions
// and fails the completeness contract until it is listed here.
var placedHarnesses = []string{
	"claude-code",
	"codex",
	"goose",
	"grok-build",
	"hermes",
	"kimi-code",
	"mistral-vibe",
	"oh-my-pi",
	"opencode",
	"opencode2",
	"pi",
	"qwen-code",
}

// exemptionsFor records why journey does not drive each placed harness it
// leaves out: the harness's own reason where one is given, else fallback.
func exemptionsFor(journey Journey, fallback string, reasons map[string]string) map[string]string {
	exemptions := map[string]string{}
	for _, name := range placedHarnesses {
		if slices.Contains(journey.Harnesses, name) {
			continue
		}
		if reason, ok := reasons[name]; ok {
			exemptions[name] = reason
			continue
		}
		exemptions[name] = fallback
	}
	return exemptions
}

// learnBlockers records why a harness is not driven by the learn-mode
// journeys although the slice (#852) covered it: in each, the read-back finds
// no host-scoped credential to learn, so every learn journey's credential
// assertion would fail on what the product does.
var learnBlockers = map[string]string{
	"claude-code": "learn mode on this harness waits for the product: its read-back reads only the OAuth login " +
		"store; settings.json, where an endpoint and key live, is neither read back nor seeded until #821",
	"codex": "learn mode on this harness waits for the product: its read-back reads only the auth.json login " +
		"store, whose value is never read, and not config.toml, whose providers hold no key value, so no " +
		"host-scoped credential is learned (#873)",
	"pi": "learn mode on this harness waits for the product: its read-back withholds models.json, where a " +
		"custom provider's key lives, until provider headers are classified (#820, #849)",
}

// The fallback reasons per journey family.
const (
	rolloutLearnReason     = "learn mode on this harness lands with the full rollout, #794"
	rolloutCoreReason      = "core session on this harness lands with the full rollout, #794"
	loginReason            = "the login journey starts with codex; other harnesses follow (#875)"
	mcpLoginReason         = "the MCP sign-in journey covers this harness once its MCP facts are recorded (#875)"
	exposedLoginKeyReason  = "learn mode inspects only codex's and claude-code's login stores for a plain-text API key (#873, #892)"
	proxySpikeExemptReason = "the proxy spike asks whether a stub route passes the learn proxy, which one harness answers (#793)"
)

// Exemptions records, per journey, why a registered harness is not driven by
// it. The completeness contract requires every (journey, registered agent) pair
// to be covered or exempted with a non-empty reason, so a newly registered
// harness fails the default suite until it is placed.
var Exemptions = map[string]map[string]string{
	uc1Journey.Name:         exemptionsFor(uc1Journey, rolloutLearnReason, learnBlockers),
	uc4Journey.Name:         exemptionsFor(uc4Journey, rolloutLearnReason, learnBlockers),
	configLearnJourney.Name: exemptionsFor(configLearnJourney, rolloutLearnReason, learnBlockers),
	attributionJourney.Name: exemptionsFor(attributionJourney, rolloutLearnReason, learnBlockers),
	coreSessionJourney.Name: exemptionsFor(coreSessionJourney, rolloutCoreReason, nil),
	loginJourney.Name:       exemptionsFor(loginJourney, loginReason, nil),
	mcpLoginJourney.Name:    exemptionsFor(mcpLoginJourney, mcpLoginReason, nil),
	proxySpikeJourney.Name:  exemptionsFor(proxySpikeJourney, proxySpikeExemptReason, nil),
	secretSpikeJourney.Name: exemptionsFor(
		secretSpikeJourney,
		"the secret-substitution route spike starts with opencode2 (#794)",
		nil,
	),
	exposedLoginKeyJourney.Name: exemptionsFor(
		exposedLoginKeyJourney, exposedLoginKeyReason, nil,
	),
}
