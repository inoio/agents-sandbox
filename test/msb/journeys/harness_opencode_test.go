package journeys

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestOpenCodeMCPAdapterDeclaresRemoteServer(t *testing.T) {
	const url = "http://host.microsandbox.internal:4001/mcp"
	content := opencodeHarness.MCP.Snippets(stubMCPServerName, url)[opencodeMCPSnippet]
	if !strings.Contains(content, url) || !strings.Contains(content, `"type":"remote"`) {
		t.Errorf("OpenCode MCP snippet = %q, want a remote server at %s", content, url)
	}
	if got := opencodeHarness.MCP.LoginCommand(stubMCPServerName); got != "opencode mcp auth 'journeys'" {
		t.Errorf("OpenCode MCP login command = %q, want mcp auth %s", got, stubMCPServerName)
	}
}

func TestOpencodeCoreSessionUsesItsOwnWriteToolSchema(t *testing.T) {
	harness, ok := Harnesses["opencode"]
	if !ok || !slices.Contains(coreSessionJourney.Harnesses, "opencode") {
		t.Fatal("opencode has no adapter or core-session coverage")
	}
	call := harness.Drive.WriteFileCall("/workspace/a.txt", "hi")
	var arguments map[string]string
	if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"filePath": "/workspace/a.txt", "content": "hi"}
	if call.Name != "write" || !maps.Equal(arguments, want) {
		t.Errorf("call = %s(%v), want write(%v)", call.Name, arguments, want)
	}
}

func TestOpencodeCoreSessionSharesProviderConfigButNotDaemonFacts(t *testing.T) {
	harness, ok := Harnesses["opencode"]
	if !ok {
		t.Fatal("opencode has no adapter")
	}
	const addr = "http://stub.test:1234"
	files := harness.Stub.HomeFiles(addr)
	if files[opencodeStubConfigTarget] != opencodeProviderDocument(addr) {
		t.Fatal("opencode does not use the shared OpenAI-compatible provider config")
	}
	if !slices.Equal(harness.Drive.ResumeArgs, []string{continueFlag}) {
		t.Fatal("opencode does not continue its persisted session")
	}
	if harness.Drive.Diagnostics != "" || harness.Learn != nil || harness.Login != nil {
		t.Fatal("opencode inherited unverified opencode2-only daemon, login, or learn facts")
	}
}
