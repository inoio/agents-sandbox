package vm

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestStartDockerdCtxDone covers the context-cancellation branch inside the
// dockerd readiness poll loop: when the context is cancelled, the poll aborts
// with the context error.
func TestStartDockerdCtxDone(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(true, 0, "", "", nil),
			dockerdReadyCmd:       msb.NewTestResult(false, 1, "", "", nil),
			dockerdRestartCmd:     msb.NewTestResult(true, 0, "", "", nil),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := startDockerdIfPresent(ctx, sb, &ui); err == nil {
		t.Fatal("expected an error when the context is cancelled during dockerd startup")
	}
	if err := ctx.Err(); err == nil {
		t.Fatal("expected the context to be cancelled")
	}
}
