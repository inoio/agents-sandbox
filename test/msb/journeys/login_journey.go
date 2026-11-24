package journeys

// loginJourney drives a harness's logins through the sandbox, with no real
// account:
//
//   - The browser login goes up to the provider: the link reaches the host's
//     browser, and the browser's return to the loopback callback reaches the
//     harness in the VM through the host forwarder. No provider is involved,
//     so the harness's token exchange fails; the journey asserts the sandbox's
//     part.
//   - An API-key login from the shell stores a login in the VM's home.
//   - The MCP sign-in runs a full OAuth login against the stub authorization
//     server on the host: discovery, client registration, the browser's
//     authorize and callback through the opener and the forwarder, and the
//     token exchange with PKCE from the VM.
//   - Both stored logins must survive a VM stop: the harness starts signed in
//     and calls the MCP server with the issued token.
//
// It runs on its own project, so the stored logins stay out of the harness's
// shared home volume, and under the `host` profile, which admits only the host
// gateway the stub listens behind: no step reaches a real provider.
var loginJourney = Journey{
	Name:      "harness-login",
	Harnesses: []string{codexAgentName},
	Network:   hostGatewayNetwork,
	Steps: []Step{
		Given(StepOwnProject("login")),
		And(StepFreshHome()),
		And(StepConfigureLauncher()),
		And(StepFakeHostBrowser()),
		And(StepStartStubOAuthServer()),
		When(StepLaunchLogin()),
		Then(StepAssertLoginLinkOpened()),
		When(StepBrowserReturnsToCallback()),
		Then(StepAssertCallbackReachedHarness()),
		And(StepWaitLoginEnds()),
		When(StepSignInWithAPIKey()),
		And(StepLaunchMCPLogin()),
		Then(StepAssertLoginLinkOpened()),
		When(StepBrowserSignsIn()),
		Then(StepAssertMCPLoginCompleted()),
		When(StepStopVM()),
		Then(StepAssertLoginStored()),
		When(StepLaunch(runCommand)),
		Then(StepWaitReady()),
		And(StepAssertNoSignIn()),
		And(StepAssertMCPCalledWithToken()),
		When(StepQuitToExit()),
	},
}
