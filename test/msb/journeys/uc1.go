package journeys

import "github.com/inoio/agents-sandbox/internal/firstrun"

// uc1Prompt is the one message UC1 sends. A completed model turn is not
// required: the journey asserts what the read-back found in the agent's home,
// not the model's answer, so the prompt only has to make the agent issue a
// request at the stub provider's baseURL.
const uc1Prompt = "hello"

// stubHost is the host of the stub provider's advertised origin, the host a
// journey's credential is scoped to in env.secret.yaml. It is derived at start
// from the same override StartStubProvider reads, so a host accept that pins
// AGENTS_SANDBOX_JOURNEYS_STUB_ORIGIN keeps the assertion and the written host
// in agreement.
var stubHost = stubAdvertisedHost()

// uc1Scope is what UC1 asserts the run wrote to the per-slug user scope: the
// stub provider's inline apiKey is classified as a static credential and scoped
// to the stub host in env.secret.yaml.
//
// There is deliberately no managed-config assertion (no ConfigKeys, no
// Placeholder). analyser.ManagedConfigFindings writes back only a config file
// the session *changed* since the baseline manifest, but the stub provider
// config is provisioned through the launcher's `home:` manifest during
// prepareSandbox, before CaptureBaseline (internal/sandbox/session/run.go), so
// the read-back never reports it as changed and no managed config — and so no
// ${STUB_API_KEY} placeholder — is produced for UC1. Asserting the placeholder
// here would fail on what the product actually does, so the scope want stays at
// the effects the read-back and analyser do produce.
var uc1Scope = ScopeWant{
	EnvVar:      stubCredentialEnvVar,
	Hosts:       []string{stubHost},
	ConfigKeys:  nil,
	Placeholder: false,
}

// uc1Journey is UC1: a fresh install with no host config and no managed layer,
// offered the first-run menu, that learns by using the seeded stub provider and
// saves the credential it needed to the per-slug user scope.
//
// The launcher is a plain `run`, not `run --learn`: firstRunMenu returns early
// when --learn is already set (cmd/agents-sandbox/commands_cli.go), so
// `run --learn` would never offer the menu. The menu's "Learn by using it"
// choice turns learn mode on for the session through Outcome.Apply.
var uc1Journey = Journey{
	Name:      "uc1-learn-by-using",
	Harnesses: []string{opencode2AgentName},
	Steps: []Step{
		Given(StepFreshHome()),
		And(StepSeedStub()),
		When(StepLaunch("run")),
		Then(StepAssertFirstRunOffered(firstrun.ChoiceLearnByUsing)),
		When(StepChooseFirstRun(firstrun.ChoiceLearnByUsing)),
		Then(StepWaitReady()),
		When(StepSendPrompt(uc1Prompt)),
		And(StepQuit()),
		And(StepChooseExit(exitSaveAllKey)),
		Then(StepAssertScopeWritten(uc1Scope)),
		And(StepAssertNoProjectScaffolding()),
		And(StepAssertFirstRunRecorded(firstrun.ChoiceLearnByUsing)),
	},
}
