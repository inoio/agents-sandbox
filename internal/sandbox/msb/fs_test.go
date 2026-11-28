package msb

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// TestRealSandboxFS pins that realSandbox.FS returns the launcher adapter over
// the SDK filesystem, not the SDK type itself. It moves here (from msb_test.go)
// so the unit's tests live in the file named after the unit.
func TestRealSandboxFS(t *testing.T) {
	swapSeam(t, &realSandboxFilesystem,
		func(*msbSdk.Sandbox) *msbSdk.SandboxFSOps { return &msbSdk.SandboxFSOps{} })

	got := realSandbox{sandbox: &msbSdk.Sandbox{}}.FS()
	if _, ok := got.(realSandboxFS); !ok {
		t.Fatalf("FS() = %T, want realSandboxFS", got)
	}
}

// TestRealSandboxFSDelegatesTransfers pins that the launcher FS adapter routes
// the two transfer methods to the SDK filesystem with the argument order each
// method promises (hostPath, guestPath) and (guestPath, hostPath).
func TestRealSandboxFSDelegatesTransfers(t *testing.T) {
	ctx := context.Background()
	fs := realSandboxFS{SandboxFSOps: &msbSdk.SandboxFSOps{}}

	var fromHost [2]string
	swapSeam(t, &realSandboxFSCopyFromHost,
		func(_ *msbSdk.SandboxFSOps, _ context.Context, hostPath, guestPath string) error {
			fromHost = [2]string{hostPath, guestPath}
			return nil
		})
	if err := fs.CopyFromHost(ctx, "host.txt", "/guest.txt"); err != nil {
		t.Fatalf("CopyFromHost() error = %v", err)
	}
	if fromHost != [2]string{"host.txt", "/guest.txt"} {
		t.Errorf("CopyFromHost args = %v, want host then guest", fromHost)
	}

	var toHost [2]string
	swapSeam(t, &realSandboxFSCopyToHost,
		func(_ *msbSdk.SandboxFSOps, _ context.Context, guestPath, hostPath string) error {
			toHost = [2]string{guestPath, hostPath}
			return nil
		})
	if err := fs.CopyToHost(ctx, "/guest.txt", "host.txt"); err != nil {
		t.Fatalf("CopyToHost() error = %v", err)
	}
	if toHost != [2]string{"/guest.txt", "host.txt"} {
		t.Errorf("CopyToHost args = %v, want guest then host", toHost)
	}
}

// TestRealSandboxFSPropagatesTransferErrors pins that a failed SDK transfer is
// returned to the caller rather than swallowed, so sandbox cp reports it.
func TestRealSandboxFSPropagatesTransferErrors(t *testing.T) {
	cause := errors.New("transfer boom")
	fs := realSandboxFS{SandboxFSOps: &msbSdk.SandboxFSOps{}}
	swapSeam(t, &realSandboxFSCopyFromHost,
		func(*msbSdk.SandboxFSOps, context.Context, string, string) error { return cause })

	if err := fs.CopyFromHost(context.Background(), "host.txt", "/guest.txt"); !errors.Is(err, cause) {
		t.Fatalf("CopyFromHost() error = %v, want %v", err, cause)
	}
}
