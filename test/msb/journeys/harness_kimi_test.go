package journeys

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestKimiCoreSessionUsesTheStubAndResumesWithoutAskingForEdits(t *testing.T) {
	harness, ok := Harnesses["kimi-code"]
	if !ok || !slices.Contains(coreSessionJourney.Harnesses, "kimi-code") {
		t.Fatal("kimi-code has no adapter or core-session coverage")
	}
	const addr = "http://stub.test:1234"
	files := harness.Stub.Snippets(addr)
	var config struct {
		DefaultModel string `toml:"default_model"`
		Providers    map[string]struct {
			Type    string `toml:"type"`
			BaseURL string `toml:"base_url"`
			APIKey  string `toml:"api_key"`
		} `toml:"providers"`
		Models map[string]struct {
			Provider       string `toml:"provider"`
			Model          string `toml:"model"`
			MaxContextSize int    `toml:"max_context_size"`
		} `toml:"models"`
	}
	if len(files) != 1 {
		t.Fatal("the adapter must provide one config snippet")
	}
	for _, content := range files {
		if err := toml.Unmarshal([]byte(content), &config); err != nil {
			t.Fatal(err)
		}
	}
	provider := config.Providers[stubProviderID]
	model := config.Models[config.DefaultModel]
	if provider.Type != "openai" || provider.BaseURL != addr+"/v1" || provider.APIKey != stubProviderAPIKey ||
		model.Provider != stubProviderID || model.Model != stubProviderModel || model.MaxContextSize <= 0 {
		t.Fatal("the selected Kimi model does not reach the stub")
	}
	if !slices.Equal(harness.Drive.LaunchArgs, []string{"--auto"}) ||
		!slices.Equal(harness.Drive.ResumeArgs, []string{continueFlag}) {
		t.Fatal("the adapter does not allow edits or continue the last session")
	}
}

func TestKimiWriteToolSchema(t *testing.T) {
	harness, ok := Harnesses["kimi-code"]
	if !ok {
		t.Fatal("kimi-code has no adapter")
	}
	call := harness.Drive.WriteFileCall("/workspace/a.txt", "hi")
	var arguments map[string]string
	if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"path": "/workspace/a.txt", "content": "hi"}
	if call.Name != "Write" || !maps.Equal(arguments, want) {
		t.Errorf("call = %s(%v), want Write(%v)", call.Name, arguments, want)
	}
}
