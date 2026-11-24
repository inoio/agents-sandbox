package journeys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// StepStartStubOAuthServer starts the stub authorization server with its MCP
// server, and declares that MCP server in the harness's config through the
// adapter's Login.MCPServer snippets.
func StepStartStubOAuthServer() Step {
	return Step{
		Name: "an MCP server behind an OAuth authorization server is declared for the harness",
		Run: func(c *Context) error {
			mcp, err := mcpFacts(c)
			if err != nil {
				return err
			}
			stub, err := StartStubOAuthServer(c.TB, mcp.TLS)
			if err != nil {
				return err
			}
			c.OAuth = stub
			return declareMCPServer(c, mcp, stub.MCPURL())
		},
	}
}

// declareMCPServer declares the stub MCP server at url the harness's way: its
// launcher snippets and its project files (the adapter's MCP facts).
func declareMCPServer(c *Context, mcp MCPFacts, url string) error {
	if mcp.Snippets != nil {
		profile, ok := agent.Lookup(c.Agent)
		if !ok {
			return fmt.Errorf("unknown agent %q", c.Agent)
		}
		if err := driver.SeedAgentSnippets(
			c.Home,
			profile.ConfigDirName(),
			mcp.Snippets(stubMCPServerName, url),
		); err != nil {
			return err
		}
	}
	if mcp.ProjectFiles == nil {
		return nil
	}
	for rel, content := range mcp.ProjectFiles(stubMCPServerName, url) {
		if err := writeProjectFile(c.ProjectDir(), rel, content); err != nil {
			return err
		}
	}
	return nil
}

// writeProjectFile writes content to rel under the project dir.
func writeProjectFile(project, rel, content string) error {
	path := filepath.Join(project, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create the dir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// mcpFacts returns the harness's MCP facts, or an error naming a harness the
// MCP sign-in journey cannot cover yet.
func mcpFacts(c *Context) (MCPFacts, error) {
	if c.Harness.MCP == nil {
		return MCPFacts{}, fmt.Errorf("the %s adapter declares no MCP facts", c.Agent)
	}
	return *c.Harness.MCP, nil
}

// StepLaunchMCPLogin starts the harness's sign-in to the stub's MCP server (the
// adapter's MCP.LoginArgs) under `agents-sandbox run`.
func StepLaunchMCPLogin() Step {
	return Step{Name: "the user signs the harness in to the MCP server", Run: func(c *Context) error {
		mcp, err := mcpFacts(c)
		if err != nil {
			return err
		}
		if mcp.LoginCommand == nil {
			launchClient(c, []string{runCommand}, mcp.LoginArgs(stubMCPServerName))
			return nil
		}
		launchClient(c, []string{"shell"}, nil)
		ses := c.Session
		if err := ses.WaitForMarker(shellPromptMarker, scaled(loginLinkTimeout)); err != nil {
			return err
		}
		// One write: the command must run once, and the shell exits with its
		// status, which StepAssertMCPLoginCompleted reads.
		return ses.Write([]byte(mcp.LoginCommand(stubMCPServerName) + "; exit\r"))
	}}
}

// shellPromptMarker is part of the VM shell's prompt (dev@<vm>), which shows
// once the shell reads input.
const shellPromptMarker = "dev@"

// StepAssertMCPLoginCompleted checks the MCP sign-in finished: the login
// command exited 0 and the stub's token endpoint issued a token, which it does
// only for a code whose PKCE verifier matches.
func StepAssertMCPLoginCompleted() Step {
	return Step{Name: "the MCP sign-in completes with a token exchange in the VM", Run: func(c *Context) error {
		if c.OAuth == nil {
			return errors.New("no stub authorization server on the context")
		}
		ses, err := session(c)
		if err != nil {
			return err
		}
		code, err := ses.WaitExit(scaled(loginExitTimeout))
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("the MCP sign-in exited %d; output: %s", code, ses.OutputTail())
		}
		if len(c.OAuth.Issued()) == 0 {
			return fmt.Errorf("the stub issued no token (requests: %+v)", c.OAuth.Requests())
		}
		// Keep the sign-in's tokens and record only the calls after it, so a
		// later check sees the stored login in use, not the sign-in's own call.
		c.SignInTokens = c.OAuth.Issued()
		c.OAuth.ResetAuthorizedMCPCalls()
		return nil
	}}
}

// mcpCallTimeout bounds how long the harness may take to call its MCP server
// after it started.
const mcpCallTimeout = 30 * time.Second

// StepAssertMCPCalledWithToken checks that, after the sign-in completed, the
// harness called the stub's MCP server with a token the sign-in issued: the
// stored MCP login is in use, not a later login or refresh.
func StepAssertMCPCalledWithToken() Step {
	return AsCheck(Step{Name: "the harness calls the MCP server with the sign-in's token", Run: func(c *Context) error {
		if c.OAuth == nil || len(c.SignInTokens) == 0 {
			return errors.New("no completed MCP sign-in on the context")
		}
		return pollUntil(scaled(mcpCallTimeout), stubPollInterval, func() (bool, error) {
			return slices.ContainsFunc(c.OAuth.AuthorizedMCPCalls(), func(token string) bool {
				return slices.Contains(c.SignInTokens, token)
			}), nil
		}, func() error {
			return fmt.Errorf(
				"no MCP call carried the sign-in's token %v (calls with issued tokens: %v; requests: %+v)",
				c.SignInTokens,
				c.OAuth.AuthorizedMCPCalls(),
				c.OAuth.Requests(),
			)
		})
	}})
}
