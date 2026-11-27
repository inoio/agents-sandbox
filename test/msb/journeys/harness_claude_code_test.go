package journeys

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
)

func TestClaudeCodeStubEnvPointsTheMessagesEndpointAtTheStub(t *testing.T) {
	const addr = "http://stub.test:1234"
	env := claudeCodeHarness.Stub.Env(addr)
	if env["ANTHROPIC_BASE_URL"] != addr || env["ANTHROPIC_MODEL"] != stubProviderModel ||
		env["ANTHROPIC_SMALL_FAST_MODEL"] != stubTitleModel || env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != stubTitleModel ||
		env["ANTHROPIC_AUTH_TOKEN"] == "" {
		t.Errorf("env = %v, want the stub's base URL, a token, and its main and small models", env)
	}
	if !slices.Equal(claudeCodeHarness.Drive.LaunchArgs, []string{"--permission-mode", "acceptEdits"}) {
		t.Errorf("LaunchArgs = %v, want acceptEdits so Write runs without a prompt", claudeCodeHarness.Drive.LaunchArgs)
	}
}

// claude-code's Write tool takes file_path and content.
func TestClaudeCodeWriteFileCallUsesTheWriteTool(t *testing.T) {
	call := claudeCodeHarness.Drive.WriteFileCall("/workspace/a.txt", "hi")
	var arguments map[string]string
	if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
		t.Fatalf("arguments %q: %v", call.Arguments, err)
	}
	want := map[string]string{"file_path": "/workspace/a.txt", "content": "hi"}
	if call.Name != "Write" || !maps.Equal(arguments, want) {
		t.Errorf("call = %s(%v), want Write(%v)", call.Name, arguments, want)
	}
}
