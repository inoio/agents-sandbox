package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

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
	if direction == CopyHostToVM {
		binds := hostReadWriteBinds(identity.BindRoot, handle)
		if destPath, ok := overwritingHostDest(binds, hostPath, guestPath); ok {
			return CopyResult{}, fmt.Errorf(
				"refusing to copy %s -> %s: the guest path is the host file %s through a host-backed read-write mount, so the copy would overwrite its own source",
				hostPath,
				guestPath,
				destPath,
			)
		}
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

// hostBind is one host-backed guest mount the copy guard maps through.
type hostBind struct {
	guest string
	host  string
}

// hostReadWriteBinds returns the host-backed read-write binds a host→VM copy
// could alias through: the managed /workspace bind always, plus every
// read-write, host-backed mount the running VM reports. The /workspace entry is
// static so the common case is protected even when the live mount read is empty
// or fails; a ConfigMounts error is non-fatal for the same reason. Named, tmpfs,
// disk and owned mounts carry no host path, and a readonly bind cannot truncate,
// so both are excluded.
func hostReadWriteBinds(bindRoot string, handle msb.SandboxHandle) []hostBind {
	binds := []hostBind{{guest: "/workspace", host: bindRoot}}
	mounts, err := handle.ConfigMounts()
	if err != nil {
		return binds
	}
	for guest, mount := range mounts {
		if mount.Kind != msb.MountKindBind || mount.Readonly || mount.Bind == "" {
			continue
		}
		binds = append(binds, hostBind{guest: path.Clean(guest), host: mount.Bind})
	}
	return binds
}

// overwritingHostDest reports the host file a host→VM copy would alias — and so
// truncate, since the SDK opens the guest sink before it reads — through the
// longest matching host-backed bind, if any. It maps the cleaned guest path to
// its host destination and refuses only when both sides stat and os.SameFile
// holds; a guest path under no bind, or any stat failure, proceeds, leaving the
// transfer's own error to surface.
func overwritingHostDest(binds []hostBind, hostPath, guestPath string) (string, bool) {
	destPath, ok := hostDestForGuest(guestPath, binds)
	if !ok {
		return "", false
	}
	source, err := os.Stat(hostPath)
	if err != nil {
		return "", false
	}
	dest, err := os.Stat(destPath)
	if err != nil {
		return "", false
	}
	if !os.SameFile(source, dest) {
		return "", false
	}
	return destPath, true
}

// hostDestForGuest maps a guest path to its host file through the longest
// matching host-backed bind. A bind matches only at an exact path-segment
// boundary (so /dataX is not under /data), and the bind root itself maps to the
// mount's host directory. It reports false when no bind contains the path.
func hostDestForGuest(guestPath string, binds []hostBind) (string, bool) {
	clean := path.Clean(guestPath)
	best := ""
	bestHost := ""
	for _, bind := range binds {
		guest := path.Clean(bind.guest)
		if clean != guest && !strings.HasPrefix(clean, guest+"/") {
			continue
		}
		if len(guest) > len(best) {
			best = guest
			bestHost = bind.host
		}
	}
	if best == "" {
		return "", false
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(clean, best), "/")
	if rel == "" {
		return bestHost, true
	}
	return filepath.Join(bestHost, rel), true
}
