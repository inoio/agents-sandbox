package journeys

import "encoding/json"

// opencode2AgentName is the agent the learn-mode journeys drive: the
// S1-verified profile.
const opencode2AgentName = "opencode2"

const (
	// opencodeStubConfigTarget is the VM-home config file the stub provider is
	// provisioned to: opencode's middle-precedence config file (config.json <
	// opencode.json < opencode.jsonc), which the agent reads. It is not a
	// launcher snippet name, since a file under the launcher's agent config dir
	// would make firstrun count a managed layer and suppress the menu.
	opencodeStubConfigTarget = ".config/opencode/opencode.json"
	// opencodeHostConfigRelPath is the host config file a journey seeds into the
	// isolated HOME: opencode's higher-precedence .jsonc, so the host seed is
	// what the agent reads over the provisioned .json.
	opencodeHostConfigRelPath = ".config/opencode/opencode.jsonc"
	// opencodeLoginStoreRelPath is opencode's auth.json, a declared login
	// credential store the host seed must drop rather than copy.
	opencodeLoginStoreRelPath = ".local/share/opencode/auth.json"
	// opencodeProviderNPM is the SDK package opencode loads for an
	// OpenAI-compatible provider, so the provider is real enough for the agent
	// to issue a request at the baseURL.
	opencodeProviderNPM = "@ai-sdk/openai-compatible"
)

// opencode2Harness points opencode2 at the stub through its provider config.
var opencode2Harness = Harness{
	Stub: StubSetup{
		HomeFiles: func(addr string) map[string]string {
			return map[string]string{opencodeStubConfigTarget: opencodeProviderDocument(addr)}
		},
	},
	Drive: Driving{
		// opencode2's write tool takes the file as `path` (opencode 1.x named
		// it `filePath`); a wrong key is a tool error the turn survives, so only
		// the file check notices.
		WriteFileCall: func(guestPath, content string) ToolCall {
			return ToolCall{Name: "write", Arguments: writeToolArguments("path", guestPath, content)}
		},
		ResumeArgs:  []string{continueFlag},
		Diagnostics: opencode2Diagnostics,
	},
	Login: &LoginFacts{
		// OpenAI has several auth methods; pin the browser method so the CLI
		// never waits for a method-selection prompt.
		BrowserCommand: "opencode2 auth login openai --method chatgpt-browser --server http://127.0.0.1:4096",
		CallbackMarker: "Authorization successful",
	},
	MCP: &MCPFacts{
		// A launcher snippet merged into opencode's config declares the
		// remote server; `run` cannot pass a subcommand to the attach client,
		// so the sign-in runs in the shell.
		Snippets: func(name, url string) map[string]string {
			return map[string]string{opencodeMCPSnippet: mustJSON(map[string]any{
				"mcp": map[string]any{name: map[string]any{"type": "remote", "url": url}},
			})}
		},
		LoginCommand: func(name string) string {
			return "opencode2 mcp auth " + shellQuote(name) + " --server http://127.0.0.1:4096"
		},
		// opencode2 sends OAuth credentials only over TLS.
		TLS: true,
	},
	Learn: &LearnFacts{
		HostConfig: func(addr string) map[string]string {
			return map[string]string{opencodeHostConfigRelPath: opencodeProviderDocument(addr)}
		},
		HostLoginStore: map[string]string{opencodeLoginStoreRelPath: `{"openai":{"type":"api"}}`},
		// The read-back derives <PROVIDERID>_API_KEY from a literal
		// provider.<id>.options.apiKey, and a copied provider block keeps its
		// baseURL and npm keys.
		CredentialEnvVar:             stubCredentialEnvVar,
		ManagedConfigKeys:            []string{"baseURL", "npm"},
		ManagedConfigHoldsCredential: true,
	},
}

// opencodeMCPSnippet is the launcher snippet that declares the MCP sign-in
// journey's server in opencode's config.
const opencodeMCPSnippet = "opencode-journeys-mcp.json"

// jsonObject renders a string map as a JSON object.
func jsonObject(fields map[string]string) string {
	data, err := json.Marshal(fields)
	if err != nil {
		// A string map always marshals, so this is unreachable.
		return "{}"
	}
	return string(data)
}

// opencodeProviderDocument renders the opencode provider config that points the
// agent at addr: an OpenAI-compatible provider whose options.baseURL is
// addr+"/v1", an inline API key for the read-back to classify, the main model
// that selects it, a separate small model for side requests such as the
// session title, and the stub as the only enabled provider.
func opencodeProviderDocument(addr string) string {
	document := map[string]any{
		"provider": map[string]any{
			stubProviderID: map[string]any{
				"npm":     opencodeProviderNPM,
				"options": map[string]any{"baseURL": addr + "/v1", "apiKey": stubProviderAPIKey},
				"models":  map[string]any{stubProviderModel: map[string]any{}, stubTitleModel: map[string]any{}},
			},
		},
		"model":       stubProviderID + "/" + stubProviderModel,
		"small_model": stubProviderID + "/" + stubTitleModel,
		// Only the stub provider: opencode2 can give a new session its
		// built-in default model before the configured one is known; with
		// that provider disabled, such a turn fails at once and names the
		// model instead of retrying an unreachable provider for a minute (#881).
		"enabled_providers": []string{stubProviderID},
	}
	data, err := json.Marshal(document)
	if err != nil {
		// The document is all marshalable literals, so this is unreachable.
		return "{}"
	}
	return string(data)
}

// opencode2Diagnostics prints the serve daemon's sessions and the messages of
// each (up to three) from its API (basic auth with the baked password, each
// call bounded): what it recorded of the prompt, and whether a model turn
// started, streamed, or failed. Its log records no session events at its
// default level.
const opencode2Diagnostics = `A="-s -m 10 -u opencode:$OPENCODE_SERVER_PASSWORD"; U=http://127.0.0.1:4096/api; ` +
	`curl $A $U/session | head -c 3000; echo; ` +
	`for S in $(curl $A $U/session | grep -o '"id":"ses_[A-Za-z0-9]*"' | cut -d'"' -f4 | head -3); do ` +
	`echo "== $S"; curl $A "$U/session/$S/message" | head -c 6000; echo; done`
