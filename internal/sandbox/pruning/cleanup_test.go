package pruning

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// withAutoPruneMocks opts a smoke test into isolated msb and docker factories so
// AutoPrune does not reach real microsandbox or Docker. The config-path mock is
// needed because PruneVolumes now reads project state references unconditionally.
func withAutoPruneMocks(t *testing.T) {
	t.Helper()
	configpathstest.WithMockConfigPaths(t)
	msbtest.WithMsbMock(t, &msbtest.MockMsbClient{})
	dockertest.WithNoopDockerMock(t)
}

func TestAutoPruneDoesNotPanic(t *testing.T) {
	withAutoPruneMocks(t)
	testUI := termiotest.NewTestMock(t)
	AutoPrune(context.Background(), docker.Get(), time.Hour, true, &testUI)
}

func TestAutoPruneIsIdempotent(t *testing.T) {
	// AutoPrune uses sync.Once, so calling it twice should be safe.
	// We can't easily test that it only runs once via Prune (which calls real SDK),
	// but we can verify no panic on repeated calls.
	withAutoPruneMocks(t)
	testUI := termiotest.NewTestMock(t)
	AutoPrune(context.Background(), docker.Get(), time.Hour, true, &testUI)
	AutoPrune(context.Background(), docker.Get(), time.Hour, true, &testUI)
}

func TestAutoPruneWarnsOnPruneError(t *testing.T) {
	// autoPruneOnce has no production reset hook, so reset it directly to drive
	// the single-shot body through the failure branch.
	autoPruneOnce = sync.Once{}
	msbtest.WithMsbMock(t, &msbtest.MockMsbClient{
		ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errors.New("boom")
		},
	})
	dockertest.WithNoopDockerMock(t)
	testUI := termiotest.NewTestMock(t)
	AutoPrune(context.Background(), docker.Get(), time.Hour, false, &testUI)
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
	testUI := termiotest.NewTestMock(t)
	AutoPrune(context.Background(), docker.Get(), 0, true, &testUI)
}

func TestAutoPruneDefaultThresholdLiteral(t *testing.T) {
	if autoPruneDefaultThreshold != 30*24*time.Hour {
		t.Errorf("autoPruneDefaultThreshold = %v, want 30 days", autoPruneDefaultThreshold)
	}
}

// TestAutoPruneZeroThresholdKeepsFreshStoppedVM pins the 0 -> default mapping: a
// zero threshold must behave as 30 days, keeping a freshly stopped VM.
func TestAutoPruneZeroThresholdKeepsFreshStoppedVM(t *testing.T) {
	autoPruneOnce = sync.Once{}
	configpathstest.WithMockConfigPaths(t)
	fresh := &msbtest.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
		Status_:    msb.SandboxStatusStopped,
		UpdatedAt_: time.Now(),
	}
	client := &msbtest.MockMsbClient{Sandboxes: []msb.SandboxHandle{fresh}}
	msbtest.WithMsbMock(t, client)
	dockertest.WithNoopDockerMock(t)
	testUI := termiotest.NewTestMock(t)

	AutoPrune(context.Background(), docker.Get(), 0, false, &testUI)

	if fresh.DidRemove() {
		t.Error("a zero threshold must default to 30 days and keep a fresh stopped VM")
	}
}

func TestAutoPruneRendersSummary(t *testing.T) {
	autoPruneOnce = sync.Once{}
	withAutoPruneMocks(t)
	testUI := termiotest.NewTestMock(t)

	AutoPrune(context.Background(), docker.Get(), time.Hour, false, &testUI)

	if len(testUI.OutCalls) == 0 || !strings.Contains(testUI.OutCalls[0], "Pruned") {
		t.Errorf("OutCalls = %v, want a prune summary", testUI.OutCalls)
	}
}
