package journeys

// The core-session journey's prompts and the file its first turn writes. Each
// prompt is distinctive, so its echo and its restored message cannot be matched
// by a repaint or by another prompt.
const (
	coreFirstPrompt  = "journeys core first turn"
	coreSecondPrompt = "journeys core second client"
	coreThirdPrompt  = "journeys core first client again"
	coreFile         = "journeys-core.txt"
	coreFileContent  = "written by the stub's tool call"
)

// hostGatewayNetwork is the network profile that admits the host gateway the
// stub's default route names.
const hostGatewayNetwork = "host"

// coreSessionJourney chains the #783 promotion bar's first run (A), real turn
// (C), second client (E), and persistence (D) over one project and two boots:
// the harness starts on a fresh home whose launcher is already configured (the
// first-run menu is the learn-mode journeys' concern), a turn writes a file the
// host sees, a second client works next to the first, and after both quit and
// the VM is stopped, the harness resumes the session from what the home volume
// kept. It runs on a project of its own, because it restarts and attaches
// twice, and under the host profile the stub route needs. Client 1 both sends
// the last prompt and quits last, because harnesses differ in which session
// they continue: opencode2 the last active one, claude-code the last saved one.
var coreSessionJourney = Journey{
	Name:      "core-session",
	Harnesses: []string{opencode2AgentName, claudeCodeAgentName},
	Network:   hostGatewayNetwork,
	Steps: []Step{
		Given(StepOwnProject("core")),
		And(StepFreshHome()),
		And(StepSeedStub()),
		And(StepConfigureLauncherEnv()),
		And(StepScriptWriteFile(coreFile, coreFileContent)),
		When(StepLaunch("run")),
		Then(StepWaitReady()),
		And(StepAssertExperimentalNotice()),
		And(StepAssertNoProjectScaffolding()),
		When(StepSendPrompt(coreFirstPrompt)),
		Then(StepAssertStubServed()),
		And(StepAssertProjectFile(coreFile, coreFileContent)),
		And(StepAssertReplyShown()),
		When(StepAttachClient()),
		Then(StepWaitReady()),
		And(StepAssertNoReconfigPrompt()),
		When(StepSendPrompt(coreSecondPrompt)),
		Then(StepAssertReplyShown()),
		When(StepUseClient(1)),
		And(StepSendPrompt(coreThirdPrompt)),
		Then(StepAssertReplyShown()),
		When(StepUseClient(2)),
		And(StepQuitToExit()),
		And(StepUseClient(1)),
		And(StepQuitToExit()),
		And(StepStopVM()),
		When(StepRelaunchWithResume()),
		Then(StepWaitReady()),
		And(StepAssertVMRestarted()),
		And(StepAssertSessionRestored(coreThirdPrompt)),
		When(StepQuitToExit()),
	},
}
