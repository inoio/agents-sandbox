package journeys

import (
	"errors"
	"io/fs"
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

// The ordering refusal applies only to a TLS stub after seeding: a TLS stub
// before seeding, and a plain stub after it, both start.
func TestStepStartStubOAuthServerRefusesOnlyALateTLSStub(t *testing.T) {
	early := &Context{
		TB:      t,
		Agent:   claudeCodeAgentName,
		Harness: claudeCodeHarness,
		Home:    t.TempDir(),
		Project: t.TempDir(),
	}
	if err := StepStartStubOAuthServer().Run(early); err != nil || len(early.OAuth.CAPEM()) == 0 {
		t.Errorf("a TLS stub before seeding: err = %v, want an HTTPS stub", err)
	}
	homeFile := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(homeFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	unwritable := &Context{TB: t, Agent: codexAgentName, Harness: codexHarness, Home: homeFile}
	if err := StepStartStubOAuthServer().Run(unwritable); err == nil {
		t.Error("a declaration that cannot be written: the step reported no error")
	}
	plain := &Context{TB: t, Agent: codexAgentName, Harness: codexHarness, Home: t.TempDir(), Stub: &stubProvider{}}
	if err := StepStartStubOAuthServer().Run(plain); err != nil || plain.OAuth.CAPEM() != nil {
		t.Errorf("a plain stub after seeding: err = %v, want a plain-HTTP stub", err)
	}
	if err := StepStartStubOAuthServer().Run(&Context{TB: t, Agent: piAgentName, Harness: Harnesses[piAgentName]}); err == nil ||
		!strings.Contains(err.Error(), "declares no MCP facts") {
		t.Errorf("a harness without MCP facts: err = %v, want the missing-facts error", err)
	}
}

// declareMCPServer reports where a declaration fails: an unknown agent for
// snippets, an unwritable home, an unwritable project.
func TestDeclareMCPServerReportsFailures(t *testing.T) {
	const url = "https://host.microsandbox.internal:1/mcp"
	snippetsOnly := MCPFacts{Snippets: codexHarness.MCP.Snippets}
	if err := declareMCPServer(&Context{Agent: "no-such-agent", Home: t.TempDir()}, snippetsOnly, url); err == nil ||
		!strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("an unknown agent: err = %v", err)
	}
	homeFile := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(homeFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := declareMCPServer(&Context{Agent: codexAgentName, Home: homeFile}, snippetsOnly, url); err == nil {
		t.Error("a home that is a file: got no error")
	}
	if err := declareMCPServer(&Context{Agent: codexAgentName, Home: t.TempDir()}, snippetsOnly, url); err != nil {
		t.Errorf("snippets only, no project files: %v", err)
	}
	projectFile := filepath.Join(t.TempDir(), "project")
	if err := os.WriteFile(projectFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	projectOnly := MCPFacts{ProjectFiles: claudeCodeHarness.MCP.ProjectFiles}
	if err := declareMCPServer(
		&Context{Agent: claudeCodeAgentName, Project: projectFile},
		projectOnly,
		url,
	); err == nil ||
		!strings.Contains(err.Error(), "create the dir") || !errors.As(err, new(*fs.PathError)) {
		t.Errorf("a project that is a file: err = %v, want the wrapped dir error", err)
	}
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".mcp.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := declareMCPServer(&Context{Agent: claudeCodeAgentName, Project: project}, projectOnly, url); err == nil ||
		!strings.Contains(err.Error(), "write ") || !errors.As(err, new(*fs.PathError)) {
		t.Errorf("a project file that is a dir: err = %v, want the wrapped write error", err)
	}
}
