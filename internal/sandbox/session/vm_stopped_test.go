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
func withProjectVMStatus(t *testing.T, status msb.SandboxStatus, err error) *int {
	t.Helper()
	lookups := 0
	original := projectVMStatus
	projectVMStatus = func(context.Context, state.Key) (msb.SandboxStatus, error) {
		lookups++
		return status, err
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
	for name, status := range map[string]msb.SandboxStatus{
		"stopped":  msb.SandboxStatusStopped,
		"crashed":  msb.SandboxStatusCrashed,
		"draining": msb.SandboxStatusDraining,
		"created":  msb.SandboxStatusCreated,
		"gone":     "",
	} {
		t.Run(name, func(t *testing.T) {
			withProjectVMStatus(t, status, nil)

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
		lookupErr error
	}{
		"running":      {msb.SandboxStatusRunning, nil},
		"paused":       {msb.SandboxStatusPaused, nil},
		"starting":     {msb.SandboxStatusStarting, nil},
		"lookup fails": {"", errors.New("runtime unreachable")},
	} {
		t.Run(name, func(t *testing.T) {
			withProjectVMStatus(t, tc.status, tc.lookupErr)

			_, err := attachEndingWith(t, errors.New("agent connection closed during terminal attach"))

			if err == nil || !strings.Contains(err.Error(), "agent session failed") ||
				errors.Is(err, errVMStoppedDuringAttach) {
				t.Fatalf("err = %v, want the attach's own failure", err)
			}
		})
	}
}

func TestRunAttachLooksTheVMUpOnlyAfterAFailedAttach(t *testing.T) {
	lookups := withProjectVMStatus(t, msb.SandboxStatusStopped, nil)

	_, err := attachEndingWith(t, nil)
	if err != nil {
		t.Fatalf("err = %v, want a clean exit", err)
	}
	if *lookups != 0 {
		t.Errorf("looked the VM up %d times after a clean exit, want none", *lookups)
	}
}

func TestDefaultProjectVMStatus(t *testing.T) {
	k := state.Key{Slug: "lookup", Agent: "opencode"}
	for name, tc := range map[string]struct {
		lookup  func(context.Context, string) (msb.SandboxHandle, error)
		status  msb.SandboxStatus
		wantErr bool
	}{
		"found": {
			lookup: func(context.Context, string) (msb.SandboxHandle, error) {
				return &msbtest.MockSandboxHandle{Status_: msb.SandboxStatusStopped}, nil
			},
			status: msb.SandboxStatusStopped,
		},
		// The mock holds no sandbox, so its lookup reports the VM not found.
		"gone": {lookup: nil, status: ""},
		"lookup fails": {
			lookup:  func(context.Context, string) (msb.SandboxHandle, error) { return nil, errors.New("runtime down") },
			wantErr: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			msbtest.WithMsbMock(t, &msbtest.MockMsbClient{GetSandboxFn: tc.lookup})

			status, err := defaultProjectVMStatus(context.Background(), k)

			if (err != nil) != tc.wantErr || status != tc.status {
				t.Errorf("defaultProjectVMStatus = %q, %v; want %q, error %v", status, err, tc.status, tc.wantErr)
			}
		})
	}
}

// TestRunAttachLeavesASignalEndedAttachAlone pins that an attach the user
// ended with a signal is not reported as a stopped VM: the signal, not the VM,
// ended it, and the lookup is skipped.
func TestRunAttachLeavesASignalEndedAttachAlone(t *testing.T) {
	lookups := withProjectVMStatus(t, msb.SandboxStatusStopped, nil)
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
