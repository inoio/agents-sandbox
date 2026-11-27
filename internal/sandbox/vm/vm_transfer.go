package vm

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// CopyDirection is the direction of a host↔project-VM file transfer.
type CopyDirection int

const (
	// CopyHostToVM copies a host file into the project VM.
	CopyHostToVM CopyDirection = iota
	// CopyVMToHost copies a project-VM file out to the host.
	CopyVMToHost
)

func (d CopyDirection) String() string {
	if d == CopyVMToHost {
		return "vm-to-host"
	}
	return "host-to-vm"
}

// CopyResult is a completed host↔project-VM transfer. HostPath and GuestPath
// name the two sides, not the source/destination order.
type CopyResult struct {
	VMName    string
	Direction CopyDirection
	HostPath  string
	GuestPath string
}

// CopyProjectFile transfers one file between the host and the current
// directory's running project VM. It resolves identity as StopProjectVM does,
// requires a running VM, and never boots a stopped one.
func CopyProjectFile(
	ctx context.Context,
	paths configpaths.ConfigPaths,
	mclient msb.Client,
	direction CopyDirection,
	hostPath, guestPath string,
	ui termio.UI,
	agentName string,
) (CopyResult, error) {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return CopyResult{}, fmt.Errorf("unknown agent %q", agentName)
	}
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		return CopyResult{}, err
	}
	// cp has no dry run, so a pending legacy identity is applied as run does.
	adoption, err := AdoptLegacyIdentity(ctx, paths, mclient, identity, a.Name(), true, ui)
	if err != nil {
		return CopyResult{}, err
	}
	k := state.Key{Slug: adoption.Slug, Agent: a.Name()}

	handle, found, err := FindProjectVM(ctx, mclient, k)
	if err != nil {
		return CopyResult{}, err
	}
	if !found {
		return CopyResult{}, errors.New(
			"no project VM for this checkout; start it with `agents-sandbox run` or `agents-sandbox shell`")
	}
	if handle.Status() != msb.SandboxStatusRunning {
		return CopyResult{}, fmt.Errorf(
			"project VM %q is %s, not running; start it with `agents-sandbox run` or `agents-sandbox shell`",
			handle.Name(), handle.Status())
	}
	// Connect is status-gated and never boots the VM.
	sandbox, err := handle.Connect(ctx)
	if err != nil {
		return CopyResult{}, fmt.Errorf("connect to sandbox %q: %w", handle.Name(), err)
	}
	if err := copyFile(ctx, sandbox.FS(), direction, hostPath, guestPath); err != nil {
		return CopyResult{}, err
	}
	return CopyResult{VMName: handle.Name(), Direction: direction, HostPath: hostPath, GuestPath: guestPath}, nil
}

// copyFile runs one transfer. CopyFromHost does not create the guest
// destination's parent, so a nested guest destination is Mkdir'd first (the SDK
// Mkdir creates every missing parent); CopyToHost needs no such step.
func copyFile(
	ctx context.Context,
	fs msb.SandboxFS,
	direction CopyDirection,
	hostPath, guestPath string,
) error {
	if direction == CopyVMToHost {
		if err := fs.CopyToHost(ctx, guestPath, hostPath); err != nil {
			return fmt.Errorf("copy %s -> %s: %w", guestPath, hostPath, err)
		}
		return nil
	}
	if parent := path.Dir(guestPath); parent != "/" {
		if err := fs.Mkdir(ctx, parent); err != nil {
			return fmt.Errorf("create guest destination directory %s: %w", parent, err)
		}
	}
	if err := fs.CopyFromHost(ctx, hostPath, guestPath); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", hostPath, guestPath, err)
	}
	return nil
}
