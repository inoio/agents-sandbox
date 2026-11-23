package journeys

import "github.com/inoio/agents-sandbox/internal/firstrun"

// stubTurnPrompt is the message the stub-turn journey sends: distinctive, so its
// echo cannot be matched by a repaint.
const stubTurnPrompt = "journeys stub turn"

// hostGatewayNetwork is the network profile that admits the host gateway the
// stub's default route names.
const hostGatewayNetwork = "host"

// stubTurnJourney drives a plain run against the host-side stub over the
// default host-gateway route and asserts the turn completes: the stub served a
// main-model chat completion and the harness shows its reply. It runs on a
// project of its own, because the shared project's VM may still run with
// another journey's network policy.
var stubTurnJourney = Journey{
	Name:      "stub-turn-completes",
	Harnesses: []string{opencode2AgentName},
	Network:   hostGatewayNetwork,
	Steps: []Step{
		Given(StepOwnProject("turn")),
		And(StepFreshHome()),
		And(StepSeedStub()),
		When(StepLaunch("run")),
		And(StepChooseFirstRun(firstrun.ChoiceManual)),
		And(StepDeclineHostProvisioning()),
		Then(StepWaitReady()),
		When(StepSendPrompt(stubTurnPrompt)),
		Then(StepAssertStubServed()),
		And(StepAssertReplyShown()),
		When(StepQuitToExit()),
	},
}
