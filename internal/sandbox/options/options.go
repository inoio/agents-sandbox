package options

import (
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/lifecycle"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
)

// DockerSetting is the resolved docker capability: whether the Docker engine
// is on, and whether that value came from an explicit config/flag/env setting
// (and therefore overrides any project-Dockerfile content signal). Enablement
// consults Enabled only when Explicit is true; without an explicit source the
// project Dockerfile's content signals decide.
type DockerSetting struct {
	Enabled  bool
	Explicit bool
}

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
	Agent string
	// ResolvedAgent is the selected profile and the capabilities the run
	// consumes, resolved once at the run boundary (runoptions.Build). Never set
	// from config or flags.
	ResolvedAgent agent.Resolved
	// ConfigSlug is the effective per-slug user config slug the launcher config
	// was resolved with, carried so later stages can rebuild the per-slug config
	// scope. It is resolved at the CLI boundary from project identity and carried
	// on the resolved config; it is not a config key or a CLI flag, mirroring
	// ResolvedAgent.
	ConfigSlug  string
	ReapPolicy  ReapPolicy
	IdleTimeout time.Duration
	CPUs        uint8
	Rebuild     bool
	// NoRebuild reuses the existing runner image even when its inputs changed
	// and skips the agent upgrade check. A missing image is still built.
	NoRebuild bool
	// Docker is the resolved docker capability: whether the tool's Docker
	// engine block is baked into the runner image and whether that value was
	// set explicitly.
	Docker DockerSetting
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
	// Ports are user-published ports: guest ports reachable from the host
	// loopback. Empty by default; set via the ports config key, the
	// AGENTS_SANDBOX_PORTS env var, or --port.
	Ports []ports.PortMapping
	// Notify is the resolved notify config for the session (channels + triggers).
	Notify notify.Config
	// Network is the resolved egress policy for the project VM. An unset profile
	// is resolved to the public profile.
	Network network.Policy
	// Mounts are additional host directories bind-mounted at absolute guest paths.
	Mounts mounts.Mounts
	// AllowPlaceholderFor lists hosts that may receive any secret's placeholder
	// unchanged. It comes from the config-file-only
	// secrets.common.allow-placeholder-for key.
	AllowPlaceholderFor []string
	// AgentVersion pins the agent version baked into the runner image on
	// rebuild. Empty means resolve the latest at build time.
	AgentVersion string
	// ProvisionHostConfig controls whether the agent's host config files are
	// copied into the VM (drop-in provisioning). A nil value enables it
	// (default); cmd resolves the launcher config and sets it explicitly.
	ProvisionHostConfig *bool
	// OpenLinks starts the host side of the in-VM xdg-open shim for the
	// session, so links opened in the VM open on the host (internal/opener).
	OpenLinks bool
	// MemoryFlagChanged reports whether the --memory flag was explicitly set on
	// this invocation. A persisted raise cannot take effect when the flag
	// supplies the value, so PersistMemory refuses to write a shadowed config
	// value (specs/resources/resource-limit-proposal.md).
	MemoryFlagChanged bool
	// Trace records the lifecycle stages the invocation reaches. cmd creates
	// it; the stages in vm and session record into it. Nil records nothing.
	Trace *lifecycle.Trace
	// Seams carries the run's behavior test seams. The zero value is
	// production (every stage uses its package default); only tests populate a
	// field.
	Seams Seams
}

// ReapPolicy controls what happens after the last client detaches from a VM.
type ReapPolicy struct {
	// AutoStopOnActiveSessions, when true, behaves like "auto-stop active sessions":
	// the VM is NOT held for in-flight agent work and stops promptly (via the msb
	// idle timeout) even while sessions are busy. When false (default), the reaper
	// holds the VM (keeper exec) while any session is busy or retrying below
	// MaxSessionRetries, then detaches and the idle timeout stops the VM.
	AutoStopOnActiveSessions bool
	// MaxSessionRetries is how many retry attempts a detach tolerates before
	// letting the VM idle out: a session retrying with an attempt below this cap
	// holds the VM, and one at or beyond it does not. 0 means use the package
	// default (10).
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
