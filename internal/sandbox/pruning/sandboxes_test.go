package pruning

import (
	"context"
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestPruneSandboxes(t *testing.T) {
	stale := time.Now().Add(-15 * 24 * time.Hour)

	stopped := &msb.MockSandboxHandle{
		Name_:      "opencode-sandbox-vm-proj1-1mjusbm3wikhb0",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: stale,
	}
	running := &msb.MockSandboxHandle{
		Name_:      "opencode-sandbox-vm-proj2-1mjusbm3wikhb0",
		Status_:    msbSdk.SandboxStatusRunning,
		UpdatedAt_: stale,
	}
	task := &msb.MockSandboxHandle{
		Name_:      "opencode-sandbox-task-fill-proj",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: stale,
	}

	t.Run("removes stopped sandboxes and skips running", func(t *testing.T) {
		client := &msb.MockMsbClient{}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		state := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{
				{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}: stopped,
				{Slug: "proj2-1mjusbm3wikhb0", Agent: ""}: running,
				{Slug: "fill", Agent: ""}:                 task,
			},
		}
		r, err := PruneSandboxes(context.Background(), state, false, ui)
		if err != nil {
			t.Fatalf("PruneSandboxes: %v", err)
		}
		if r.VMsPruned != 2 {
			t.Errorf("VMsPruned = %d, want 2 (stopped vm + task)", r.VMsPruned)
		}
		if len(client.RemovedSandboxes) != 2 {
			t.Fatalf("RemovedSandboxes = %v, want 2", client.RemovedSandboxes)
		}
		removed := map[string]bool{}
		for _, n := range client.RemovedSandboxes {
			removed[n] = true
		}
		for _, want := range []string{"opencode-sandbox-vm-proj1-1mjusbm3wikhb0", "opencode-sandbox-task-fill-proj"} {
			if !removed[want] {
				t.Errorf("RemovedSandboxes missing %q, got %v", want, client.RemovedSandboxes)
			}
		}
	})

	t.Run("dry run counts but does not delete", func(t *testing.T) {
		client := &msb.MockMsbClient{}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneSandboxes(
			context.Background(),
			PruneState{ToPrune: map[state.Key]msb.SandboxHandle{{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}: stopped}},
			true,
			ui,
		)
		if err != nil {
			t.Fatalf("PruneSandboxes: %v", err)
		}
		if r.VMsPruned != 1 {
			t.Errorf("VMsPruned = %d, want 1", r.VMsPruned)
		}
		if len(client.RemovedSandboxes) != 0 {
			t.Errorf("dry run removed sandboxes: %v", client.RemovedSandboxes)
		}
	})

	t.Run("empty state prunes nothing", func(t *testing.T) {
		client := &msb.MockMsbClient{}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneSandboxes(context.Background(), PruneState{}, false, ui)
		if err != nil {
			t.Fatalf("PruneSandboxes: %v", err)
		}
		if r.VMsPruned != 0 {
			t.Errorf("VMsPruned = %d, want 0", r.VMsPruned)
		}
		if len(client.RemovedSandboxes) != 0 {
			t.Errorf("RemovedSandboxes = %v, want none", client.RemovedSandboxes)
		}
	})

	t.Run("continues past a sandbox removal error", func(t *testing.T) {
		removed := make(map[string]bool)
		client := &msb.MockMsbClient{
			RemoveSandboxFn: func(_ context.Context, name string) error {
				if name == "opencode-sandbox-vm-proj1-1mjusbm3wikhb0" {
					return errors.New("boom")
				}
				removed[name] = true
				return nil
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		state := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{
				{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}: stopped,
				{Slug: "proj2-1mjusbm3wikhb0", Agent: ""}: &msb.MockSandboxHandle{
					Name_:      "opencode-sandbox-vm-proj2-1mjusbm3wikhb0",
					Status_:    msbSdk.SandboxStatusStopped,
					UpdatedAt_: stale,
				},
			},
		}
		r, err := PruneSandboxes(context.Background(), state, false, ui)
		if err != nil {
			t.Fatalf("PruneSandboxes: %v", err)
		}
		if r.VMsPruned != 1 {
			t.Errorf("VMsPruned = %d, want 1 (failed one is skipped)", r.VMsPruned)
		}
		if !removed["opencode-sandbox-vm-proj2-1mjusbm3wikhb0"] {
			t.Errorf("expected the non-failing sandbox to be removed, got %v", removed)
		}
		if len(ui.WarnCalls) != 1 {
			t.Errorf("WarnCalls = %v, want 1 warn about the failed removal", ui.WarnCalls)
		}
	})
}
