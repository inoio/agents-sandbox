package vm

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// SetUpSandbox consumes the run's already-resolved agent capabilities (the
// caller holds them) instead of re-resolving opts.Agent, so an opts.Agent the
// registry does not know — the resolution now happens at the run boundary — must
// not fail it.
func TestSetUpSandboxUsesTheThreadedResolvedAgent(t *testing.T) {
	healthyOpencodeDaemon(t)
	configpaths.WithMockConfigPaths(t)
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: msb.NewTestFS(nil, nil)}
	ui := termio.NewTestMock(t)

	if _, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "does-not-exist"},
		&reprovision.ConfigFiles{},
		&ui,
		false,
		VMBootConnected,
		mounts.WorkspaceMountPath,
	); err != nil {
		t.Fatalf("SetUpSandbox with a threaded resolved agent error = %v", err)
	}
}
