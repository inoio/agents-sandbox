package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestProjectVMHomeVolume pins the home volume read every caller relies on: a
// lookup error is returned, a missing VM or a nil handle counts as "no VM", and
// a mounts read failure reports the VM as existing so a destroying caller can
// fail closed.
func TestProjectVMHomeVolume(t *testing.T) {
	lookupErr := errors.New("lookup boom")
	mountsErr := errors.New("mounts boom")
	notFound := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}

	for _, tc := range []struct {
		name       string
		getSandbox func(context.Context, string) (msb.SandboxHandle, error)
		wantVolume string
		wantExists bool
		wantErr    error
	}{
		{
			name: "lookup error",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return nil, lookupErr
			},
			wantErr: lookupErr,
		},
		{
			name: "no VM",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return nil, notFound
			},
		},
		{
			name: "nil handle",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return nil, nil //nolint:nilnil // the nil handle is the case under test
			},
		},
		{
			name: "mounts unreadable",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return &msb.MockSandboxHandle{Name_: "vm", MountsErr: mountsErr}, nil
			},
			wantExists: true,
			wantErr:    mountsErr,
		},
		{
			name: "mounted home volume",
			getSandbox: func(context.Context, string) (msb.SandboxHandle, error) {
				return &msb.MockSandboxHandle{Name_: "vm", Mounts: map[string]msbSdk.MountConfig{
					mounts.VMHomeDir: msbSdk.Mount.Named("home-vol", msbSdk.MountOptions{}),
				}}, nil
			},
			wantVolume: "home-vol",
			wantExists: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &msb.MockMsbClient{GetSandboxFn: tc.getSandbox}

			volume, exists, err := ProjectVMHomeVolume(context.Background(), client, testVMKey())

			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if volume != tc.wantVolume || exists != tc.wantExists {
				t.Errorf("got (%q, %v), want (%q, %v)", volume, exists, tc.wantVolume, tc.wantExists)
			}
		})
	}
}

// TestSetProjectVMLockReplacesTheCreationLock pins the test seam: EnsureProjectVM
// takes the replaced lock on its create path, and the seam hands back the lock it
// replaced so a test can restore it.
func TestSetProjectVMLockReplacesTheCreationLock(t *testing.T) {
	lockErr := errors.New("lock boom")
	failingLock := func(state.Key) (func(), error) { return nil, lockErr }
	orig := SetProjectVMLock(failingLock)
	t.Cleanup(func() { SetProjectVMLock(orig) })
	ui := termio.NewTestMock(t)

	_, _, err := EnsureProjectVM(
		context.Background(), &msb.MockMsbClient{}, options.RunOptions{}, image.ImageInfo{}, "home-vol", "/repo",
		testVMKey(), nil, "repo", false, envsecret.EnvSecrets{}, &ui,
	)
	if !errors.Is(err, lockErr) {
		t.Fatalf("EnsureProjectVM error = %v, want the replaced lock's %v", err, lockErr)
	}

	replaced := SetProjectVMLock(orig)
	if _, err := replaced(testVMKey()); !errors.Is(err, lockErr) {
		t.Errorf("SetProjectVMLock returned a lock with error %v, want the one it replaced (%v)", err, lockErr)
	}
}
