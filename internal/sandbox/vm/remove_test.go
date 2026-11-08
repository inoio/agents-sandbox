package vm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestIsProjectVMName pins which names RemoveSandbox treats as project VMs: a
// prefixed legacy name and a prefix-free name that parses to a slug and is not
// a task. A name that parses to no slug (empty, a home volume, a runner image)
// is not one, although ParseVMName reports it as no task.
func TestIsProjectVMName(t *testing.T) {
	for name, want := range map[string]bool{
		ProjectVMName(state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}): true,
		naming.LegacyVMPrefix + "proj-aBc1234D":                                    true,
		naming.LegacyTaskPrefix + "x-1729":                                         false,
		"":                                                                         false,
		naming.LegacyHomePrefix + "proj":                                           false,
		"agents-sandbox/runner-proj:latest":                                        false,
	} {
		if got := isProjectVMName(name); got != want {
			t.Errorf("isProjectVMName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestRemoveSandboxRefusesTaskSandboxName(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	handle := &msb.MockSandboxHandle{
		Name_:   naming.LegacyTaskPrefix + "x-1729",
		Status_: msb.SandboxStatusStopped,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	err := RemoveSandbox(context.Background(), handle.Name_, false, assumeYesUI())
	if err == nil || !strings.Contains(err.Error(), "not an agents-sandbox project VM") {
		t.Fatalf("RemoveSandbox(task sandbox) = %v, want a refusal", err)
	}
	if handle.DidRmv {
		t.Error("a task sandbox must not be removed by RemoveSandbox")
	}
}

func TestRemoveSandboxRefusesRunningWithoutForce(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	handle := &msb.MockSandboxHandle{
		Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
		Status_: msb.SandboxStatusRunning,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	ui := assumeYesUI()
	err := RemoveSandbox(context.Background(), handle.Name_, false, ui)
	if err == nil || !strings.Contains(err.Error(), "not stopped") {
		t.Fatalf("RemoveSandbox(running, no force) = %v, want a refusal to remove a VM that is not stopped", err)
	}
	if handle.DidRmv || handle.DidStop {
		t.Error("a running sandbox without --force must be neither stopped nor removed")
	}
	if len(ui.ConfirmCalls) != 0 {
		t.Errorf("the running guard must run before the prompt, got %v", ui.ConfirmCalls)
	}
}

func TestRemoveSandboxForceStopsThenRemoves(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	handle := &msb.MockSandboxHandle{
		Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
		Status_: msb.SandboxStatusRunning,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	if err := RemoveSandbox(context.Background(), handle.Name_, true, assumeYesUI()); err != nil {
		t.Fatalf("RemoveSandbox(running, force) = %v", err)
	}
	if !handle.DidStop {
		t.Error("a forced running removal must stop the sandbox first")
	}
	if !handle.DidRmv {
		t.Error("a forced running removal must remove the sandbox")
	}
}

// TestRemoveSandboxRemovesNothingWithoutConfirmation pins that a removal that
// is not confirmed (a non-interactive run without --yes, or a declined prompt)
// neither stops nor removes the sandbox, with or without --force.
func TestRemoveSandboxRemovesNothingWithoutConfirmation(t *testing.T) {
	cases := []struct {
		name    string
		status  msb.SandboxStatus
		force   bool
		ui      *termio.Mock
		wantErr error
	}{
		{
			name:    "non-interactive",
			status:  msb.SandboxStatusStopped,
			ui:      &termio.Mock{},
			wantErr: termio.ErrConfirmationRequired,
		},
		{
			name:    "declined",
			status:  msb.SandboxStatusStopped,
			ui:      &termio.Mock{IsInteractiveResult: true, ConfirmResult: false},
			wantErr: termio.ErrCancelled,
		},
		{
			name:    "declined with force on a running VM",
			status:  msb.SandboxStatusRunning,
			force:   true,
			ui:      &termio.Mock{IsInteractiveResult: true, ConfirmResult: false},
			wantErr: termio.ErrCancelled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			handle := &msb.MockSandboxHandle{Name_: naming.LegacyVMPrefix + "proj-aBc1234D", Status_: tc.status}
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(handle)
			msb.WithMsbMock(t, client)

			err := RemoveSandbox(context.Background(), handle.Name_, tc.force, tc.ui)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("RemoveSandbox = %v, want %v", err, tc.wantErr)
			}
			if handle.DidStop || handle.DidRmv {
				t.Errorf("stopped %v, removed %v; want neither without confirmation", handle.DidStop, handle.DidRmv)
			}
		})
	}
}

// TestRemoveSandboxKeepsTheProjectState pins that removing a project VM, forced
// or not, leaves its persisted project state, so the next run reuses the
// recorded home volume.
func TestRemoveSandboxKeepsTheProjectState(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
			const homeVolume = "agents-sandbox-home-proj-1mjusbm3wikhb0-opencode-20260812T123456"
			if err := seedHomeState(key, state.HomeState{HomeVolume: homeVolume}); err != nil {
				t.Fatalf("seed state: %v", err)
			}
			handle := &msb.MockSandboxHandle{
				Name_:   naming.LegacyVMPrefix + key.Slug + "-" + key.Agent,
				Status_: msb.SandboxStatusStopped,
			}
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(handle)
			msb.WithMsbMock(t, client)

			if err := RemoveSandbox(context.Background(), handle.Name_, force, assumeYesUI()); err != nil {
				t.Fatalf("RemoveSandbox: %v", err)
			}
			if !handle.DidRmv {
				t.Fatal("the sandbox was not removed")
			}
			st, err := state.ReadState(key)
			if err != nil || st.HomeVolume != homeVolume {
				t.Errorf("state after removal = %+v, %v; want the recorded home volume kept", st, err)
			}
		})
	}
}

