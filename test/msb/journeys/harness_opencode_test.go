package journeys

import (
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
