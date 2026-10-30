package volume

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// VolumeDetail is the inspected identity and storage metadata of a home volume.
type VolumeDetail struct {
	Name          string
	Kind          string
	Path          string
	QuotaMiB      *uint32
	UsedBytes     uint64
	CapacityBytes *uint64
	DiskFormat    *string
	DiskFstype    *string
	Labels        map[string]string
	CreatedAt     string
}

// InspectVolume returns the full detail of a home volume by name.
func InspectVolume(ctx context.Context, name string) (VolumeDetail, error) {
	handle, err := msb.Get().GetVolume(ctx, name)
	if err != nil {
		return VolumeDetail{}, fmt.Errorf("inspect volume %q: %w", name, err)
	}
	return VolumeDetail{
		Name:          handle.Name(),
		Kind:          string(handle.Kind()),
		Path:          handle.Path(),
		QuotaMiB:      handle.QuotaMiB(),
		UsedBytes:     handle.UsedBytes(),
		CapacityBytes: handle.CapacityBytes(),
		DiskFormat:    handle.DiskFormat(),
		DiskFstype:    handle.DiskFstype(),
		Labels:        handle.Labels(),
		CreatedAt:     FormatVolumeTime(handle.CreatedAt()),
	}, nil
}
