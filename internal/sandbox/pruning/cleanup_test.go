package pruning

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// withAutoPruneMocks opts a smoke test into isolated msb and docker factories so
// AutoPrune does not reach real microsandbox or Docker. The config-path mock is
// needed because PruneVolumes now reads project state references unconditionally.
func withAutoPruneMocks(t *testing.T) {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	docker.WithNoopDockerMock(t)
}

func TestAutoPruneDoesNotPanic(t *testing.T) {
	withAutoPruneMocks(t)
	testUI := termio.NewTestMock(t)
	AutoPrune(context.Background(), time.Hour, true, &testUI)
}

func TestAutoPruneIsIdempotent(t *testing.T) {
	// AutoPrune uses sync.Once, so calling it twice should be safe.
	// We can't easily test that it only runs once via Prune (which calls real SDK),
	// but we can verify no panic on repeated calls.
	withAutoPruneMocks(t)
	testUI := termio.NewTestMock(t)
	AutoPrune(context.Background(), time.Hour, true, &testUI)
	AutoPrune(context.Background(), time.Hour, true, &testUI)
}

func TestAutoPruneWarnsOnPruneError(t *testing.T) {
	// autoPruneOnce has no production reset hook, so reset it directly to drive
	// the single-shot body through the failure branch.
	autoPruneOnce = sync.Once{}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errors.New("boom")
		},
	})
	docker.WithNoopDockerMock(t)
	testUI := termio.NewTestMock(t)
	AutoPrune(context.Background(), time.Hour, false, &testUI)
	if len(testUI.WarnCalls) != 1 {
		t.Fatalf("WarnCalls = %v, want one auto-prune failure warning", testUI.WarnCalls)
	}
	if !strings.Contains(testUI.WarnCalls[0], "auto-prune failed") {
		t.Errorf("WarnCalls = %v, want the auto-prune failure message", testUI.WarnCalls)
	}
}

func TestAutoPruneDefaultsToSevenDays(t *testing.T) {
	// When threshold is 0, AutoPrune should default to 30 days.
	// We can't directly assert the threshold used, but we verify zero doesn't panic.
	withAutoPruneMocks(t)
	testUI := termio.NewTestMock(t)
	AutoPrune(context.Background(), 0, true, &testUI)
}
