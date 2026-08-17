package pruning

import (
	"context"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestPruneVMs(t *testing.T) {
	stale := time.Now().Add(-15 * 24 * time.Hour)
	recent := time.Now().Add(-5 * time.Minute)

	t.Run("prunes stale vm and stopped task, skips running and fresh", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-vm-proj1-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: stale,
				},
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-vm-proj2-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: recent,
				},
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-vm-proj3-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusRunning,
					UpdatedAt_: stale,
				},
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-task-fill-proj",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: stale,
				},
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-task-fill-proj2",
					Status_:    msbSdk.SandboxStatusRunning,
					UpdatedAt_: stale,
				},
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneVMs(context.Background(), LiveState{}, 24*time.Hour, false, ui)
		if err != nil {
			t.Fatalf("PruneVMs: %v", err)
		}
		// stale VM + stopped task pruned; fresh VM, running VM, running task kept
		if r.VMsPruned != 2 {
			t.Errorf("VMsPruned = %d, want 2 (stale vm + stopped task)", r.VMsPruned)
		}
		want := []string{"opencode-sandbox-vm-proj1-1mjusbm3wikhb0", "opencode-sandbox-task-fill-proj"}
		if len(client.RemovedSandboxes) != len(want) {
			t.Fatalf("RemovedSandboxes = %v, want %v", client.RemovedSandboxes, want)
		}
		for i, n := range want {
			if client.RemovedSandboxes[i] != n {
				t.Errorf("RemovedSandboxes[%d] = %q, want %q", i, client.RemovedSandboxes[i], n)
			}
		}
	})

	t.Run("dry run counts but does not delete", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-vm-proj1-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: stale,
				},
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneVMs(context.Background(), LiveState{}, 24*time.Hour, true, ui)
		if err != nil {
			t.Fatalf("PruneVMs: %v", err)
		}
		if r.VMsPruned != 1 {
			t.Errorf("VMsPruned = %d, want 1", r.VMsPruned)
		}
		if len(client.RemovedSandboxes) != 0 {
			t.Errorf("dry run removed sandboxes: %v", client.RemovedSandboxes)
		}
	})

	t.Run("zero threshold prunes any stopped", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Sandboxes: []msb.SandboxHandle{
				&msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-vm-proj1-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: recent,
				},
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneVMs(context.Background(), LiveState{}, 0, false, ui)
		if err != nil {
			t.Fatalf("PruneVMs: %v", err)
		}
		if r.VMsPruned != 1 {
			t.Errorf("VMsPruned = %d, want 1 (age 0 = no wait)", r.VMsPruned)
		}
	})
}
