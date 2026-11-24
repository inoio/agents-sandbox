package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claude-code's MCP server is declared in the project: the step writes the
// project file and the approving settings snippet.
func TestDeclareMCPServerWritesProjectFilesAndSnippets(t *testing.T) {
	c := &Context{TB: t, Agent: claudeCodeAgentName, Home: t.TempDir(), Project: t.TempDir()}
	const url = "https://host.microsandbox.internal:1/mcp"
	if err := declareMCPServer(c, *claudeCodeHarness.MCP, url); err != nil {
		t.Fatal(err)
	}
	project, err := os.ReadFile(filepath.Join(c.Project, ".mcp.json"))
	if err != nil || !strings.Contains(string(project), url) {
		t.Errorf(".mcp.json = %q (%v), want the server's URL", project, err)
	}
	matches, _ := filepath.Glob(filepath.Join(c.Home, "*", "*", "*", claudeCodeMCPSnippet))
	if len(matches) != 1 {
		t.Errorf("the approving snippet %s was not seeded under the home: %v", claudeCodeMCPSnippet, matches)
	}
}

// A TLS stub must start before the home is seeded, or its CA never reaches
// the VM; the step refuses the wrong order.
func TestStepStartStubOAuthServerRefusesToRunAfterSeeding(t *testing.T) {
	c := &Context{TB: t, Agent: claudeCodeAgentName, Harness: claudeCodeHarness, Stub: &stubProvider{}}
	if err := StepStartStubOAuthServer().Run(c); err == nil || !strings.Contains(err.Error(), "before seeding") {
		t.Errorf("after seeding: err = %v, want the ordering refusal", err)
	}
}
