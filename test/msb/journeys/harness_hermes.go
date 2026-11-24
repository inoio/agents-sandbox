package journeys

import "fmt"

// hermesAgentName is hermes' registered agent name.
const hermesAgentName = "hermes"

// hermesStubSnippet is the launcher snippet that points hermes at the stub; the
// launcher merges config*.yaml snippets into ~/.hermes/config.yaml, which is
// the merged-config target and so cannot come through the home: manifest.
const hermesStubSnippet = "config-journeys.yaml"

// hermesHarness points hermes at the stub through its model block: a custom,
// OpenAI-compatible provider at the stub with the stub model and a dummy key.
// The telemetry block settles the shared-metrics opt-in, which would otherwise
// prompt before the REPL. hermes writes through write_file with path/content
// and continues the last session with --continue.
var hermesHarness = Harness{
	Stub: StubSetup{
		Snippets: func(addr string) map[string]string {
			return map[string]string{hermesStubSnippet: hermesStubConfig(addr)}
		},
	},
	Drive: Driving{
		// The startup smoke's banner marker shows before hermes' input line
		// takes keys, so a prompt typed then loses its Enter; the hint row
		// under the input line shows once it does.
		ReadyMarker: hermesInputHintMarker,
		WriteFileCall: func(guestPath, content string) ToolCall {
			return ToolCall{Name: "write_file", Arguments: writeToolArguments("path", guestPath, content)}
		},
		ResumeArgs: []string{continueFlag},
	},
}

// hermesInputHintMarker is the hint row hermes draws under its input line.
const hermesInputHintMarker = "/ commands · @ files"

// hermesStubConfig renders the config.yaml fragment that selects the stub at
// addr. %q stands in for YAML double-quoted string escaping, which holds for
// its inputs: an http URL and ASCII identifiers.
func hermesStubConfig(addr string) string {
	return fmt.Sprintf(`model:
  provider: custom
  default: %q
  base_url: %q
  api_key: %q
telemetry:
  shared_metrics:
    enabled: false
    send: false
    offer_version: 2
`, stubProviderModel, addr+"/v1", stubProviderAPIKey)
}
