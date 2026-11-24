package journeys

import (
	"fmt"
	"strings"
)

// codexAgentName is codex's registered agent name.
const codexAgentName = "codex"

// codexAuthPath is codex's login file, home-relative.
const codexAuthPath = ".codex/auth.json"

// codexStubSnippet is the launcher snippet that merges the stub provider into
// codex's config.toml; the launcher merges only snippets named config*.toml.
const codexStubSnippet = "config-journeys-stub.toml"

// codexHarness points codex at the stub through a config.toml snippet the
// launcher merges in: a Responses-API provider at the stub, selected with the
// stub model. Its config.toml is a reserved home target and -c overrides switch
// codex into its embedded mode, so the snippet is the route. auth.json holds a
// dummy API key, so codex skips its sign-in screen.
var codexHarness = Harness{
	Stub: StubSetup{
		HomeFiles: func(string) map[string]string {
			return map[string]string{codexAuthPath: jsonObject(map[string]string{"OPENAI_API_KEY": stubProviderAPIKey})}
		},
		Snippets: func(addr string) map[string]string {
			return map[string]string{codexStubSnippet: codexStubConfig(addr)}
		},
	},
	Drive: Driving{
		// codex writes through its exec_command tool, a shell command line.
		WriteFileCall: func(guestPath, content string) ToolCall {
			command := "printf %s " + shellQuote(content) + " > " + shellQuote(guestPath)
			return ToolCall{Name: "exec_command", Arguments: jsonObject(map[string]string{"cmd": command})}
		},
		ResumeArgs: []string{"resume", "--last"},
	},
	Login: &LoginFacts{
		// `codex login` starts the ChatGPT browser login: a callback server on
		// port 1455 in the VM and no paste fallback. codex answers a callback
		// it cannot complete with its error page.
		BrowserArgs:    []string{"login"},
		CallbackMarker: "Codex Sign-in",
		// codex takes the key only from a pipe, not a terminal.
		APIKey: func(key string) string {
			return "printf %s " + shellQuote(key) + " | codex login --with-api-key"
		},
		Status:       "codex login status",
		SignInMarker: "Choose how you want to use Codex",
		MCPServer: func(name, url string) map[string]string {
			return map[string]string{codexMCPSnippet: fmt.Sprintf("[mcp_servers.%s]\nurl = %q\n", name, url)}
		},
		MCPLoginArgs: func(name string) []string { return []string{"mcp", "login", name} },
	},
}

// codexMCPSnippet is the launcher snippet that declares the login journey's
// MCP server in codex's config.toml.
const codexMCPSnippet = "config-journeys-mcp.toml"

// codexStubConfig renders the config.toml fragment that selects the stub
// provider at addr. Go's %q stands in for TOML string escaping, which holds for
// its inputs only: ASCII identifiers and an http URL with no control characters.
func codexStubConfig(addr string) string {
	return fmt.Sprintf(`model = %q
model_provider = %q

[model_providers.%s]
name = %q
base_url = %q
wire_api = "responses"
`, stubProviderModel, stubProviderID, stubProviderID, stubProviderID, addr+"/v1")
}

// shellQuote quotes s as one POSIX shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
