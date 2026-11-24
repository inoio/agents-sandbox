package journeys

import (
	"errors"
	"fmt"
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
		Name: "an MCP server behind an OAuth authorization server is in the harness's config",
		Run: func(c *Context) error {
			login, err := loginFacts(c)
			if err != nil {
				return err
			}
			stub, err := StartStubOAuthServer(c.TB)
			if err != nil {
				return err
			}
			c.OAuth = stub
			profile, ok := agent.Lookup(c.Agent)
			if !ok {
				return fmt.Errorf("unknown agent %q", c.Agent)
			}
			return driver.SeedAgentSnippets(
				c.Home,
				profile.ConfigDirName(),
				login.MCPServer(stubMCPServerName, stub.MCPURL()),
			)
		},
	}
}

// StepLaunchMCPLogin starts the harness's sign-in to the stub's MCP server (the
// adapter's Login.MCPLoginArgs) under `agents-sandbox run`.
func StepLaunchMCPLogin() Step {
	return Step{Name: "the user signs the harness in to the MCP server", Run: func(c *Context) error {
		login, err := loginFacts(c)
		if err != nil {
			return err
		}
		launchClient(c, []string{runCommand}, login.MCPLoginArgs(stubMCPServerName))
		return nil
	}}
}

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
