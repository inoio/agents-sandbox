package journeys

import "fmt"

const kimiAgentName = "kimi-code"

var kimiHarness = Harness{
	Stub: StubSetup{
		Snippets: func(addr string) map[string]string {
			return map[string]string{"config-journeys.toml": kimiStubConfig(addr)}
		},
	},
	Drive: Driving{
		LaunchArgs: []string{"--auto"},
		ResumeArgs: []string{continueFlag},
		WriteFileCall: func(guestPath, content string) ToolCall {
			return ToolCall{Name: "Write", Arguments: writeToolArguments("path", guestPath, content)}
		},
	},
}

func kimiStubConfig(addr string) string {
	return fmt.Sprintf(`default_model = "stub/stub"
auto_session_title = false
telemetry = false
disable_feedback_survey = true

[providers.stub]
type = "openai"
base_url = %q
api_key = %q

[models."stub/stub"]
provider = "stub"
model = "stub"
max_context_size = 32768
`, addr+"/v1", stubProviderAPIKey)
}
