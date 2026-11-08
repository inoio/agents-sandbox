package msb

// SandboxStatus reports the lifecycle state of a sandbox. The launcher owns the
// type so its interfaces never speak an SDK enum; the string values match the
// runtime's so string comparisons and rendering keep working.
type SandboxStatus string

const (
	SandboxStatusCreated  SandboxStatus = "created"
	SandboxStatusStarting SandboxStatus = "starting"
	SandboxStatusRunning  SandboxStatus = "running"
	SandboxStatusDraining SandboxStatus = "draining"
	SandboxStatusPaused   SandboxStatus = "paused"
	SandboxStatusStopped  SandboxStatus = "stopped"
	SandboxStatusCrashed  SandboxStatus = "crashed"
)

// BackendKind names the runtime a sandbox runs on.
type BackendKind string

const (
	BackendLocal   BackendKind = "local"
	BackendCloud   BackendKind = "cloud"
	BackendUnknown BackendKind = "unknown"
)

// VolumeKind selects the backing store of a named volume.
type VolumeKind string

const (
	VolumeKindDir  VolumeKind = "dir"
	VolumeKindDisk VolumeKind = "disk"
)
