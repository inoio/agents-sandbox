package journeys

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configmerge"
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
		config := Harnesses[name].Stub.HomeFiles("http://stub.test:1")
		if _, ok := config[path]; !ok || len(config) != 1 {
			t.Errorf("%s Stub.HomeFiles = %v, want only %s", name, config, path)
		}
		args := Harnesses[name].Drive.LaunchArgs
		if model := slices.Index(args, "--model"); model < 0 || model+1 >= len(args) || args[model+1] != "stub/stub" {
			t.Errorf("%s LaunchArgs = %v, want --model stub/stub", name, args)
		}
	}
}

func TestOhMyPiSettingsSnippetMatchesItsPatternAndTurnsOffSideTurns(t *testing.T) {
	profile, ok := agent.Lookup(ohMyPiAgentName)
	if !ok {
		t.Fatal("oh-my-pi is not registered")
	}
	merger, ok := profile.(agent.ConfigMerger)
	if !ok {
		t.Fatal("oh-my-pi does not merge config snippets")
	}
	if !configmerge.SnippetNameMatches(merger.SnippetPattern(), ohMyPiSetupSnippet) {
		t.Errorf("snippet %q does not match oh-my-pi's pattern %q", ohMyPiSetupSnippet, merger.SnippetPattern())
	}
	if Harnesses[ohMyPiAgentName].Drive.ReadyMarker == "" {
		t.Error("the oh-my-pi adapter overrides no readiness marker; the smoke's needs an unconfigured model")
	}
	for _, want := range []string{"setupWizard: false", "generator: tiny", "predictions: false"} {
		if !strings.Contains(ohMyPiSetupConfig, want) {
			t.Errorf("config = %q, want %s", ohMyPiSetupConfig, want)
		}
	}
	if !slices.Contains(Harnesses[ohMyPiAgentName].Drive.LaunchArgs, "--smol") {
		t.Errorf("LaunchArgs = %v, want the smol role on the small model", Harnesses[ohMyPiAgentName].Drive.LaunchArgs)
	}
}
