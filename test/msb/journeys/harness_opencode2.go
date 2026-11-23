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
	StubConfig: func(addr string) map[string]string {
		return map[string]string{opencodeStubConfigTarget: opencodeProviderDocument(addr)}
	},
	HostConfig: func(addr string) map[string]string {
		return map[string]string{opencodeHostConfigRelPath: opencodeProviderDocument(addr)}
	},
	HostLoginStore: map[string]string{opencodeLoginStoreRelPath: `{"openai":{"type":"api"}}`},
	// opencode2's write tool takes the file as `path` (opencode 1.x named it
	// `filePath`); a wrong key is a tool error the turn survives, so only the
	// file check notices.
	WriteFileCall: func(guestPath, content string) ToolCall {
		return ToolCall{
			Name:      "write",
			Arguments: jsonObject(map[string]string{"path": guestPath, "content": content}),
		}
	},
	ResumeArgs: []string{"--continue"},
}

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
// that selects it, and a separate small model for side requests such as the
// session title.
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
	}
	data, err := json.Marshal(document)
	if err != nil {
		// The document is all marshalable literals, so this is unreachable.
		return "{}"
	}
	return string(data)
}
