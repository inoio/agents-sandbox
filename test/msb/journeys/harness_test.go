package journeys

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// TestEveryDrivenHarnessHasAnAdapter pins that a journey can only list a
// harness whose adapter tells its steps how to point it at the stub.
func TestEveryDrivenHarnessHasAnAdapter(t *testing.T) {
	for _, journey := range Journeys {
		for _, name := range journey.Harnesses {
			adapter, ok := Harnesses[name]
			if !ok {
				t.Errorf("journey %q drives %q, which has no adapter in Harnesses", journey.Name, name)
				continue
			}
			if adapter.Stub.HomeFiles == nil && adapter.Stub.Env == nil && adapter.Stub.Snippets == nil {
				t.Errorf("the %s adapter has no route to the stub", name)
			}
			if journey.LearnMode && adapter.Learn == nil {
				t.Errorf("learn journey %q drives %q, whose adapter declares no learn facts", journey.Name, name)
			}
		}
	}
}

// Each MCP adapter declares the server where its harness reads it: snippet
// names its launcher merges, or project files.
func TestMCPAdaptersDeclareTheServerTheHarnessesWay(t *testing.T) {
	const url = "https://host.microsandbox.internal:1/mcp"
	for name, adapter := range Harnesses {
		if adapter.MCP == nil {
			continue
		}
		mcp := adapter.MCP
		if (mcp.LoginArgs == nil) == (mcp.LoginCommand == nil) {
			t.Errorf("%s: declare exactly one of LoginArgs and LoginCommand", name)
		}
		declared := false
		if mcp.Snippets != nil {
			profile, _ := agent.Lookup(name)
			merger, ok := profile.(agent.ConfigMerger)
			if !ok {
				t.Errorf("%s declares MCP snippets but merges none", name)
				continue
			}
			for file, content := range mcp.Snippets(stubMCPServerName, url) {
				if matched, err := filepath.Match(merger.SnippetPattern(), file); err != nil || !matched {
					t.Errorf("%s: snippet %q does not match %q", name, file, merger.SnippetPattern())
				}
				declared = declared || strings.Contains(content, url)
			}
		}
		if mcp.ProjectFiles != nil {
			for _, content := range mcp.ProjectFiles(stubMCPServerName, url) {
				declared = declared || strings.Contains(content, url)
			}
		}
		if !declared {
			t.Errorf("%s: no snippet or project file names the server's URL", name)
		}
	}
}
