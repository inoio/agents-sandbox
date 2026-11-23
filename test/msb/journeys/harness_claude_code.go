package journeys

import "github.com/inoio/agents-sandbox/test/msb/driver"

// claudeCodeAgentName is claude-code's registered agent name.
const claudeCodeAgentName = "claude-code"

// claudeCodeHarness points claude-code at the stub through its environment: the
// Messages endpoint, a bearer token (an auth token skips the custom-API-key
// approval an ANTHROPIC_API_KEY asks for), the stub's main and small models, and
// no nonessential traffic. Its settings.json is a reserved home target, so the
// environment is the only route. The token is a dummy the stub never checks, so a
// plain env var is fine for it; a real credential would go through the secret
// mechanism. acceptEdits lets the Write tool run without a
// permission prompt. ~/.claude.json skips onboarding and accepts the workspace
// trust dialog, as the startup smoke does.
var claudeCodeHarness = Harness{
	StubConfig: func(string) map[string]string {
		return map[string]string{driver.ClaudeCodeConfigPath: driver.ClaudeCodeSmokeConfig}
	},
	StubEnv: func(addr string) map[string]string {
		return map[string]string{
			"ANTHROPIC_BASE_URL":                       addr,
			"ANTHROPIC_AUTH_TOKEN":                     stubProviderAPIKey,
			"ANTHROPIC_MODEL":                          stubProviderModel,
			"ANTHROPIC_SMALL_FAST_MODEL":               stubTitleModel,
			"ANTHROPIC_DEFAULT_HAIKU_MODEL":            stubTitleModel,
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		}
	},
	LaunchArgs: []string{"--permission-mode", "acceptEdits"},
	WriteFileCall: func(guestPath, content string) ToolCall {
		return ToolCall{
			Name:      "Write",
			Arguments: writeToolArguments("file_path", guestPath, content),
		}
	},
	ResumeArgs: []string{continueFlag},
}
