package journeys

// Harness is what a journey's steps need to know about one harness beyond how
// to start and quit it (driver.Spec, which the startup smoke shares), in three
// parts: how it reaches the stub, how a journey drives it, and what learn mode
// finds in it. A journey never names a harness's paths or formats itself; it
// asks the adapter.
type Harness struct {
	Stub  StubSetup
	Drive Driving
	// Learn is nil for a harness no learn-mode journey covers yet.
	Learn *LearnFacts
	// Login is nil for a harness the login journey does not cover yet.
	Login *LoginFacts
}

// StubSetup is how a harness is pointed at the stub provider at addr. A
// harness uses whichever of the three routes its own config allows; its
// natural config file is often a reserved home target.
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
type Driving struct {
	// ReadyMarker, when set, replaces the startup smoke's readiness marker
	// (driver.Spec.ReadyMarker) for a harness whose start screen differs once
	// the stub is configured.
	ReadyMarker string
	// LaunchArgs are agent arguments every run of the harness passes, before
	// any step-specific ones.
	LaunchArgs []string
	// EnterInterrupts marks a harness whose Enter stops a running turn, so the
	// prompt step never presses Enter again after a submit (confirmSubmit).
	EnterInterrupts bool
	// StartupBusyMarker, when set, is the notice the harness shows for a prompt
	// submitted before its startup finished; it keeps the prompt in its editor,
	// so StepSendPrompt submits it again.
	StartupBusyMarker string
	// Diagnostics is a shell command that prints the harness's own view of its
	// sessions, which a failed turn wait prints (logHarnessDiagnostics).
	Diagnostics string
	// ResumeArgs are the agent arguments that continue the last session.
	ResumeArgs []string
	// WriteFileCall is the harness's tool call that writes content to the
	// guest's absolute path, which the stub answers a turn with.
	WriteFileCall func(guestPath, content string) ToolCall
	// Setup, when set, is the harness's own first-start setup screen, which it
	// shows when the journey's settings (StubSetup.Snippets) are not in place:
	// in a learn journey, which keeps the launcher unconfigured so the
	// first-run menu appears.
	Setup *SetupScreen
}

// SetupScreen is a harness's first-start setup screen and how a user leaves it.
type SetupScreen struct {
	// Marker is text the screen shows.
	Marker string
	// ExitKeys leave the setup and land in the harness's editor.
	ExitKeys []byte
}

// LearnFacts is what learn mode finds in a harness pointed at the stub: what
// the learn-mode journeys seed as host setup and expect in the per-slug scope.
type LearnFacts struct {
	// HostConfig renders the harness's own seedable host config files, for a
	// journey that seeds an existing host setup. Where the harness keeps its
	// provider in a seedable file, they point at the stub at addr and carry its
	// key (ManagedConfigHoldsCredential); otherwise they hold only settings.
	HostConfig func(addr string) map[string]string
	// HostLoginStore is a host credential store file (relative path to
	// content), a login store or a dotenv, that the host seed must drop rather
	// than copy. Its content must hold no static
	// value: the seed would rewrite one, and the exclusion check matches the
	// content.
	HostLoginStore map[string]string
	// CredentialEnvVar is the env-var name the read-back derives for the stub
	// credential the harness's config carries.
	CredentialEnvVar string
	// ManagedConfigKeys are config keys the managed agent config carries once
	// the host seed or the write-back copied HostConfig.
	ManagedConfigKeys []string
	// ManagedConfigHoldsCredential is whether the managed agent config carries
	// the stub credential, as the ${ENVVAR} placeholder, once the host seed or
	// the write-back copied the harness's config. A harness whose provider and
	// key live in a file neither copies (the pi family's models file) never
	// carries it.
	ManagedConfigHoldsCredential bool
}

// LoginFacts is how a harness signs in: its own logins, which the login journey
// drives through the sandbox, and how it shows a stored login.
type LoginFacts struct {
	// BrowserArgs are the agent arguments that start its browser login, which
	// opens an authorization link naming a loopback callback in the VM.
	BrowserArgs []string
	// CallbackMarker is text the page the harness answers a login callback
	// with shows, which the host forwarder's own answers lack.
	CallbackMarker string
	// APIKey is the shell command that signs the harness in with key and
	// stores the login in the harness's home.
	APIKey func(key string) string
	// Status is the shell command that exits 0 only while a login is stored.
	Status string
	// SignInMarker is text the harness's start screen shows while no login is
	// stored.
	SignInMarker string
	// MCPServer renders the launcher snippets (file name to content) that
	// declare a remote MCP server name at url in the harness's config.
	MCPServer func(name, url string) map[string]string
	// MCPLoginArgs are the agent arguments that sign in to the MCP server name
	// through its OAuth authorization server, in the browser.
	MCPLoginArgs func(name string) []string
}

// Harnesses maps an agent name to its adapter. Every harness a journey drives
// has one (TestEveryDrivenHarnessHasAnAdapter).
var Harnesses = map[string]Harness{
	opencode2AgentName:  opencode2Harness,
	claudeCodeAgentName: claudeCodeHarness,
	codexAgentName:      codexHarness,
	piAgentName:         piFamilyHarness(piModelsPath),
	ohMyPiAgentName:     ohMyPiHarness(),
	hermesAgentName:     hermesHarness,
}

// continueFlag is the agent flag that continues the last session, shared by
// opencode2, claude-code, and the pi family.
const continueFlag = "--continue"

// writeToolArguments renders a file-writing tool's JSON arguments: the guest
// path under pathKey (the harness's own name for it) and the content.
func writeToolArguments(pathKey, guestPath, content string) string {
	return jsonObject(map[string]string{pathKey: guestPath, "content": content})
}
