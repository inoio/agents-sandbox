package journeys

import "github.com/inoio/agents-sandbox/internal/firstrun"

// uc4Prompt is the one message UC4 sends. Like UC1, a completed model turn is
// not required: the journey asserts what the host seed wrote to the per-slug
// scope before the session, so the prompt only has to make the agent issue a
// request at the stub provider's baseURL.
const uc4Prompt = "hello"

// The host files UC4 seeds into the isolated HOME: opencode2's provision rules
// select both, so the first-run decision sees host setup and offers "Learn from
// my host setup".
const (
	// uc4HostConfigRelPath is the opencode global config file the agent's
	// snippet pattern merges: the host config the seed copies (placeholdered)
	// into the per-slug scope.
	uc4HostConfigRelPath = ".config/opencode/opencode.jsonc"
	// uc4LoginStoreRelPath is opencode's auth.json, a declared login credential
	// store. The seed must drop it rather than copy its raw value.
	uc4LoginStoreRelPath = ".local/share/opencode/auth.json"
	// uc4LoginStoreFileName is the name the login store would take in the
	// scope's managed agent config dir had the seed copied it.
	uc4LoginStoreFileName = "auth.json"
)

// uc4HostConfig builds UC4's host agent config from the journey context: the
// same OpenAI-compatible provider the stub serves, but as the agent's own host
// config file. Its baseURL host is the stub's advertised origin, so the static
// apiKey the seed migrates is scoped to exactly the host uc4Scope asserts. The
// login store beside it is a value store the seed must never copy.
func uc4HostConfig(c *Context) map[string]string {
	return map[string]string{
		uc4HostConfigRelPath: stubProviderDocument(c.Stub.Addr()),
		uc4LoginStoreRelPath: `{"openai":{"type":"api"}}`,
	}
}

// uc4Scope is what UC4 asserts the host seed wrote to the per-slug user scope
// before the session: the stub provider's inline apiKey is a static credential,
// copied into the managed config as the ${STUB_API_KEY} placeholder and scoped
// to the stub host in env.secret.yaml. Unlike UC1, the seed writes the managed
// config before CaptureBaseline, so UC4 can assert the placeholder.
var uc4Scope = ScopeWant{
	EnvVar:      stubCredentialEnvVar,
	Hosts:       []string{stubHost},
	ConfigKeys:  []string{"baseURL", "npm"},
	Placeholder: true,
}

// uc4Journey is UC4: a fresh install with the agent's host config present, whose
// first-run menu offers "Learn from my host setup" and whose choice seeds the
// per-slug scope from the host before the session, then learns like UC1.
//
// The launcher is a plain `run`, not `run --learn`: firstRunMenu returns early
// when --learn is already set, so the menu (and its host-seed choice) would
// never be offered. The stub is started before the host config so the host
// config's provider baseURL can carry the stub's runtime address.
var uc4Journey = Journey{
	Name:  "uc4-learn-from-host",
	Agent: opencode2AgentName,
	Steps: []Step{
		StepFreshHome(),
		StepSeedStub(),
		StepSeedHostConfig(uc4HostConfig),
		StepLaunch("run"),
		StepAssertFirstRunOffered(firstrun.ChoiceLearnFromHost),
		StepChooseFirstRun(firstrun.ChoiceLearnFromHost),
		StepWaitReady(),
		StepSendPrompt(uc4Prompt),
		StepQuit(),
		StepChooseExit(exitSaveAllKey),
		StepAssertScopeWritten(uc4Scope),
		StepAssertScopeExcludes(uc4LoginStoreFileName),
	},
}
