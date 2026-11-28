package journeys

const secretSpikeEnv = "AGENTS_SANDBOX_JOURNEYS_SECRET_SPIKE"

const secretSpikeReason = "the host-gateway HTTPS route completes turns but delivers the unchanged secret placeholder; see #794"

// secretSpikeJourney tests the host-gateway route before promotion check B can
// become a core-session assertion. It needs its own VM because secrets and TLS
// interception are creation-time settings.
var secretSpikeJourney = Journey{
	Name:      "secret-substitution-spike",
	Harnesses: []string{opencode2AgentName},
	Network:   hostGatewayNetwork,
	Steps: []Step{
		Given(StepRequireOptIn(secretSpikeEnv, secretSpikeReason)),
		And(StepOwnProject("secret")),
		And(StepFreshHome()),
		And(StepSeedSecretStub()),
		And(StepConfigureLauncherEnv()),
		When(StepLaunch("run")),
		Then(StepWaitReady()),
		And(StepAssertGuestHasOnlyPlaceholder()),
		When(StepSendPrompt("journeys secret substitution turn")),
		Then(StepAssertStubServed()),
		And(StepAssertReplyShown()),
		And(StepAssertSecretSubstituted()),
		When(StepQuitToExit()),
	},
}
