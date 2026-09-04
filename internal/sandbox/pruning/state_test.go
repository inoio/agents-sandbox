package pruning

import (
	"context"
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

var errBoom = errors.New("boom")

func TestBuildPruneState(t *testing.T) {
	recent := time.Now().Add(-5 * time.Minute)
	stale := time.Now().Add(-15 * 24 * time.Hour)

	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// running -> never stale (kept)
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj1-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: recent,
			},
			// stopped but fresh -> not yet stale (kept)
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj2-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: recent,
			},
			// stale stopped -> prunable
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj3-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: stale,
			},
			// task sandbox -> prunable regardless of age (slug "fill")
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-task-fill-proj",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: recent,
			},
			// running task -> never stale (slug "fill2")
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-task-fill2-proj",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: stale,
			},
		},
	}
	msb.WithMsbMock(t, client)

	pruneState, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}]; ok {
		t.Error("running VM must not be prunable")
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj2-1mjusbm3wikhb0", Agent: ""}]; ok {
		t.Error("fresh stopped VM must not be prunable")
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj3-1mjusbm3wikhb0", Agent: ""}]; !ok {
		t.Error("stale stopped VM must be prunable")
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "fill", Agent: ""}]; !ok {
		t.Error("stopped task sandbox must be prunable regardless of age")
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "fill2", Agent: ""}]; ok {
		t.Error("running task sandbox must not be prunable")
	}
	// Kept VMs: live project VMs (running or not-yet-stale) are kept; a pruned VM
	// and task sandboxes never count as kept.
	if _, ok := pruneState.ToKeep[state.Key{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}]; !ok {
		t.Error("running VM must be a kept VM")
	}
	if _, ok := pruneState.ToKeep[state.Key{Slug: "proj2-1mjusbm3wikhb0", Agent: ""}]; !ok {
		t.Error("fresh stopped VM must be a kept VM")
	}
	if _, ok := pruneState.ToKeep[state.Key{Slug: "proj3-1mjusbm3wikhb0", Agent: ""}]; ok {
		t.Error("stale stopped VM must not be a kept VM")
	}
	if _, ok := pruneState.ToKeep[state.Key{Slug: "fill", Agent: ""}]; ok {
		t.Error("task sandbox must not be a kept VM")
	}
	if _, ok := pruneState.ToKeep[state.Key{Slug: "fill2", Agent: ""}]; ok {
		t.Error("running task sandbox must not be a kept VM")
	}
}

func TestBuildPruneState_AgeZero(t *testing.T) {
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now(),
			},
		},
	}
	msb.WithMsbMock(t, client)

	pruneState, err := buildPruneState(context.Background(), 0)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: ""}]; !ok {
		t.Error("age 0 must make any stopped VM prunable")
	}
}

func TestBuildPruneState_SkipsUnparseableName(t *testing.T) {
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// Vm-prefixed but with no slug remainder -> must be skipped.
			&msb.MockSandboxHandle{
				Name_:      "agents-sandbox-vm-",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
			},
		},
	}
	msb.WithMsbMock(t, client)
	pruneState, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if len(pruneState.ToPrune)+len(pruneState.ToKeep) != 0 {
		t.Errorf("unparseable sandbox must not appear in PruneState, got %v", pruneState)
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
