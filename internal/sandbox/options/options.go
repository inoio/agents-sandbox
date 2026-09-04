package options

import (
	"fmt"
	"net"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// RunOptions controls sandbox lifecycle and configuration.
type RunOptions struct {
	Worktree WorktreeSpec
	Memory   string
	TmpSize  string
	DiskSize string
	Root     bool
	Args     []string
	// Agent selects the coding-agent profile for this session. Empty resolves
	// to the default agent via agent.Lookup.
	Agent       string
	ReapPolicy  ReapPolicy
	IdleTimeout time.Duration
	CPUs        uint8
	Rebuild     bool
	// Dind appends the tool's Docker-in-Docker block to the runner image.
	Dind     bool
	DryRun   bool
	DryRunVM bool
	// WorkspaceQuota is the guest-write quota for the /workspace bind mount.
	WorkspaceQuota string
	// Recreate forces a project-VM rebuild on this invocation. It is set by
	// prepareSandbox from the reconfig decision and is never user-facing.
	Recreate bool
	// ServeHostPort is the resolved published host port for serve-only mode
	// (0 when not serving). It is set by prepareSandbox and is never
	// user-facing, mirroring Recreate.
	ServeHostPort int
	// ServeOnly starts the VM with the agent port published on the host and
	// serves without attaching the in-VM TUI. Clients (e.g. a desktop app)
	// connect to the published host port.
	ServeOnly bool
	// Notify is the resolved notify config for the session (channels + triggers).
	Notify notify.Config
	// Network is the resolved egress policy for the project VM. The zero value
	// (Empty) means no policy is set and the default public profile applies.
	Network network.Policy
	// Mounts are additional host directories bind-mounted at absolute guest paths.
	Mounts mounts.Mounts
	// AgentVersion pins the agent version baked into the runner image on
	// rebuild. Empty means resolve the latest at build time.
	AgentVersion string
	// ProvisionHostConfig controls whether the agent's host config files are
	// copied into the VM (drop-in provisioning). A nil value enables it
	// (default); cmd resolves the launcher config and sets it explicitly.
	ProvisionHostConfig *bool
}

// ServeOnlyBindAddr is the loopback address the agent server binds to
// when run in serve-only mode.
const ServeOnlyBindAddr = "127.0.0.1"

// ServeOnlyBasePort is the lowest host port probed when publishing the agent
// server in serve-only mode, and the guest port the agents serve on.
const ServeOnlyBasePort = 4096

// FirstFreeHostPort returns the first host port at or above base that is not
// currently bound on the loopback address.
func FirstFreeHostPort(base int) int {
	for p := base; p < base+1024; p++ {
		//nolint:noctx // no context applies to a transient probe of localhost port availability
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", ServeOnlyBindAddr, p))
		if err == nil {
			_ = ln.Close()
			return p
		}
	}
	return base
}

// ServeOnlyBindings returns the msb port binding for serve-only mode with the
// given host port, mapping it to the fixed guest port.
func ServeOnlyBindings(hostPort int) []msbSdk.PortBinding {
	return []msbSdk.PortBinding{
		//nolint:gosec // G115: host ports are well below the uint16 max
		{
			Bind:      ServeOnlyBindAddr,
			HostPort:  uint16(hostPort),
			GuestPort: ServeOnlyBasePort,
			Protocol:  msbSdk.PortProtocolTCP,
		},
	}
}

// ResolveServeHostPort returns the host port to publish for serve-only mode:
// the port an existing VM already publishes (kept stable across runs), or the
// first free host port at or above ServeOnlyBasePort.
func ResolveServeHostPort(cfg *msbSdk.SandboxConfig, serveOnly bool) int {
	if !serveOnly {
		return 0
	}
	if cfg != nil {
		for _, pb := range cfg.PortBindings {
			if pb.HostPort != 0 {
				return int(pb.HostPort)
			}
		}
	}
	return FirstFreeHostPort(ServeOnlyBasePort)
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

// NewReapPolicy builds a ReapPolicy. A non-positive maxSessionRetries uses
// the package default (10).
func NewReapPolicy(autoStopOnActiveSessions bool, maxSessionRetries int) ReapPolicy {
	if maxSessionRetries <= 0 {
		maxSessionRetries = 10
	}
	return ReapPolicy{
		AutoStopOnActiveSessions: autoStopOnActiveSessions,
		MaxSessionRetries:        maxSessionRetries,
	}
}

// WorktreeSpec is a parsed --worktree flag value: a required name (which must
// already be a slug) and an optional base ref to point a fresh worktree at.
type WorktreeSpec struct {
	Name string
	Base string
}
