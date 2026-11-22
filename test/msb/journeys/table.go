package journeys

// Journeys is the journey table: one entry per scenario the suite drives. v1
// covers opencode2 only, with the learn-mode journeys (UC1, UC4, `config learn`,
// attribution) landing across Phase 2; a journey is added here as a literal once
// its steps exist, and every registered agent it does not yet drive carries a
// recorded exemption below.
var Journeys = []Journey{
	uc1Journey,
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
