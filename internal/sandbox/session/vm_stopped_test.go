package session

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// withProjectVMStatus pins what the project-VM lookup after a failed attach
// finds, and counts the lookups.
func withProjectVMStatus(t *testing.T, status msb.SandboxStatus, found bool, err error) *int {
	t.Helper()
	lookups := 0
	original := projectVMStatus
	projectVMStatus = func(context.Context, state.Key) (msb.SandboxStatus, bool, error) {
		lookups++
		return status, found, err
	}
	t.Cleanup(func() { projectVMStatus = original })
	return &lookups
}

// attachEndingWith runs an attach whose session ends with attachErr.
func attachEndingWith(t *testing.T, attachErr error) (*termiotest.Mock, error) {
	t.Helper()
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	sb := &msbtest.MockSandbox{AttachErr: attachErr}
	err := runAttach(context.Background(), state.Key{Slug: "vmstopped", Agent: "opencode"}, sb, "/workspace", ui,
		resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")
	return ui, err
}

func TestRunAttachNamesAVMThatStoppedDuringTheAttach(t *testing.T) {
	for name, tc := range map[string]struct {
		status msb.SandboxStatus
		found  bool
	}{
		"stopped":  {msb.SandboxStatusStopped, true},
		"crashed":  {msb.SandboxStatusCrashed, true},
		"draining": {msb.SandboxStatusDraining, true},
		"gone":     {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			withProjectVMStatus(t, tc.status, tc.found, nil)

			ui, err := attachEndingWith(t, errors.New("agent connection closed during terminal attach"))

			if !errors.Is(err, errVMStoppedDuringAttach) {
				t.Fatalf("err = %v, want the VM-stopped message", err)
			}
			if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "agent connection closed") {
				t.Errorf("VerboseCalls = %v, want the attach's own error kept for -v", ui.VerboseCalls)
			}
		})
	}
}

func TestRunAttachKeepsTheAttachErrorWhileTheVMRuns(t *testing.T) {
	for name, tc := range map[string]struct {
		status    msb.SandboxStatus
		found     bool
		lookupErr error
	}{
		"running":      {msb.SandboxStatusRunning, true, nil},
		"lookup fails": {"", false, errors.New("runtime unreachable")},
	} {
		t.Run(name, func(t *testing.T) {
			withProjectVMStatus(t, tc.status, tc.found, tc.lookupErr)

			_, err := attachEndingWith(t, errors.New("agent connection closed during terminal attach"))

			if err == nil || !strings.Contains(err.Error(), "agent session failed") ||
				errors.Is(err, errVMStoppedDuringAttach) {
				t.Fatalf("err = %v, want the attach's own failure", err)
			}
		})
	}
}

func TestRunAttachLooksTheVMUpOnlyAfterAFailedAttach(t *testing.T) {
	lookups := withProjectVMStatus(t, msb.SandboxStatusStopped, true, nil)

	_, err := attachEndingWith(t, nil)
	if err != nil {
		t.Fatalf("err = %v, want a clean exit", err)
	}
	if *lookups != 0 {
		t.Errorf("looked the VM up %d times after a clean exit, want none", *lookups)
	}
}

func TestProjectVMStatusReadsTheProjectVM(t *testing.T) {
	original := projectVMStatus
	t.Cleanup(func() { projectVMStatus = original })
	restore := msb.Get
	t.Cleanup(func() { msb.Get = restore })
	k := state.Key{Slug: "lookup", Agent: "opencode"}
	handle := &msbtest.MockSandboxHandle{Status_: msb.SandboxStatusStopped}
	msb.Get = func() msb.Client {
		return &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
			return handle, nil
		}}
	}
	projectVMStatus = defaultProjectVMStatus

	status, found, err := projectVMStatus(context.Background(), k)

	if err != nil || !found || status != msb.SandboxStatusStopped {
		t.Errorf("projectVMStatus = %q, %v, %v; want the handle's stopped status", status, found, err)
	}
}

// TestRunAttachLeavesASignalEndedAttachAlone pins that an attach the user
// ended with a signal is not reported as a stopped VM: the signal, not the VM,
// ended it, and the lookup is skipped.
func TestRunAttachLeavesASignalEndedAttachAlone(t *testing.T) {
	lookups := withProjectVMStatus(t, msb.SandboxStatusStopped, true, nil)
	configpathstest.WithMockConfigPaths(t)
	sb := &msbtest.MockSandbox{AttachFn: func(ctx context.Context) (int, error) {
		//testenv:allow sends SIGINT to this process to end the attach by a signal
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			return 0, err
		}
		<-ctx.Done()
		return 0, errors.New("agent connection closed during terminal attach")
	}}

	err := runAttach(context.Background(), state.Key{Slug: "signalstop", Agent: "opencode"}, sb, "/workspace",
		&termiotest.Mock{}, resolvedRunOptions(t, options.RunOptions{ReapPolicy: options.NewReapPolicy(true, 5)}), "-l")

	if errors.Is(err, errVMStoppedDuringAttach) {
		t.Fatalf("err = %v, want the signal-ended attach not reported as a stopped VM", err)
	}
	if *lookups != 0 {
		t.Errorf("looked the VM up %d times after a signal, want none", *lookups)
	}
}
