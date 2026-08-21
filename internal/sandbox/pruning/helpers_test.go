package pruning

import (
	"context"
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestInvokePruneFunc(t *testing.T) {
	t.Run("builds state and invokes the pruner", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-vm-proj-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
				},
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		var gotState PruneState
		err := InvokePruneFunc(context.Background(),
			func(_ context.Context, state PruneState, _ bool, _ termio.UI) (int, error) {
				gotState = state
				return 42, nil
			},
			7*24*time.Hour, false, ui)
		if err != nil {
			t.Fatalf("InvokePruneFunc: %v", err)
		}
		if _, ok := gotState.ToPrune["proj-1mjusbm3wikhb0"]; !ok {
			t.Errorf("pruner did not receive built PruneState, got %v", gotState)
		}
	})

	t.Run("propagates buildPruneState error", func(t *testing.T) {
		client := &msb.MockMsbClient{
			ListSandboxesFn: func(context.Context, map[string]string) ([]msb.SandboxHandle, error) {
				return nil, errors.New("boom")
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		invoked := false
		err := InvokePruneFunc(context.Background(),
			func(context.Context, PruneState, bool, termio.UI) (int, error) {
				invoked = true
				return 0, nil
			},
			time.Hour, false, ui)
		if err == nil {
			t.Fatal("expected error from buildPruneState")
		}
		if invoked {
			t.Error("pruner must not be invoked when state build fails")
		}
	})

	t.Run("propagates pruner error", func(t *testing.T) {
		msb.WithMsbMock(t, &msb.MockMsbClient{})
		ui := &termio.Mock{}
		err := InvokePruneFunc(context.Background(),
			func(context.Context, PruneState, bool, termio.UI) (int, error) {
				return 0, errors.New("prune boom")
			},
			time.Hour, false, ui)
		if err == nil || err.Error() != "prune boom" {
			t.Errorf("expected pruner error to propagate, got %v", err)
		}
	})
}
