package journeys

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestPiModelsDocumentDeclaresTheStubProvider(t *testing.T) {
	var document struct {
		Providers map[string]struct {
			BaseURL string              `json:"baseUrl"`
			API     string              `json:"api"`
			APIKey  string              `json:"apiKey"`
			Models  []map[string]string `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(piModelsDocument("http://stub.test:1234")), &document); err != nil {
		t.Fatal(err)
	}
	provider := document.Providers[stubProviderID]
	if provider.BaseURL != "http://stub.test:1234/v1" || provider.API != "openai-completions" || provider.APIKey == "" {
		t.Errorf("provider = %+v, want an OpenAI-compatible provider at the stub", provider)
	}
	var ids []string
	for _, model := range provider.Models {
		ids = append(ids, model["id"])
	}
	if !slices.Equal(ids, []string{stubProviderModel, stubTitleModel}) {
		t.Errorf("models = %v, want the main and the small model", ids)
	}
}

// pi and oh-my-pi share the adapter but read different models files.
func TestPiFamilyAdaptersProvisionTheirOwnModelsFile(t *testing.T) {
	for name, path := range map[string]string{piAgentName: piModelsPath, ohMyPiAgentName: ohMyPiModelsPath} {
		config := Harnesses[name].StubConfig("http://stub.test:1")
		if _, ok := config[path]; !ok || len(config) != 1 {
			t.Errorf("%s StubConfig = %v, want only %s", name, config, path)
		}
		if !slices.Equal(Harnesses[name].LaunchArgs[:2], []string{"--model", "stub/stub"}) {
			t.Errorf("%s LaunchArgs = %v, want --model stub/stub first", name, Harnesses[name].LaunchArgs)
		}
	}
}

func TestOhMyPiSettingsSnippetMatchesItsPatternAndTurnsOffSideTurns(t *testing.T) {
	profile, _ := agent.Lookup(ohMyPiAgentName)
	merger, ok := profile.(agent.ConfigMerger)
	if !ok {
		t.Fatal("oh-my-pi does not merge config snippets")
	}
	if matched, err := filepath.Match("config*.yml", ohMyPiSetupSnippet); err != nil || !matched ||
		merger.SnippetPattern() != "config*.{yml,yaml}" {
		t.Errorf("snippet %q against pattern %q", ohMyPiSetupSnippet, merger.SnippetPattern())
	}
	for _, want := range []string{"setupWizard: false", "generator: tiny", "predictions: false"} {
		if !strings.Contains(ohMyPiSetupConfig, want) {
			t.Errorf("config = %q, want %s", ohMyPiSetupConfig, want)
		}
	}
	if !slices.Contains(Harnesses[ohMyPiAgentName].LaunchArgs, "--smol") {
		t.Errorf("LaunchArgs = %v, want the smol role on the small model", Harnesses[ohMyPiAgentName].LaunchArgs)
	}
}
