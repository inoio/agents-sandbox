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
	StubConfig: func(string) map[string]string {
		return map[string]string{codexAuthPath: jsonObject(map[string]string{"OPENAI_API_KEY": stubProviderAPIKey})}
	},
	StubSnippets: func(addr string) map[string]string {
		return map[string]string{codexStubSnippet: codexStubConfig(addr)}
	},
	// codex writes through its exec_command tool, a shell command line.
	WriteFileCall: func(guestPath, content string) ToolCall {
		command := "printf %s " + shellQuote(content) + " > " + shellQuote(guestPath)
		return ToolCall{Name: "exec_command", Arguments: jsonObject(map[string]string{"cmd": command})}
	},
	ResumeArgs: []string{"resume", "--last"},
}

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
