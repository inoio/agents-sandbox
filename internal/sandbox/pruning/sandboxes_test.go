package pruning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func stoppedHandle(name string) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Name_:      name,
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
	}
}

func TestRenderSandboxReport(t *testing.T) {
	ui := &termio.Mock{}
	RenderSandboxReport(ui, SandboxReport{
		VMsPruned: 1,
		Details: []StaleEntry{
			{Type: StaleTypeVM, Name: "agents-sandbox-vm-proj-1mjusbm3wikhb0", Slug: "proj-1mjusbm3wikhb0"},
		},
	}, false)
	if len(ui.OutCalls) != 1 || ui.OutCalls[0] != "Pruned 1 sandbox(es)" {
		t.Errorf("OutCalls = %v, want the summary", ui.OutCalls)
	}
	if len(ui.VerboseCalls) != 1 || !strings.Contains(ui.VerboseCalls[0], "agents-sandbox-vm-proj-1mjusbm3wikhb0") {
		t.Errorf("VerboseCalls = %v, want the detail line", ui.VerboseCalls)
	}

	empty := &termio.Mock{}
	RenderSandboxReport(empty, SandboxReport{}, true)
	if len(empty.OutCalls) != 1 || empty.OutCalls[0] != "dry-run: Would prune 0 sandbox(es)" {
		t.Errorf("OutCalls = %v, want the dry-run summary", empty.OutCalls)
	}
	if len(empty.VerboseCalls) != 0 {
		t.Errorf("VerboseCalls = %v, want none without details", empty.VerboseCalls)
	}
}

func TestPruneSandboxes(t *testing.T) {
	t.Run("removes inactive sandboxes by identity and skips running", func(t *testing.T) {
		stopped := stoppedHandle("agents-sandbox-vm-proj1-1mjusbm3wikhb0")
		running := &msb.MockSandboxHandle{
			Name_:      "agents-sandbox-vm-proj2-1mjusbm3wikhb0",
			Status_:    msbSdk.SandboxStatusRunning,
			UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
		}
		task := stoppedHandle("agents-sandbox-task-fill-proj")

		client := &msb.MockMsbClient{}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		pruneState := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{
				{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}: stopped,
				{Slug: "proj2-1mjusbm3wikhb0", Agent: ""}: running,
				{Slug: "fill", Agent: ""}:                 task,
			},
		}
		r, err := PruneSandboxes(context.Background(), pruneState, false, ui)
		if err != nil {
			t.Fatalf("PruneSandboxes: %v", err)
		}
		if r.VMsPruned != 2 {
			t.Errorf("VMsPruned = %d, want 2 (stopped vm + task)", r.VMsPruned)
		}
		if !stopped.DidRemove() || !task.DidRemove() {
			t.Errorf(
				"inactive sandboxes not removed via handle: stopped=%v task=%v",
				stopped.DidRemove(),
				task.DidRemove(),
			)
		}
		if running.DidRemove() {
			t.Errorf("running sandbox was removed")
		}
	})

	t.Run("dry run counts but does not delete", func(t *testing.T) {
		stopped := stoppedHandle("agents-sandbox-vm-proj1-1mjusbm3wikhb0")
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
		if stopped.DidRemove() {
			t.Errorf("dry run removed a sandbox")
		}
	})

	t.Run("skips active, transitional, and unknown statuses", func(t *testing.T) {
		starting := &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-starting",
			Status_: msbSdk.SandboxStatusStarting,
		}
		unknown := &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-unknown",
			Status_: msbSdk.SandboxStatus("future-status"),
		}
		client := &msb.MockMsbClient{}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		pruneState := PruneState{ToPrune: map[state.Key]msb.SandboxHandle{
			{Slug: "starting", Agent: ""}: starting,
			{Slug: "unknown", Agent: ""}:  unknown,
		}}
		r, err := PruneSandboxes(context.Background(), pruneState, false, ui)
		if err != nil {
			t.Fatalf("PruneSandboxes: %v", err)
		}
		if r.VMsPruned != 0 || starting.DidRemove() || unknown.DidRemove() {
			t.Fatalf("protected statuses were pruned: report=%+v", r)
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
	})

	t.Run("continues past a sandbox removal error", func(t *testing.T) {
		failing := stoppedHandle("agents-sandbox-vm-proj1-1mjusbm3wikhb0")
		failing.RemoveErr = errors.New("boom")
		ok := stoppedHandle("agents-sandbox-vm-proj2-1mjusbm3wikhb0")

		client := &msb.MockMsbClient{}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		pruneState := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{
				{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}: failing,
				{Slug: "proj2-1mjusbm3wikhb0", Agent: ""}: ok,
			},
		}
		r, err := PruneSandboxes(context.Background(), pruneState, false, ui)
		if err != nil {
			t.Fatalf("PruneSandboxes: %v", err)
		}
		if r.VMsPruned != 1 {
			t.Errorf("VMsPruned = %d, want 1 (failed one is skipped)", r.VMsPruned)
		}
		if !ok.DidRemove() {
			t.Errorf("expected the non-failing sandbox to be removed")
		}
		if len(ui.WarnCalls) != 1 {
			t.Errorf("WarnCalls = %v, want 1 warn about the failed removal", ui.WarnCalls)
		}
	})

	t.Run("skips replaced and already-gone sandboxes without warning", func(t *testing.T) {
		replaced := stoppedHandle("agents-sandbox-vm-proj1-1mjusbm3wikhb0")
		replaced.RemoveErr = &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"}
		gone := stoppedHandle("agents-sandbox-vm-proj2-1mjusbm3wikhb0")
		gone.RemoveErr = &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
		ok := stoppedHandle("agents-sandbox-vm-proj3-1mjusbm3wikhb0")

		client := &msb.MockMsbClient{}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		pruneState := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{
				{Slug: "proj1-1mjusbm3wikhb0", Agent: ""}: replaced,
				{Slug: "proj2-1mjusbm3wikhb0", Agent: ""}: gone,
				{Slug: "proj3-1mjusbm3wikhb0", Agent: ""}: ok,
			},
		}
		r, err := PruneSandboxes(context.Background(), pruneState, false, ui)
		if err != nil {
			t.Fatalf("PruneSandboxes: %v", err)
		}
		if r.VMsPruned != 1 {
			t.Errorf("VMsPruned = %d, want 1 (replaced and gone are skipped)", r.VMsPruned)
		}
		if !ok.DidRemove() {
			t.Errorf("expected the non-replaced sandbox to be removed")
		}
		if len(ui.WarnCalls) != 0 {
			t.Errorf("WarnCalls = %v, want none for replaced/gone skips", ui.WarnCalls)
		}
	})

	t.Run("prunes stopped task sandboxes carried in Tasks", func(t *testing.T) {
		task := stoppedHandle("agents-sandbox-task-fill-1mjusbm3wikhb0-opencode-1729")
		client := &msb.MockMsbClient{}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneSandboxes(
			context.Background(),
			PruneState{Tasks: []msb.SandboxHandle{task}},
			false,
			ui,
		)
		if err != nil {
			t.Fatalf("PruneSandboxes: %v", err)
		}
		if r.VMsPruned != 1 || !task.DidRemove() {
			t.Errorf("task sandbox not pruned: pruned=%d removed=%v", r.VMsPruned, task.DidRemove())
		}
	})
}
