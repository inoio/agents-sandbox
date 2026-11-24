package journeys

import (
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestHermesStubConfigSelectsACustomProviderAtTheStub(t *testing.T) {
	profile, _ := agent.Lookup(hermesAgentName)
	merger, ok := profile.(agent.ConfigMerger)
	if !ok {
		t.Fatal("hermes does not merge config snippets")
	}
	if matched, err := filepath.Match(merger.SnippetPattern(), hermesStubSnippet); err != nil || !matched {
		t.Errorf(
			"snippet name %q does not match hermes' snippet pattern %q",
			hermesStubSnippet,
			merger.SnippetPattern(),
		)
	}

	var config struct {
		Model struct {
			Provider string `yaml:"provider"`
			Default  string `yaml:"default"`
			BaseURL  string `yaml:"base_url"`
			APIKey   string `yaml:"api_key"`
		} `yaml:"model"`
	}
	content := hermesHarness.Stub.Snippets("http://stub.test:1")[hermesStubSnippet]
	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		t.Fatalf("the snippet is not YAML: %v\n%s", err, content)
	}
	model := config.Model
	if model.Provider != "custom" || model.Default != stubProviderModel ||
		model.BaseURL != "http://stub.test:1/v1" || model.APIKey != stubProviderAPIKey {
		t.Errorf("model = %+v, want the custom provider at the stub with the stub model and key", model)
	}
}
