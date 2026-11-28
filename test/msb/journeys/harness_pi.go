package journeys

import "github.com/inoio/agents-sandbox/test/msb/driver"

// The pi family's agent names: pi and its fork oh-my-pi.
const (
	piAgentName     = "pi"
	ohMyPiAgentName = "oh-my-pi"
)

// Where the pi family reads its custom providers, home-relative: pi a JSON
// models.json, oh-my-pi a YAML models.yml of the same shape. JSON is YAML, so
// both get the same document.
const (
	piModelsPath        = ".pi/agent/models.json"
	ohMyPiModelsPath    = ".omp/agent/models.yml"
	piMCPConfigPath     = ".pi/agent/mcp.json"
	ohMyPiMCPConfigPath = ".omp/agent/mcp.json"
)

// ohMyPiSetupSnippet is the launcher snippet oh-my-pi's journey settings go in;
// the launcher merges config*.yml snippets into its config.yml.
const ohMyPiSetupSnippet = "config-journeys.yml"

// ohMyPiSetupConfig turns off oh-my-pi's first-run setup wizard, names sessions
// through the title model role instead of a fork of the session's turn, and
// turns off its next-message predictions, so no side request runs as a turn on
// the main model.
const ohMyPiSetupConfig = `startup:
  setupWizard: false
title:
  generator: tiny
composer:
  predictions: false
`

// ohMyPiHarness is the pi-family adapter plus oh-my-pi's own start: its journey
// settings (ohMyPiSetupConfig), its "smol" role on the stub's small model, and,
// since a configured model removes the startup smoke's "no default model"
// marker, its status line as the readiness marker.
func ohMyPiHarness() Harness {
	harness := piFamilyHarness(ohMyPiModelsPath)
	harness.Login = &LoginFacts{
		BrowserCommand: "omp login openai-codex",
		CallbackMarker: "Authentication successful",
		StopKeys:       []byte{driver.CtrlC},
	}
	harness.MCP = &MCPFacts{
		HomeFiles: func(name, url string) map[string]string {
			return map[string]string{ohMyPiMCPConfigPath: piMCPConfig(name, url)}
		},
		LoginCommand: func(name string) string {
			return "omp -p " + shellQuote("/mcp reauth "+name)
		},
		// Print mode dispatches only /mcp reauth; /mcp test must run in the TUI.
		CallPrompt: func(name string) string { return "/mcp test " + name },
	}
	harness.Stub.Snippets = func(string) map[string]string {
		return map[string]string{ohMyPiSetupSnippet: ohMyPiSetupConfig}
	}
	harness.Drive.ReadyMarker = ohMyPiStatusLineMarker
	harness.Drive.Setup = &SetupScreen{Marker: ohMyPiSetupExitHint, ExitKeys: []byte{driver.CtrlC}}
	harness.Drive.LaunchArgs = append(harness.Drive.LaunchArgs, "--smol", stubProviderID+"/"+stubTitleModel)
	harness.Learn = &LearnFacts{
		// The seedable host config is the settings file. The provider and its
		// key stay in models.yml, which neither the seed nor the write-back
		// copies (so the managed config never holds the credential); the
		// read-back reads it from the stub's provisioned file.
		HostConfig: func(string) map[string]string {
			return map[string]string{ohMyPiConfigPath: ohMyPiSetupConfig}
		},
		HostLoginStore: map[string]string{ohMyPiDotenvPath: ohMyPiDotenvContent},
		// The read-back derives <PROVIDERID>_API_KEY from the stub provider's
		// literal apiKey in models.yml.
		CredentialEnvVar:  stubCredentialEnvVar,
		ManagedConfigKeys: []string{"setupWizard", "predictions"},
	}
	return harness
}

// piStartupBusyMarker is pi's notice for a prompt submitted while its startup
// (managed tools, extensions, the session) still runs; pi keeps the prompt.
const piStartupBusyMarker = "Startup is still in progress"

// piFamilyHarness points a pi-family harness at the stub: its models file
// declares an OpenAI-compatible provider at the stub (provisioned at
// modelsPath; neither file is a reserved home target), and --model selects it.
func piFamilyHarness(modelsPath string) Harness {
	return Harness{
		Stub: StubSetup{
			HomeFiles: func(addr string) map[string]string {
				return map[string]string{modelsPath: piModelsDocument(addr)}
			},
		},
		Login: &LoginFacts{
			BrowserCommand:     "pi auth login openai",
			CallbackParameters: map[string]string{"client_id": "journeys-client"},
			CallbackMarker:     "Authentication successful",
			StopKeys:           []byte{driver.CtrlC},
		},
		MCP: &MCPFacts{
			TLS: true,
			HomeFiles: func(name, url string) map[string]string {
				return map[string]string{piMCPConfigPath: piMCPConfig(name, url)}
			},
			LoginCommand: func(name string) string { return "pi mcp login " + shellQuote(name) },
		},
		Drive: Driving{
			LaunchArgs: []string{"--model", stubProviderID + "/" + stubProviderModel},
			WriteFileCall: func(guestPath, content string) ToolCall {
				return ToolCall{Name: "write", Arguments: writeToolArguments("path", guestPath, content)}
			},
			ResumeArgs:        []string{continueFlag},
			StartupBusyMarker: piStartupBusyMarker,
		},
	}
}

func piMCPConfig(name, url string) string {
	return mustJSON(map[string]any{
		"mcpServers": map[string]any{
			name: map[string]string{"type": "http", "url": url},
		},
	})
}

// piModelsDocument renders the models file that declares the stub provider at
// addr with its main and small models.
func piModelsDocument(addr string) string {
	return mustJSON(map[string]any{
		"providers": map[string]any{
			stubProviderID: map[string]any{
				"baseUrl": addr + "/v1",
				"api":     "openai-completions",
				"apiKey":  stubProviderAPIKey,
				"models":  []map[string]string{{"id": stubProviderModel}, {"id": stubTitleModel}},
			},
		},
	})
}

// oh-my-pi's settings file and its dotenv, home-relative. The host seed never
// selects the dotenv (its provision rules name only the settings file), so the
// exclusion check guards that choice against regression; the journey's dotenv
// holds no value, so the check can match its content.
const (
	ohMyPiConfigPath    = ".omp/agent/config.yml"
	ohMyPiDotenvPath    = ".omp/agent/.env"
	ohMyPiDotenvContent = "# oh-my-pi journeys dotenv\n"
)

// ohMyPiSetupExitHint is the key hint oh-my-pi's setup wizard shows on every
// scene; Ctrl+C leaves the whole wizard.
const ohMyPiSetupExitHint = "Ctrl+C exit setup"

// ohMyPiStatusLineMarker is the workspace segment of oh-my-pi's status line, which it
// draws once its editor is up; the hint beside it rotates, so it is no marker.
const ohMyPiStatusLineMarker = "📁 /workspace"
