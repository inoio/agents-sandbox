package journeys

// opencode2AgentName is the agent the v1 learn-mode journeys drive: the
// S1-verified profile. It is a named constant so the journey table and each
// journey literal cannot drift apart.
const opencode2AgentName = "opencode2"

// opencodeHostConfigRelPath is the agent's global config file the journeys seed
// into the isolated HOME as host config. It is opencode's higher-precedence
// .jsonc, so the host seed is what the agent reads over the stub's provisioned
// .json; both carry the same provider pointing at the stub. It is shared by
// every journey that seeds a host config (UC4 and attribution), so a path change
// updates both seeds together.
const opencodeHostConfigRelPath = ".config/opencode/opencode.jsonc"

// Journeys is the journey table: one entry per scenario the suite drives. v1
// covers opencode2 only, with the learn-mode journeys (UC1, UC4, `config learn`,
// attribution) landing across Phase 2; a journey is added here as a literal once
// its steps exist, and every registered agent it does not yet drive carries a
// recorded exemption below. An agent may be driven by more than one journey (UC1
// and UC4 both drive opencode2); the completeness contract only rejects an agent
// that is neither covered nor exempted.
var Journeys = []Journey{
	uc1Journey,
	uc4Journey,
	configLearnJourney,
	attributionJourney,
}

// notCoveredInV1 is the recorded reason for every registered agent the journeys
// suite does not yet drive. The journey spec is a table field, so covering one
// more harness is one entry plus its steps; full-harness coverage is tracked by
// the follow-up issue.
const notCoveredInV1 = "Not covered in v1: the journeys suite drives learn mode on opencode2 only, " +
	"and the journey spec is a table field, so extending coverage is one entry. " +
	"Tracked by the follow-up issue for full-harness coverage."

// Exemptions records, per registered agent not yet covered by a journey, why it
// is not. The table-completeness test requires every agent.Names() entry to be
// either covered by a journey or listed here with a non-empty reason, so a newly
// registered harness fails the default suite until it is covered or exempted
// (mirroring the startup-smoke completeness contract).
var Exemptions = map[string]string{
	"claude-code":  notCoveredInV1,
	"codex":        notCoveredInV1,
	"goose":        notCoveredInV1,
	"grok-build":   notCoveredInV1,
	"hermes":       notCoveredInV1,
	"kimi-code":    notCoveredInV1,
	"mistral-vibe": notCoveredInV1,
	"oh-my-pi":     notCoveredInV1,
	"opencode":     notCoveredInV1,
	"pi":           notCoveredInV1,
	"qwen-code":    notCoveredInV1,
}
