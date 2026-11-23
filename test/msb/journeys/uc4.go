package journeys

import (
	"maps"

	"github.com/inoio/agents-sandbox/internal/firstrun"
)

// uc4Prompt is the one message UC4 sends. Like UC1, a completed model turn is
// not required: the journey asserts what the host seed wrote to the per-slug
// scope before the session, so the prompt only has to make the agent issue a
// request at the stub provider's baseURL.
const uc4Prompt = "hello"

// uc4HostConfig builds UC4's host agent config from the journey context: the
// same provider the stub serves, but as the harness's own host config
// (Harness.Learn.HostConfig). Its baseURL host is the stub's
// advertised origin, so the static apiKey the seed migrates is scoped to
// exactly the host uc4Scope asserts. The harness's login store beside it
// (Harness.Learn.HostLoginStore) is a value store the seed must never copy.
func uc4HostConfig(c *Context) map[string]string {
	files := c.Harness.Learn.HostConfig(c.Stub.Addr())
	maps.Copy(files, c.Harness.Learn.HostLoginStore)
	return files
}

// uc4Scope is what UC4 asserts the host seed wrote to the per-slug user scope
// before the session: the stub provider's inline apiKey is a static credential,
// copied into the managed config as the ${STUB_API_KEY} placeholder and scoped
// to the stub host in env.secret.yaml. Unlike UC1, the seed writes the managed
// config before CaptureBaseline, so UC4 can assert the placeholder.
var uc4Scope = ScopeWant{ConfigKeys: true, Placeholder: true}

// uc4Journey is UC4: a fresh install with the agent's host config present, whose
// first-run menu offers "Learn from my host setup" and whose choice seeds the
// per-slug scope from the host before the session, then learns like UC1.
//
// The launcher is a plain `run`, not `run --learn`: firstRunMenu returns early
// when --learn is already set, so the menu (and its host-seed choice) would
// never be offered. The stub is started before the host config so the host
// config's provider baseURL can carry the stub's runtime address.
var uc4Journey = Journey{
	Name:      "uc4-learn-from-host",
	Harnesses: []string{opencode2AgentName},
	LearnMode: true,
	Steps: []Step{
		Given(StepFreshHome()),
		And(StepSeedStub()),
		And(StepSeedHostConfig(uc4HostConfig)),
		When(StepLaunch("run")),
		Then(StepAssertFirstRunOffered(firstrun.ChoiceLearnFromHost)),
		When(StepChooseFirstRun(firstrun.ChoiceLearnFromHost)),
		Then(StepWaitReady()),
		When(StepSendPrompt(uc4Prompt)),
		And(StepQuit()),
		And(StepChooseExit(exitSaveAllKey)),
		Then(StepAssertScopeWritten(uc4Scope)),
		And(StepAssertScopeExcludesHostLoginStore()),
	},
}
