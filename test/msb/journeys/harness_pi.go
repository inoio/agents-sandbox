package journeys

// The pi family's agent names: pi and its fork oh-my-pi.
const (
	piAgentName     = "pi"
	ohMyPiAgentName = "oh-my-pi"
)

// Where the pi family reads its custom providers, home-relative: pi a JSON
// models.json, oh-my-pi a YAML models.yml of the same shape. JSON is YAML, so
// both get the same document.
const (
	piModelsPath     = ".pi/agent/models.json"
	ohMyPiModelsPath = ".omp/agent/models.yml"
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
	harness.StubSnippets = func(string) map[string]string {
		return map[string]string{ohMyPiSetupSnippet: ohMyPiSetupConfig}
	}
	harness.ReadyMarker = ohMyPiReadyMarker
	harness.LaunchArgs = append(harness.LaunchArgs, "--smol", stubProviderID+"/"+stubTitleModel)
	return harness
}

// piFamilyHarness points a pi-family harness at the stub: its models file
// declares an OpenAI-compatible provider at the stub (provisioned at
// modelsPath; neither file is a reserved home target), and --model selects it.
func piFamilyHarness(modelsPath string) Harness {
	return Harness{
		StubConfig: func(addr string) map[string]string {
			return map[string]string{modelsPath: piModelsDocument(addr)}
		},
		LaunchArgs: []string{"--model", stubProviderID + "/" + stubProviderModel},
		WriteFileCall: func(guestPath, content string) ToolCall {
			return ToolCall{
				Name:      "write",
				Arguments: writeToolArguments("path", guestPath, content),
			}
		},
		ResumeArgs: []string{continueFlag},
	}
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

// ohMyPiReadyMarker is the workspace segment of oh-my-pi's status line, which it
// draws once its editor is up; the hint beside it rotates, so it is no marker.
const ohMyPiReadyMarker = "📁 /workspace"
