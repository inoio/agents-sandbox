package vm

import (
	"context"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// setUpSandbox must resolve the agent from opts.Agent, not from the hardcoded
// default. An unknown agent must surface as an error early.
func TestSetUpSandboxRejectsUnknownAgent(t *testing.T) {
	ui := termio.NewTestMock(t)
	opts := options.RunOptions{Agent: "does-not-exist"}
	if _, err := setUpSandbox(context.Background(), nil, opts, nil, &ui, false, vmBootConnected); err == nil {
		t.Fatal("expected an error for an unknown --agent in setUpSandbox")
	}
}
