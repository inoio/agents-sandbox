package msb

import (
	"context"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// SandboxFS is the subset of the sandbox filesystem operations used by the launcher.
type SandboxFS interface {
	Exists(ctx context.Context, path string) (bool, error)
	ReadString(ctx context.Context, path string) (string, error)
	Mkdir(ctx context.Context, path string) error
	Write(ctx context.Context, path string, data []byte) error
	Read(ctx context.Context, path string) ([]byte, error)
	Remove(ctx context.Context, path string) error
	// CopyFromHost copies a single host file into the sandbox. It does not create
	// a missing parent directory, so a caller writing to a nested guest path
	// Mkdirs that parent first.
	CopyFromHost(ctx context.Context, hostPath, guestPath string) error
	// CopyToHost copies a single sandbox file to the host.
	CopyToHost(ctx context.Context, guestPath, hostPath string) error
}

// realSandboxFS adapts *msbSdk.SandboxFSOps to SandboxFS so the seam's
// filesystem surface is implemented by a launcher type, not the SDK type. The
// six read/write operations are promoted from the embedded SDK filesystem; the
// two transfer methods are explicit so their delegation is unit-testable
// without the native library.
type realSandboxFS struct {
	*msbSdk.SandboxFSOps
}

// The transfer delegation targets are package-level seams (method expressions)
// so the thin adapter is unit-testable without a live VM.
var (
	realSandboxFSCopyFromHost = (*msbSdk.SandboxFSOps).CopyFromHost
	realSandboxFSCopyToHost   = (*msbSdk.SandboxFSOps).CopyToHost
)

func (f realSandboxFS) CopyFromHost(ctx context.Context, hostPath, guestPath string) error {
	return realSandboxFSCopyFromHost(f.SandboxFSOps, ctx, hostPath, guestPath)
}

func (f realSandboxFS) CopyToHost(ctx context.Context, guestPath, hostPath string) error {
	return realSandboxFSCopyToHost(f.SandboxFSOps, ctx, guestPath, hostPath)
}