func TestRemoveSandboxReplacedFailsClosed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	replaced := &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "handle was replaced"}
	handle := &msb.MockSandboxHandle{
		Name_:     naming.LegacyVMPrefix + "proj-aBc1234D",
		Status_:   msb.SandboxStatusStopped,
		RemoveErr: replaced,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	err := RemoveSandbox(context.Background(), handle.Name_, true, assumeYesUI())
	if err == nil || !msb.IsReplaced(err) {
		t.Fatalf("RemoveSandbox(replaced) = %v, want a fail-closed replaced error", err)
	}
	if handle.DidRmv {
		t.Error("a replaced handle must not mark the sandbox removed")
	}
}

func TestRemoveSandboxSurfacesGetError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	getErr := errors.New("boom")
	client := &msb.MockMsbClient{}
	client.SetGetSandboxErr(getErr)
	msb.WithMsbMock(t, client)

	err := RemoveSandbox(context.Background(), naming.LegacyVMPrefix+"proj-aBc1234D", false, assumeYesUI())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("RemoveSandbox(get error) = %v, want the GetSandbox error", err)
	}
	if !errors.Is(err, getErr) {
		t.Errorf("error = %v, want it to wrap the GetSandbox cause", err)
	}
}

func TestRemoveSandboxSurfacesStopError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	stopErr := errors.New("stop failed")
	handle := &msb.MockSandboxHandle{
		Name_:   naming.LegacyVMPrefix + "proj-aBc1234D",
		Status_: msb.SandboxStatusRunning,
		StopErr: stopErr,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	err := RemoveSandbox(context.Background(), handle.Name_, true, assumeYesUI())
	if err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Fatalf("RemoveSandbox(stop error) = %v, want the stop error", err)
	}
	if !errors.Is(err, stopErr) {
		t.Errorf("error = %v, want it to wrap the stop cause", err)
	}
	if handle.DidRmv {
		t.Error("a failed stop must prevent removal")
	}
}

func TestRemoveSandboxSurfacesRemoveError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	removeErr := errors.New("remove failed")
	handle := &msb.MockSandboxHandle{
		Name_:     naming.LegacyVMPrefix + "proj-aBc1234D",
		Status_:   msb.SandboxStatusStopped,
		RemoveErr: removeErr,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	err := RemoveSandbox(context.Background(), handle.Name_, false, assumeYesUI())
	if !errors.Is(err, removeErr) {
		t.Fatalf("RemoveSandbox(remove error) = %v, want it to wrap %v", err, removeErr)
	}
	if msb.IsReplaced(err) || msb.IsNotFound(err) {
		t.Errorf("a generic remove error must not be classified as replaced/not-found: %v", err)
	}
}

func TestRemoveSandboxBarePrefixRemoves(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	handle := &msb.MockSandboxHandle{Name_: naming.LegacyVMPrefix, Status_: msb.SandboxStatusStopped}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	if err := RemoveSandbox(context.Background(), handle.Name_, true, assumeYesUI()); err != nil {
		t.Fatalf("RemoveSandbox(bare prefix, force) = %v", err)
	}
	if !handle.DidRmv {
		t.Error("a bare-prefix VM must still be removed")
	}
}

// assumeYesUI returns a non-interactive UI that authorizes any confirmation.
func assumeYesUI() *termio.Mock {
	ui := &termio.Mock{}
	ui.SetAssumeYes(true)
	return ui
}
