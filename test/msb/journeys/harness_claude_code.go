package journeys

// claudeCodeAgentName is claude-code's registered agent name.
const claudeCodeAgentName = "claude-code"

// claudeCodeHarness points claude-code at the stub through its environment: the
// Messages endpoint, a bearer token (an auth token skips the custom-API-key
// approval an ANTHROPIC_API_KEY asks for), the stub's main and small models, and
// no nonessential traffic. Its settings.json is a reserved home target, so the
// environment is the only route. The token is a dummy the stub never checks, so a
// plain env var is fine for it; a real credential would go through the secret
// mechanism. acceptEdits lets the Write tool run without a
// permission prompt. Its onboarding and workspace-trust state come from the
// startup smoke's seeds (driver.Spec.Seeds).
var claudeCodeHarness = Harness{
	Stub: StubSetup{
		Env: func(addr string) map[string]string {
			return map[string]string{
				"ANTHROPIC_BASE_URL":                       addr,
				"ANTHROPIC_AUTH_TOKEN":                     stubProviderAPIKey,
				"ANTHROPIC_MODEL":                          stubProviderModel,
				"ANTHROPIC_SMALL_FAST_MODEL":               stubTitleModel,
				"ANTHROPIC_DEFAULT_HAIKU_MODEL":            stubTitleModel,
				"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
			}
		},
	},
	MCP: &MCPFacts{
		// claude-code reads its servers from the project's .mcp.json, and a
		// settings snippet approves them without the per-project prompt.
		ProjectFiles: func(name, url string) map[string]string {
			return map[string]string{".mcp.json": mustJSON(map[string]any{
				"mcpServers": map[string]any{name: map[string]any{"type": "http", "url": url}},
			})}
		},
		Snippets: func(string, string) map[string]string {
			return map[string]string{claudeCodeMCPSnippet: `{"enableAllProjectMcpServers": true}`}
		},
		// run passes LaunchArgs before any subcommand, so the sign-in runs in
		// the shell.
		LoginCommand: func(name string) string { return "claude mcp login " + shellQuote(name) },
		// claude-code sends OAuth credentials only over TLS (loopback exempt).
		TLS: true,
	},
	Drive: Driving{
		LaunchArgs: []string{"--permission-mode", "acceptEdits"},
		WriteFileCall: func(guestPath, content string) ToolCall {
			return ToolCall{Name: "Write", Arguments: writeToolArguments("file_path", guestPath, content)}
		},
		ResumeArgs: []string{continueFlag},
	},
}

// claudeCodeMCPSnippet is the launcher settings snippet that approves the
// project's MCP servers.
const claudeCodeMCPSnippet = "settings-journeys-mcp.json"
