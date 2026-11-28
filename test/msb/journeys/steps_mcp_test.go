package journeys

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

func TestStepLaunchMCPLoginBypassesOpenCodeAttachCommands(t *testing.T) {
	for name, command := range map[string]string{
		opencodeAgentName:  "opencode mcp auth 'journeys'",
		opencode2AgentName: "opencode2 mcp auth 'journeys' --server http://127.0.0.1:4096",
	} {
		t.Run(name, func(t *testing.T) {
			c := shellLoginContext(t, name, command)
			if err := StepLaunchMCPLogin().Run(c); err != nil {
				t.Fatal(err)
			}
			assertShellLoginExited(t, c)
		})
	}
}

func TestHermesMCPLoginEnablesTheSandboxBrowserShim(t *testing.T) {
	c := shellLoginContext(t, hermesAgentName, "DISPLAY=:journeys hermes mcp login 'journeys'")
	if err := StepLaunchMCPLogin().Run(c); err != nil {
		t.Fatal(err)
	}
	assertShellLoginExited(t, c)
}

func TestMCPConnectionCheckDrivesTheLazyHarnessInTheActiveTUI(t *testing.T) {
	script := "printf 'ready\\n'; IFS= read -r command; [ \"$command\" = '/mcp test journeys' ]"
	ses := driver.MustStartUnderPTY(t, t.TempDir(), []string{"PATH=/usr/bin:/bin"}, "/bin/sh", "-c", script)
	if err := ses.WaitForMarker("ready", time.Second); err != nil {
		t.Fatal(err)
	}
	c := &Context{TB: t, Agent: ohMyPiAgentName, Harness: Harnesses[ohMyPiAgentName], Session: ses}
	if err := StepCallMCPServer().Run(c); err != nil {
		t.Fatal(err)
	}
	if code, err := ses.WaitExit(time.Second); err != nil || code != 0 {
		t.Fatalf("TUI command exited %d (%v); output: %s", code, err, ses.OutputTail())
	}
	if c.Session != ses || len(c.Clients) != 0 {
		t.Fatal("the connection check must use the active TUI without starting a shell client")
	}
}

func TestMCPPersistenceDiscardsCallsMadeBeforeTheVMStopped(t *testing.T) {
	c := &Context{OAuth: &StubOAuthServer{authorized: []string{"before-stop"}}, SignInTokens: []string{"before-stop"}}
	if err := StepResetMCPCalls().Run(c); err != nil {
		t.Fatal(err)
	}
	if calls := c.OAuth.AuthorizedMCPCalls(); len(calls) != 0 {
		t.Fatalf("calls retained after stop: %v", calls)
	}
	if len(c.SignInTokens) != 1 || c.SignInTokens[0] != "before-stop" {
		t.Fatalf("reset lost the sign-in token identity: %v", c.SignInTokens)
	}
	if err := StepResetMCPCalls().Run(&Context{}); err == nil {
		t.Fatal("reset passed without a completed sign-in")
	}
}

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

func TestStubSeedsIncludesMCPHomeFilesOnlyForOAuthJourneys(t *testing.T) {
	const (
		providerURL = "http://host.microsandbox.internal:4000"
		mcpURL      = "http://host.microsandbox.internal:4001/mcp"
	)
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: piAgentName, path: piMCPConfigPath},
		{name: ohMyPiAgentName, path: ohMyPiMCPConfigPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Context{
				Agent:   tc.name,
				Harness: Harnesses[tc.name],
				Stub:    &stubProvider{addr: providerURL},
				OAuth:   &StubOAuthServer{vmOrigin: "http://host.microsandbox.internal:4001"},
			}
			if content := stubSeeds(c).HomeManifest[tc.path]; !strings.Contains(content, mcpURL) {
				t.Errorf("home manifest %q = %q, want the MCP URL %q", tc.path, content, mcpURL)
			}

			c.OAuth = nil
			if _, included := stubSeeds(c).HomeManifest[tc.path]; included {
				t.Errorf("home manifest includes %q without an OAuth journey", tc.path)
			}
		})
	}
}

// A TLS stub must start before the home is seeded, or its CA never reaches
// the VM; the step refuses the wrong order.
func TestStepStartStubOAuthServerRefusesToRunAfterSeeding(t *testing.T) {
	// A temp home and project keep a mutant that skips the refusal from
	// writing its declaration into the package dir.
	c := &Context{
		TB: t, Agent: claudeCodeAgentName, Harness: claudeCodeHarness, Stub: &stubProvider{},
		Home: t.TempDir(), Project: t.TempDir(),
	}
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
	if err := StepStartStubOAuthServer().Run(&Context{TB: t, Agent: "no-mcp", Harness: Harness{}}); err == nil ||
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
