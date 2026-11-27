package msb

import "time"

// SandboxSpec is the launcher's input to CreateSandbox. The adapter translates
// every field into the SDK option list; a field the launcher does not model is
// absent. Mounts are launcher-shaped mount descriptors built by the caller (the
// fixed /home/dev, /workspace and /tmp layout stays a launcher concern, not the
// adapter's).
//
//exhaustruct:ignore
type SandboxSpec struct {
	Env         map[string]string
	Labels      map[string]string
	Mounts      map[string]MountConfig
	Image       string
	Workdir     string
	Network     NetworkSpec
	Ports       []PortBinding
	Secrets     []SecretEntry
	IdleTimeout time.Duration
	// MaxDuration bounds the sandbox's uptime since its last start; zero
	// leaves it unlimited.
	MaxDuration  time.Duration
	MemoryMiB    uint32
	MaxMemoryMiB uint32
	// RootDiskMiB sets a managed root disk of this size; 0 leaves the runtime
	// default (no managed root disk).
	RootDiskMiB uint32
	CPUs        uint8
	MaxCPUs     uint8
	Detached    bool
}

// VolumeSpec is the launcher's input to CreateVolume.
type VolumeSpec struct {
	Kind VolumeKind
}
