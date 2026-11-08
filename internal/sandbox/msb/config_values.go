package msb

// SandboxConfig is the launcher's read-back view of a sandbox's persisted
// configuration: the fields the launcher compares on a revisited VM. The
// adapter translates the SDK's config onto it, and fills the mounts and
// published ports through ConfigMounts/ConfigPorts because the SDK decode drops
// them (specs/vm/live-mount-readback.md). A field the launcher does not model
// is absent; the drift register in translate.go names it.
//
//exhaustruct:ignore
type SandboxConfig struct {
	Image        string
	Labels       map[string]string
	CPUs         uint8
	MaxCPUs      uint8
	MemoryMiB    uint32
	MaxMemoryMiB uint32
	RootDisk     *RootDiskConfig
	Volumes      map[string]MountConfig
	PortBindings []PortBinding
}

// RootDiskConfig is the launcher's read-back view of a managed or disk-image
// root disk. The launcher only compares its size.
type RootDiskConfig struct {
	SizeMiB uint32
}

// ModifyOptions is the launcher's input to SandboxResourceControl.Modify. A
// zero field is left unchanged, matching the SDK contract. Only the CPU,
// memory, root-disk and policy knobs the launcher uses are modeled; the SDK's
// env/label/secret/port patching is not used by the launcher.
//
//exhaustruct:ignore
type ModifyOptions struct {
	CPUs            uint8
	MemoryMiB       uint32
	RootDiskSizeMiB uint32
	Policy          ModificationPolicy
}

// ModificationPolicy selects how a modification is planned or applied. The
// string values mirror the SDK's so they round-trip unchanged.
type ModificationPolicy string

const (
	ModificationPolicyNoRestart ModificationPolicy = "no_restart"
	ModificationPolicyNextStart ModificationPolicy = "next_start"
	ModificationPolicyRestart   ModificationPolicy = "restart"
)

// SandboxModificationPlan is the launcher's view of the plan Modify returns.
// The launcher surfaces only the blocking conflicts.
//
//exhaustruct:ignore
type SandboxModificationPlan struct {
	Conflicts []ModificationConflict
}

// ModificationConflict is one conflict that blocks applying a modification.
type ModificationConflict struct {
	Field   string
	Message string
}

// DiskCompactionResult is the launcher's view of a disk-compaction run.
//
//exhaustruct:ignore
type DiskCompactionResult struct {
	InputLayers       uint32
	SelectedLayers    uint32
	OutputLayers      uint32
	MaterializedBytes uint64
	TotalUs           uint64
	PauseUs           uint64
	Disks             []DiskCompactionDiskResult
}

// DiskCompactionDiskResult is the per-disk outcome of a compaction.
//
//exhaustruct:ignore
type DiskCompactionDiskResult struct {
	GuestPath         string
	InputLayers       uint32
	SelectedLayers    uint32
	OutputLayers      uint32
	MaterializedBytes uint64
	TotalUs           uint64
}
