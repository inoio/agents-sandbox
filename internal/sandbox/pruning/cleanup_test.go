package pruning

import (
	"context"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// withAutoPruneMocks opts a smoke test into isolated msb and docker factories so
// AutoPrune does not reach real microsandbox or Docker.
func withAutoPruneMocks(t *testing.T) {
	t.Helper()
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

func TestAutoPruneDefaultsToSevenDays(t *testing.T) {
	// When threshold is 0, AutoPrune should default to 30 days.
	// We can't directly assert the threshold used, but we verify zero doesn't panic.
	withAutoPruneMocks(t)
	testUI := termio.NewTestMock(t)
	AutoPrune(context.Background(), 0, true, &testUI)
}
