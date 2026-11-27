package journeys

import "github.com/inoio/agents-sandbox/test/msb/driver"

// exposedLoginKey is where a harness's own API-key login stores the key in
// plain text in the VM home: a top-level field of a JSON file.
type exposedLoginKey struct {
	path  string
	field string
	// envVar and host are the env secret the key moves into.
	envVar, host string
	// provisioned reports whether the launcher provisions the store from the
	// host in the key's place (codex reads the key from its store; claude-code
	// from its environment, so it needs no file).
	provisioned bool
}

// exposedLoginKeys names the stores the read-back inspects for a plain-text key.
var exposedLoginKeys = map[string]exposedLoginKey{
	codexAgentName: {
		path: codexAuthPath, field: "OPENAI_API_KEY",
		envVar: "OPENAI_API_KEY", host: "api.openai.com", provisioned: true,
	},
	claudeCodeAgentName: {
		path: driver.ClaudeCodeConfigPath, field: "primaryApiKey",
		envVar: "ANTHROPIC_API_KEY", host: "api.anthropic.com",
	},
}

// exposedLoginKeyValue is the dummy key a journey stores; nothing checks it.
const exposedLoginKeyValue = "sk-journeys-plain-text-key"

// exposedLoginKeyJourney: a harness's own login left an API key in plain text
// in the VM home. Saving the learn proposal moves it into an env secret and,
// where the harness reads it from its store, provisions the store from the host
// with the secret's placeholder (#873, #892).
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
		Then(StepAssertExposedLoginKeyMoved()),
		And(StepAssertProvisionedLoginStore()),
		And(StepAssertRenewingLoginPreserved()),
		When(StepResetProvisionedLoginHome()),
		Then(StepAssertProvisionedLoginStore()),
	},
}
