package journeys

// proxySpikeEnv opts into the proxy-observation spike.
const proxySpikeEnv = "AGENTS_SANDBOX_JOURNEYS_PROXY_SPIKE"

// proxySpikeReason is why the spike is opt-in.
const proxySpikeReason = "no stub route both completes a turn and passes the learn observation proxy yet " +
	"(the host gateway bypasses the proxy, an unresolvable name is denied before it); see #793"

// proxySpikeJourney is the open question of the attribution half: a learning
// session's turn to the host-side stub reaches the learn observation proxy, so
// the saved observation names the stub's host. It is opt-in until a route
// exists (proxySpikeReason); a denied proxy dial still counts as observed.
var proxySpikeJourney = Journey{
	Name:      "proxy-observes-the-stub",
	Harnesses: []string{opencode2AgentName},
	Steps: []Step{
		Given(StepRequireOptIn(proxySpikeEnv, proxySpikeReason)),
		And(StepFreshHome()),
		And(StepSeedStub()),
		When(StepLaunch("run", "--learn")),
		Then(StepWaitReady()),
		When(StepSendPrompt(stubSpikePrompt)),
		And(StepQuit()),
		Then(StepAssertStubObserved()),
		When(StepChooseExit(exitSaveAllKey)),
	},
}

// stubSpikePrompt is the one message the spike sends; a completed model turn is
// not required, only that the agent issues a request to the stub host.
const stubSpikePrompt = "hello"
