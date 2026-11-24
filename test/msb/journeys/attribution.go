package journeys

import "github.com/inoio/agents-sandbox/test/msb/driver"

// attributionPrompt is the one message the journey sends, so the agent issues a
// request at the stub provider's baseURL. A completed model turn is not
// required; the journey asserts what the analyser wrote to the scope, not the
// model's answer. Under the journey's `public` profile the host-gateway route is
// denied, so the request need not reach the stub at all, and this journey does
// not depend on the terminating proxy observing the credential→destination pair
// (see attributionScope).
const attributionPrompt = "hello"

// attributionHostConfig builds the journey's host agent config from the context:
// the same OpenAI-compatible provider the stub serves, so the credential has a
// config-entry source and the agent sends to the stub. Its baseURL host is the
// stub's advertised origin, the host attributionScope asserts.
//
// The builder reads the stub's runtime address, so StepSeedStub must run before
// this step (the reverse of the plan's prose order, which the context-derived
// builder requires).
func attributionHostConfig(c *Context) (map[string]string, error) {
	learn, err := learnFacts(c)
	if err != nil {
		return nil, err
	}
	return learn.HostConfig(c.Stub.Addr()), nil
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
var attributionScope = ScopeWant{ManagedConfig: false, Placeholder: false}

// attributionJourney drives `run --learn` against the host-side stub provider and
// asserts the learned scope keeps the credential scoped to the stub host while
// not widening the network.
//
// It runs under a broad profile (driver.PublicNetwork) rather than the spec's
// default none: under none the stub request is denied, so the analyser records
// no used destination and writes no network posture, leaving the
// network-not-widened assertion vacuous. Under public a turn that reaches the
// stub is a used destination, so the analyser's lock (none + the used host)
// actually exercises the guard. `public` does not include the host gateway the
// default route names, so the dial is still denied; this only bites on a route
// that `public` admits and that passes the observation proxy.
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
	Name:      "attribution-observed-pair",
	Harnesses: []string{opencode2AgentName, ohMyPiAgentName},
	LearnMode: true,
	Network:   driver.PublicNetwork,
	Steps: []Step{
		Given(StepFreshHome()),
		And(StepSeedStub()),
		And(StepSeedHostConfig(attributionHostConfig)),
		When(StepLaunch("run", "--learn")),
		And(StepExitHarnessSetup()),
		Then(StepWaitReady()),
		When(StepSendPrompt(attributionPrompt)),
		And(StepQuit()),
		And(StepChooseExit(exitSaveAllKey)),
		Then(StepAssertScopeWritten(attributionScope)),
		And(StepAssertNetworkNotWidened(runNetworkPolicy, stubHost)),
	},
}
