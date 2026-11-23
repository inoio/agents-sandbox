package journeys

// Harness is what a journey's steps need to know about one harness beyond how
// to start and quit it (driver.Spec, which the startup smoke shares). A journey
// never names a harness's paths or formats itself; it asks the adapter.
//
//exhaustruct:ignore
type Harness struct {
	// StubConfig renders the VM-home config files (relative path to content)
	// that point the harness at the stub provider at addr. StepSeedStub
	// provisions them through the launcher's `home:` manifest.
	StubConfig func(addr string) map[string]string
	// StubEnv renders environment variables that point the harness at the stub
	// at addr, for a harness configured through its environment; they reach the
	// VM through the launcher's env file (StepConfigureLauncherEnv).
	StubEnv func(addr string) map[string]string
	// StubSnippets renders launcher agent-config snippets (file name to
	// content) that point the harness at the stub at addr; the launcher merges
	// them into the harness's own config file (StepConfigureLauncherEnv).
	StubSnippets func(addr string) map[string]string
	// ReadyMarker, when set, replaces the startup smoke's readiness marker
	// (driver.Spec.ReadyMarker) for a harness whose start screen differs once
	// the stub is configured.
	ReadyMarker string
	// LaunchArgs are agent arguments every launch of the harness passes, before
	// any step-specific ones.
	LaunchArgs []string
	// HostConfig renders the harness's own host config files pointing at the
	// stub at addr, for a journey that seeds an existing host setup.
	HostConfig func(addr string) map[string]string
	// HostLoginStore is a host login store file (relative path to content) the
	// host seed must drop rather than copy.
	HostLoginStore map[string]string
	// WriteFileCall is the harness's tool call that writes content to the
	// guest's absolute path, which the stub answers a turn with.
	WriteFileCall func(guestPath, content string) ToolCall
	// ResumeArgs are the agent arguments that continue the last session.
	ResumeArgs []string
}

// Harnesses maps an agent name to its adapter. Every harness a journey drives
// has one (TestEveryDrivenHarnessHasAnAdapter).
var Harnesses = map[string]Harness{
	opencode2AgentName:  opencode2Harness,
	claudeCodeAgentName: claudeCodeHarness,
	codexAgentName:      codexHarness,
	piAgentName:         piFamilyHarness(piModelsPath),
	ohMyPiAgentName:     ohMyPiHarness(),
}

// continueFlag is the agent flag that continues the last session, shared by
// opencode2, claude-code, and the pi family.
const continueFlag = "--continue"

// writeToolArguments renders a file-writing tool's JSON arguments: the guest
// path under pathKey (the harness's own name for it) and the content.
func writeToolArguments(pathKey, guestPath, content string) string {
	return jsonObject(map[string]string{pathKey: guestPath, "content": content})
}
