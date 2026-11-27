package journeys

import "github.com/inoio/agents-sandbox/test/msb/driver"

// loginJourney drives a harness's provider logins through the sandbox, with no
// real account:
//
//   - The browser login goes up to the provider: the link reaches the host's
//     browser, and the browser's return to the loopback callback reaches the
//     harness in the VM through the host forwarder. No provider is involved,
//     so the harness's token exchange fails; the journey asserts the sandbox's
//     part.
//   - An API-key login from the shell stores a login in the VM's home, which
//     must survive a VM stop: the harness reports it and starts signed in.
//
// It runs on its own project, so the stored login stays out of the harness's
// shared home volume, and without egress: no step reaches a real provider.
var loginJourney = Journey{
	Name:      "harness-login",
	Harnesses: []string{codexAgentName},
	Network:   driver.NetworkNone,
	Steps: []Step{
		Given(StepOwnProject("login")),
		And(StepFreshHome()),
		And(StepConfigureLauncher()),
		And(StepFakeHostBrowser()),
		When(StepLaunchLogin()),
		Then(StepAssertLoginLinkOpened()),
		When(StepBrowserReturnsToCallback()),
		Then(StepAssertCallbackReachedHarness()),
		And(StepWaitLoginEnds()),
		When(StepSignInWithAPIKey()),
		And(StepStopVM()),
		Then(StepAssertLoginStored()),
		When(StepLaunch(runCommand)),
		Then(StepWaitReady()),
		And(StepAssertNoSignIn()),
		When(StepQuitToExit()),
	},
}

// mcpLoginJourney signs a harness in to a remote MCP server behind OAuth with
// no real account: the stub authorization server on the host runs the whole
// flow (discovery through the MCP endpoint's 401, client registration, the
// authorize link through the opener to the fake browser, the redirect back to
// the loopback callback forwarded into the VM, the code exchange with its PKCE
// verifier). The harness is configured to the stub model, so it starts signed
// in; after a VM stop it must call the MCP server with the token the sign-in
// issued. It runs on its own project and under the `host` profile, which
// admits only the host gateway the stubs listen behind.
var mcpLoginJourney = Journey{
	Name:      "mcp-oauth-login",
	Harnesses: []string{codexAgentName, claudeCodeAgentName, opencode2AgentName},
	Network:   hostGatewayNetwork,
	Steps: []Step{
		Given(StepOwnProject("mcp")),
		And(StepFreshHome()),
		And(StepFakeHostBrowser()),
		And(StepStartStubOAuthServer()),
		And(StepSeedStub()),
		And(StepConfigureLauncherEnv()),
		When(StepLaunchMCPLogin()),
		Then(StepAssertLoginLinkOpened()),
		When(StepBrowserSignsIn()),
		Then(StepAssertMCPLoginCompleted()),
		When(StepStopVM()),
		And(StepLaunch(runCommand)),
		Then(StepWaitReady()),
		And(StepAssertMCPCalledWithToken()),
		When(StepQuitToExit()),
	},
}
