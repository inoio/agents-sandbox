package journeys

// Harness is what a journey's steps need to know about one harness beyond how
// to start and quit it (driver.Spec, which the startup smoke shares), in three
// parts: how it reaches the stub, how a journey drives it, and what learn mode
// finds in it. A journey never names a harness's paths or formats itself; it
// asks the adapter.
//
//exhaustruct:ignore
type Harness struct {
	Stub  StubSetup
	Drive Driving
	// Learn is nil for a harness no learn-mode journey covers yet.
	Learn *LearnFacts
}

// StubSetup is how a harness is pointed at the stub provider at addr. A
// harness uses whichever of the three routes its own config allows; its
// natural config file is often a reserved home target.
//
//exhaustruct:ignore
type StubSetup struct {
	// HomeFiles renders VM-home files (relative path to content), provisioned
	// through the launcher's `home:` manifest by StepSeedStub: provider files,
	// and the onboarding or login files that keep the harness off its first-run
	// screens.
	HomeFiles func(addr string) map[string]string
	// Env renders environment variables, which reach the VM through the
	// launcher's env file (StepConfigureLauncherEnv).
	Env func(addr string) map[string]string
	// Snippets renders launcher agent-config snippets (file name to content)
	// that the launcher merges into the harness's own config file
	// (StepConfigureLauncherEnv).
	Snippets func(addr string) map[string]string
}

// Driving is how a journey drives a harness's TUI once it starts.
//
//exhaustruct:ignore
type Driving struct {
	// ReadyMarker, when set, replaces the startup smoke's readiness marker
	// (driver.Spec.ReadyMarker) for a harness whose start screen differs once
	// the stub is configured.
	ReadyMarker string
	// LaunchArgs are agent arguments every run of the harness passes, before
	// any step-specific ones.
	LaunchArgs []string
	// ResumeArgs are the agent arguments that continue the last session.
	ResumeArgs []string
	// WriteFileCall is the harness's tool call that writes content to the
	// guest's absolute path, which the stub answers a turn with.
	WriteFileCall func(guestPath, content string) ToolCall
}

// LearnFacts is what learn mode finds in a harness pointed at the stub: what
// the learn-mode journeys seed as host setup and expect in the per-slug scope.
//
//exhaustruct:ignore
type LearnFacts struct {
	// HostConfig renders the harness's own host config files pointing at the
	// stub at addr, for a journey that seeds an existing host setup.
	HostConfig func(addr string) map[string]string
	// HostLoginStore is a host login store file (relative path to content) the
	// host seed must drop rather than copy.
	HostLoginStore map[string]string
	// CredentialEnvVar is the env-var name the read-back derives for the stub
	// credential the harness's config carries.
	CredentialEnvVar string
	// ManagedConfigKeys are config keys the managed agent config carries once
	// the host seed or the write-back copied the harness's provider config.
	ManagedConfigKeys []string
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
