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
// caller holds them) instead of re-resolving opts.Agent. The mismatched
// opts.Agent ("does-not-exist") is deliberate: production always passes the pair
// produced together at the run boundary, and this contradictory pair is the only
// way to prove SetUpSandbox reads the threaded value rather than opts.Agent —
// were it still resolving opts.Agent, an unknown name would fail here.
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
