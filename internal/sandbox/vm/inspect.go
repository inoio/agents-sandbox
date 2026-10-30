package vm

import (
	"context"
	"fmt"
	"sort"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// MountDetail describes one filesystem mount of an inspected sandbox.
type MountDetail struct {
	GuestPath string
	Kind      string
	Source    string
	Readonly  bool
}

// SandboxDetail is the inspected identity, configuration, and resource shape of
// a sandbox VM.
type SandboxDetail struct {
	Name         string
	ID           string
	Status       string
	Image        string
	Backend      string
	CreatedAt    string
	UpdatedAt    string
	Labels       map[string]string
	Mounts       []MountDetail
	MemoryMiB    uint32
	CPUs         uint8
	MaxMemoryMiB uint32
	MaxCPUs      uint8
}

// InspectSandbox returns the full detail of a sandbox VM by name.
func InspectSandbox(ctx context.Context, name string) (SandboxDetail, error) {
	handle, err := msb.Get().GetSandbox(ctx, name)
	if err != nil {
		return SandboxDetail{}, fmt.Errorf("inspect sandbox %q: %w", name, err)
	}
	cfg, err := handle.Config()
	if err != nil {
		return SandboxDetail{}, fmt.Errorf("inspect sandbox %q: read config: %w", name, err)
	}
	liveMounts, err := handle.ConfigMounts()
	if err != nil {
		return SandboxDetail{}, fmt.Errorf("inspect sandbox %q: read mounts: %w", name, err)
	}
	var labels map[string]string
	var memoryMiB, maxMemoryMiB uint32
	var cpus, maxCPUs uint8
	if cfg != nil {
		labels = cfg.Labels
		memoryMiB = cfg.MemoryMiB
		maxMemoryMiB = cfg.MaxMemoryMiB
		cpus = cfg.CPUs
		maxCPUs = cfg.MaxCPUs
	}
	return SandboxDetail{
		Name:         handle.Name(),
		ID:           handle.ID(),
		Status:       string(handle.Status()),
		Image:        handle.Image(),
		Backend:      string(handle.BackendKind()),
		CreatedAt:    FormatTime(handle.CreatedAt()),
		UpdatedAt:    FormatTime(handle.UpdatedAt()),
		Labels:       labels,
		Mounts:       mountDetails(liveMounts),
		MemoryMiB:    memoryMiB,
		CPUs:         cpus,
		MaxMemoryMiB: maxMemoryMiB,
		MaxCPUs:      maxCPUs,
	}, nil
}

// mountDetails renders a sandbox's guest-path mount map as a stable,
// guest-path-sorted slice.
func mountDetails(volumes map[string]msbSdk.MountConfig) []MountDetail {
	mounts := make([]MountDetail, 0, len(volumes))
	for guestPath, mount := range volumes {
		mounts = append(mounts, MountDetail{
			GuestPath: guestPath,
			Kind:      mountKindString(mount.Kind()),
			Source:    mountSource(mount),
			Readonly:  mount.Readonly,
		})
	}
	sort.Slice(mounts, func(i, j int) bool { return lessMountByGuestPath(mounts[i], mounts[j]) })
	return mounts
}

// lessMountByGuestPath orders two mount details by guest path, a strict weak
// ordering: a path never precedes itself.
func lessMountByGuestPath(a, b MountDetail) bool {
	return a.GuestPath < b.GuestPath
}

// mountKindString names a mount kind for display, or "unknown" for an
// unrecognized value (including a pre-kind persisted config).
func mountKindString(kind msbSdk.MountKind) string {
	switch kind {
	case msbSdk.MountKindBind:
		return "bind"
	case msbSdk.MountKindNamed:
		return "named"
	case msbSdk.MountKindTmpfs:
		return "tmpfs"
	case msbSdk.MountKindDisk:
		return "disk"
	case msbSdk.MountKindOwned:
		return "owned"
	default:
		return "unknown"
	}
}

// mountSource returns the backing source of a mount: the host path for a bind,
// the volume name for a named mount, the image path for a disk, or empty for
// kinds without a single source.
func mountSource(mount msbSdk.MountConfig) string {
	switch mount.Kind() {
	case msbSdk.MountKindBind:
		return mount.Bind
	case msbSdk.MountKindNamed:
		return mount.Named
	case msbSdk.MountKindDisk:
		return mount.Disk
	case msbSdk.MountKindOwned:
		return mount.Owned
	case msbSdk.MountKindTmpfs:
		return ""
	default:
		return ""
	}
}
