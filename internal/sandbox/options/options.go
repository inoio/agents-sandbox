package options

import (
	"fmt"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

// RunOptions controls sandbox lifecycle and configuration.
type RunOptions struct {
	Worktree    WorktreeSpec
	Memory      string
	TmpSize     string
	DiskSize    string
	User        string
	Args        []string
	ReapPolicy  ReapPolicy
	IdleTimeout time.Duration
	CPUs        uint8
	Rebuild     bool
	DryRun      bool
	DryRunVM    bool
	Auto        bool
	// Recreate forces a project-VM rebuild on this invocation. It is set by
	// prepareSandbox from the reconfig decision and is never user-facing.
	Recreate bool
	// ServeOnly starts the VM with the opencode port published on the host and
	// serves without attaching the in-VM TUI. Clients (e.g. Opencode Desktop)
	// connect to the published host port.
	ServeOnly bool
}

// ServeOnlyBindAddr is the loopback address the opencode server binds to
// when run in serve-only mode.
const ServeOnlyBindAddr = "127.0.0.1"

// ServeOnlyPort is the host/guest TCP port used for the opencode server in
// serve-only mode.
const ServeOnlyPort = "4096"

// ServeOnlyBindings returns the msb port binding for serve-only mode.
func ServeOnlyBindings() []msbSdk.PortBinding {
	return []msbSdk.PortBinding{
		{Bind: ServeOnlyBindAddr, HostPort: 4096, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
	}
}

// ReapPolicy controls what happens after the last client detaches from a VM.
type ReapPolicy struct {
	// AutoStopOnActiveSessions, when true, behaves like "auto-stop active sessions":
	// the VM is NOT held for in-flight agent work and stops promptly (via the msb
	// idle timeout) even while sessions are busy. When false (default), the reaper
	// holds the VM (keeper exec) until all sessions are quiescent (busy runs to
	// completion) or a stuck retry exceeds MaxSessionRetries, then detaches and the
	// idle timeout stops the VM.
	AutoStopOnActiveSessions bool
	// MaxSessionRetries caps how long to tolerate a session stuck in retry before
	// stopping the wait. 0 means use the package default (10).
	MaxSessionRetries int
}

// WorktreeSpec is a parsed --worktree flag value: a required name (which must
// already be a slug) and an optional base ref to point a fresh worktree at.
type WorktreeSpec struct {
	Name string
	Base string
}
