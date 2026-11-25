package journeys

import "github.com/inoio/agents-sandbox/test/msb/driver"

// exposedLoginKey is where a harness's own API-key login stores the key in
// plain text in the VM home: a top-level field of a JSON file.
type exposedLoginKey struct {
	path  string
	field string
}

// exposedLoginKeys names the stores the read-back inspects for a plain-text key.
var exposedLoginKeys = map[string]exposedLoginKey{
	codexAgentName:      {path: codexAuthPath, field: "OPENAI_API_KEY"},
	claudeCodeAgentName: {path: driver.ClaudeCodeConfigPath, field: "primaryApiKey"},
}

// exposedLoginKeyValue is the dummy key a journey stores; nothing checks it.
const exposedLoginKeyValue = "sk-journeys-plain-text-key"

// exposedLoginKeyJourney: a harness's own login left an API key in plain text
// in the VM home. Learn mode reports it after the session with guidance and
// writes nothing into the home (#873, #892).
var exposedLoginKeyJourney = Journey{
	Name:      "learn-exposed-login-key",
	Harnesses: []string{codexAgentName, claudeCodeAgentName},
	Steps: []Step{
		Given(StepFreshHome()),
		And(StepSeedStub()),
		And(StepSeedExposedLoginKey()),
		When(StepLaunch("run", "--learn")),
		Then(StepWaitReady()),
		When(StepQuit()),
		And(StepChooseExit(exitSaveAllKey)),
		Then(StepAssertExposedLoginKeyWarned()),
	},
}
