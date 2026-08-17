package pruning

import (
	"context"
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

var errBoom = errors.New("boom")

func TestBuildLiveState(t *testing.T) {
	recent := time.Now().Add(-5 * time.Minute)
	stale := time.Now().Add(-15 * 24 * time.Hour)

	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// running -> active + all
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-proj1-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: recent,
				Image_:     "opencode-sandbox/runner-proj1-1mjusbm3wikhb0:abc123",
			},
			// stopped but fresh -> all only (kept for restart)
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-proj2-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: recent,
				Image_:     "opencode-sandbox/runner-proj2-1mjusbm3wikhb0:def456",
			},
			// stale stopped -> neither (will be pruned)
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-proj3-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: stale,
			},
			// task sandbox -> never a keep-set member
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-task-fill-proj",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: stale,
			},
		},
	}

	snap, err := BuildLiveState(context.Background(), client, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("BuildLiveState: %v", err)
	}
	if got := snap.ActiveVMs["proj1-1mjusbm3wikhb0"]; got != "abc123" {
		t.Errorf("ActiveVMs[proj1] = %q, want abc123", got)
	}
	if snap.AllVMs["proj1-1mjusbm3wikhb0"] != true {
		t.Error("AllVMs[proj1] = false, want true")
	}
	if snap.AllVMs["proj2-1mjusbm3wikhb0"] != true {
		t.Error("AllVMs[proj2] = false, want true")
	}
	if snap.AllVMs["proj3-1mjusbm3wikhb0"] {
		t.Error("AllVMs[proj3] = true, want false (stale)")
	}
	if snap.ActiveVMs["proj2-1mjusbm3wikhb0"] != "" {
		t.Errorf("ActiveVMs[proj2] = %q, want empty (not running)", snap.ActiveVMs["proj2-1mjusbm3wikhb0"])
	}
	if _, ok := snap.ActiveVMs["fill-proj"]; ok {
		t.Error("task sandbox must not be an ActiveVM")
	}
}

func TestBuildLiveState_ListError(t *testing.T) {
	client := &msb.MockMsbClient{ListSandboxesFn: func(_ context.Context) ([]msb.SandboxHandle, error) {
		return nil, errBoom
	}}
	_, err := BuildLiveState(context.Background(), client, time.Hour)
	if err == nil {
		t.Fatal("expected error from ListSandboxes")
	}
}
