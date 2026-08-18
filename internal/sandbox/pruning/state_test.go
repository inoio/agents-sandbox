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

func TestBuildPruneState(t *testing.T) {
	recent := time.Now().Add(-5 * time.Minute)
	stale := time.Now().Add(-15 * 24 * time.Hour)

	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// running -> never stale (kept)
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-proj1-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: recent,
			},
			// stopped but fresh -> not yet stale (kept)
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-proj2-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: recent,
			},
			// stale stopped -> prunable
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-proj3-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: stale,
			},
			// task sandbox -> prunable regardless of age (slug "fill")
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-task-fill-proj",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: recent,
			},
			// running task -> never stale (slug "fill2")
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-task-fill2-proj",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: stale,
			},
		},
	}
	msb.WithMsbMock(t, client)

	state, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if _, ok := state["proj1-1mjusbm3wikhb0"]; ok {
		t.Error("running VM must not be prunable")
	}
	if _, ok := state["proj2-1mjusbm3wikhb0"]; ok {
		t.Error("fresh stopped VM must not be prunable")
	}
	if _, ok := state["proj3-1mjusbm3wikhb0"]; !ok {
		t.Error("stale stopped VM must be prunable")
	}
	if _, ok := state["fill"]; !ok {
		t.Error("stopped task sandbox must be prunable regardless of age")
	}
	if _, ok := state["fill2"]; ok {
		t.Error("running task sandbox must not be prunable")
	}
}

func TestBuildPruneState_AgeZero(t *testing.T) {
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-proj-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now(),
			},
		},
	}
	msb.WithMsbMock(t, client)

	state, err := buildPruneState(context.Background(), 0)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if _, ok := state["proj-1mjusbm3wikhb0"]; !ok {
		t.Error("age 0 must make any stopped VM prunable")
	}
}

func TestBuildPruneState_SkipsUnparseableName(t *testing.T) {
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// Vm-prefixed but with no slug remainder -> must be skipped.
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
			},
		},
	}
	msb.WithMsbMock(t, client)
	state, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if len(state) != 0 {
		t.Errorf("unparseable sandbox must not appear in PruneState, got %v", state)
	}
}

func TestBuildPruneState_ListError(t *testing.T) {
	client := &msb.MockMsbClient{
		ListSandboxesFn: func(_ context.Context, _ map[string]string) ([]msb.SandboxHandle, error) {
			return nil, errBoom
		},
	}
	msb.WithMsbMock(t, client)
	_, err := buildPruneState(context.Background(), time.Hour)
	if err == nil {
		t.Fatal("expected error from ListSandboxes")
	}
}
