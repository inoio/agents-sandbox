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
	Image        string
	CPUs         uint8
	MaxCPUs      uint8
	MemoryMiB    uint32
	MaxMemoryMiB uint32
	// RootDiskMiB sets a managed root disk of this size; 0 leaves the runtime
	// default (no managed root disk).
	RootDiskMiB uint32
	Workdir     string
	Detached    bool
	IdleTimeout time.Duration
	Env         map[string]string
	Labels      map[string]string
	Mounts      map[string]MountConfig
	Ports       []PortBinding
	Secrets     []SecretEntry
	Network     NetworkSpec
}

// VolumeSpec is the launcher's input to CreateVolume.
type VolumeSpec struct {
	Kind VolumeKind
}
