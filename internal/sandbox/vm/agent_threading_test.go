package vm

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// SetUpSandbox must resolve the agent from opts.Agent, not from the hardcoded
// default. An unknown agent must surface as an error early.
func TestSetUpSandboxRejectsUnknownAgent(t *testing.T) {
	ui := termio.NewTestMock(t)
	opts := options.RunOptions{Agent: "does-not-exist"}
	if _, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		nil,
		opts,
		nil,
		&ui,
		false,
		VMBootConnected,
		mounts.WorkspaceMountPath,
	); err == nil {
		t.Fatal("expected an error for an unknown --agent in SetUpSandbox")
	}
}
