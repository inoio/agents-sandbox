package journeys

// opencodeAgentName is the registered name for OpenCode v1.
const opencodeAgentName = "opencode"

// opencodeHarness points OpenCode v1 at the model stub and declares the remote
// MCP OAuth server through its merged config. Its write tool uses filePath,
// unlike opencode2's path argument.
var opencodeHarness = Harness{
	Stub: StubSetup{
		HomeFiles: func(addr string) map[string]string {
			return map[string]string{opencodeStubConfigTarget: opencodeProviderDocument(addr)}
		},
	},
	Drive: Driving{
		WriteFileCall: func(guestPath, content string) ToolCall {
			return ToolCall{Name: "write", Arguments: writeToolArguments("filePath", guestPath, content)}
		},
		ResumeArgs: []string{continueFlag},
	},
	MCP: &MCPFacts{
		Snippets: func(name, url string) map[string]string {
			return map[string]string{opencodeMCPSnippet: mustJSON(map[string]any{
				"mcp": map[string]any{name: map[string]any{"type": "remote", "url": url}},
			})}
		},
		LoginCommand: func(name string) string { return "opencode mcp auth " + shellQuote(name) },
	},
}
