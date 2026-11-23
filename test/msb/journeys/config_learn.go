package journeys

import "github.com/inoio/agents-sandbox/internal/agent"

// configLearnScope is what the `config learn` replay asserts it wrote to the
// per-slug user scope: the stub provider's inline apiKey is a static credential,
// saved to env.secret.yaml scoped to the stub host, and the managed agent config
// carries the ${STUB_API_KEY} placeholder.
//
// The placeholder holds here even though UC1 cannot assert one. UC1's live path
// captures its baseline after the launcher provisions the stub config, so the
// read-back never reports that file as changed and the write-back emits nothing.
// The replay's baseline is whatever StepSeedLearnSession recorded, and the
// journey records none: an empty baseline makes every read-back path "changed",
// so the provisioned stub config reaches ManagedConfigFindings and is written
// back sanitized. That is the same placeholder effect UC4 asserts for its host
// seed, produced through the replay's changed-config path instead.
var configLearnScope = ScopeWant{ManagedConfig: false, Placeholder: true}

// configLearnJourney is the `config learn` journey: it replays a recorded
// learning session whose owning process is gone.
//
// There is no `volume create`, and `volume reset`/`migrate`/`edit` all replace an
// existing home volume, so the only way to obtain the real home volume the
// replay resolves (and the state record naming it) is a launcher run. The
// journey therefore begins with a preliminary `run --learn` that reaches the
// TUI, quits, and answers the exit prompt with "Not now": that run creates the
// VM and home volume, provisions the stub provider into the home (where it
// persists in the volume for the replay's read-back), writes nothing, and clears
// its own session record — leaving the home volume in state for
// StepSeedLearnSession to copy.
//
// The command resolves its agent with no --agent flag, so it targets
// agent.DefaultAgentName; StepLaunchCommand guards that the journey drives the
// same agent.
var configLearnJourney = Journey{
	Name:      "config-learn-replay",
	Harnesses: []string{agent.DefaultAgentName},
	LearnMode: true,
	Steps: []Step{
		Given(StepFreshHome()),
		And(StepSeedStub()),
		And(StepLaunch("run", "--learn")),
		And(StepWaitReady()),
		And(StepQuit()),
		And(StepChooseExit(exitNotNowKey)),
		And(StepSeedLearnSession()),
		When(StepLaunchCommand("config", "learn")),
		And(StepAnswerApplyPrompts()),
		Then(StepAssertScopeWritten(configLearnScope)),
		And(StepAssertLearnArtefactGone()),
		And(StepAssertSessionCleared()),
	},
}
