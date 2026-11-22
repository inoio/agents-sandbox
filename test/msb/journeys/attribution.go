package journeys

// attributionHostConfigRelPath is the agent's global config file the attribution
// journey seeds into the isolated HOME as the host config. It is opencode's
// higher-precedence .jsonc, so the host seed is what the agent reads over the
// stub's provisioned .json; both carry the same provider pointing at the stub.
const attributionHostConfigRelPath = ".config/opencode/opencode.jsonc"

// attributionPrompt is the one message the journey sends, so the agent issues a
// request at the stub provider's baseURL and the terminating proxy observes the
// credential→destination pair. A completed model turn is not required; the
// journey asserts what the analyser wrote to the scope, not the model's answer.
const attributionPrompt = "hello"

// attributionHostConfig builds the journey's host agent config from the context:
// the same OpenAI-compatible provider the stub serves, so the credential has a
// config-entry source and the agent sends to the stub. Its baseURL host is the
// stub's advertised origin, the host attributionScope asserts.
//
// The builder reads the stub's runtime address, so StepSeedStub must run before
// this step (the reverse of the plan's prose order, which the context-derived
// builder requires).
func attributionHostConfig(c *Context) map[string]string {
	return map[string]string{
		attributionHostConfigRelPath: stubProviderDocument(c.Stub.Addr()),
	}
}

// attributionScope is what the attribution journey asserts the run wrote to the
// per-slug user scope: the stub provider's inline apiKey is a static credential,
// scoped to the stub host in env.secret.yaml.
//
// It is deliberately identical in effect to uc1Scope. The plan intended the host
// config to give the credential a config-entry source but no hosts, so only the
// observation's attribution could supply the stub host. That is not realizable in
// v1: channel.Attribution.Credential is never populated (recorder.attribute
// records header NAMES only), so analyser.credentialHosts always falls back to
// the hosts the read-back found in the credential's config entry — and that entry
// must name the stub baseURL for the agent to reach the stub at all. The
// assertion therefore holds through the fallback, not attribution; see
// specs/testing/msb-journeys.md. There is no managed-config assertion: the
// provisioned provider file precedes CaptureBaseline, so --learn leaves it
// unchanged, exactly as UC1's live path does.
var attributionScope = ScopeWant{
	EnvVar:      stubCredentialEnvVar,
	Hosts:       []string{stubHost},
	ConfigKeys:  nil,
	Placeholder: false,
}

// attributionJourney drives `run --learn` against the host-side stub provider and
// asserts the learned scope keeps the credential scoped to the stub host while
// not widening the network.
//
// `--learn` skips the first-run menu (firstRunMenu returns early when --learn is
// set), so firstrun.SeedFromHost does not run; the seeded host config reaches the
// VM only because StepSeedStub's launcher config sets provision-host-config: true.
//
// It is a faithful e2e smoke of `run --learn` → scope, not a discriminator of
// observation attribution over the config-entry fallback: v1's attribution branch
// is unreachable, so the stub-host assertion holds via the fallback regardless of
// whether the observation saw the pair (see attributionScope).
var attributionJourney = Journey{
	Name:  "attribution-observed-pair",
	Agent: opencode2AgentName,
	Steps: []Step{
		StepFreshHome(),
		StepSeedStub(),
		StepSeedHostConfig(attributionHostConfig),
		StepLaunch("run", "--learn"),
		StepWaitReady(),
		StepSendPrompt(attributionPrompt),
		StepQuit(),
		StepChooseExit(exitSaveAllKey),
		StepAssertScopeWritten(attributionScope),
		StepAssertNetworkNotWidened(runNetworkPolicy),
	},
}
