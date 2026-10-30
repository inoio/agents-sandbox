package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// inactiveStatuses are the statuses IsSandboxInactive treats as having no live
// runtime, so stop/kill must skip the action and remove only persisted state.
var inactiveStatuses = []msbSdk.SandboxStatus{
	msbSdk.SandboxStatusCreated,
	msbSdk.SandboxStatusStopped,
	msbSdk.SandboxStatusCrashed,
}

// failingStopFn records that the stop/kill action must not run on an inactive
// sandbox; it fails the test if invoked.
func failingStopFn(t *testing.T) func(msb.SandboxHandle, context.Context) error {
	t.Helper()
	return func(msb.SandboxHandle, context.Context) error {
		t.Error("stop/kill action must not run on an inactive sandbox")
		return nil
	}
}

// inactiveHandle builds a mock handle in the given inactive status.
func inactiveHandle(status msbSdk.SandboxStatus) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{Name_: "vm", Status_: status}
}

// TestStopOrKillProjectVMInactiveWithoutRemoveLeavesState pins the unchanged
// inactive path: without remove, neither the action nor the removal runs.
func TestStopOrKillProjectVMInactiveWithoutRemoveLeavesState(t *testing.T) {
	for _, status := range inactiveStatuses {
		t.Run(string(status), func(t *testing.T) {
			ui := termio.NewTestMock(t)
			handle := inactiveHandle(status)
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(handle)

			err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping",
				client, failingStopFn(t))
			if err != nil {
				t.Fatalf("stop: %v", err)
			}
			if handle.DidRemove() {
				t.Error("removal must not run without remove")
			}
			if !strings.Contains(joinStrings(ui.InfoCalls), "already stopped") {
				t.Errorf("expected the already-stopped note, got %v", ui.InfoCalls)
			}
		})
	}
}

// TestStopOrKillProjectVMInactiveRemoveRemovesState pins the new behavior:
// remove on an inactive sandbox skips the action but still removes the state.
func TestStopOrKillProjectVMInactiveRemoveRemovesState(t *testing.T) {
	for _, status := range inactiveStatuses {
		t.Run(string(status), func(t *testing.T) {
			ui := termio.NewTestMock(t)
			handle := inactiveHandle(status)
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(handle)

			err := stopOrKillProjectVM(context.Background(), true, false, &ui, testVMKey(), "stop", "Stopping",
				client, failingStopFn(t))
			if err != nil {
				t.Fatalf("stop: %v", err)
			}
			if !handle.DidRemove() {
				t.Error("expected remove to delete state on an inactive sandbox")
			}
			if !strings.Contains(joinStrings(ui.VerboseCalls), "persisted state removed") {
				t.Errorf("expected a verbose removal note, got %v", ui.VerboseCalls)
			}
		})
	}
}

// TestStopOrKillProjectVMInactiveRemoveDryRunRemovesNothing pins that a dry run
// removes nothing even for an inactive sandbox, and says it would remove.
func TestStopOrKillProjectVMInactiveRemoveDryRunRemovesNothing(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := inactiveHandle(msbSdk.SandboxStatusStopped)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)

	err := stopOrKillProjectVM(context.Background(), true, true, &ui, testVMKey(), "stop", "Stopping",
		client, failingStopFn(t))
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if handle.DidRemove() {
		t.Error("a dry run must remove nothing")
	}
	if !strings.Contains(joinStrings(ui.InfoCalls), "dry-run: Would remove persisted state") {
		t.Errorf("expected a dry-run removal note, got %v", ui.InfoCalls)
	}
}

// TestStopOrKillProjectVMInactiveRemoveFailureWarns pins the non-fatal warning
// on the inactive path, mirroring the active path.
func TestStopOrKillProjectVMInactiveRemoveFailureWarns(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := inactiveHandle(msbSdk.SandboxStatusStopped)
	handle.RemoveErr = errors.New("remove failed")
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)

	err := stopOrKillProjectVM(context.Background(), true, false, &ui, testVMKey(), "stop", "Stopping",
		client, failingStopFn(t))
	if err != nil {
		t.Fatalf("removal failure must be non-fatal: %v", err)
	}
	if !strings.Contains(joinStrings(ui.WarnCalls), "failed to remove sandbox state") {
		t.Errorf("expected a warning about the failed removal, got %v", ui.WarnCalls)
	}
}
