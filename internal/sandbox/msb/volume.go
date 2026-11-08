package msb

import (
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// VolumeHandle is the subset of *msb.VolumeHandle that the launcher needs.
type VolumeHandle interface {
	Name() string
	Path() string
	Kind() VolumeKind
	CreatedAt() time.Time
	QuotaMiB() *uint32
	UsedBytes() uint64
	CapacityBytes() *uint64
	DiskFormat() *string
	DiskFstype() *string
	Labels() map[string]string
}

// realVolumeHandle adapts *msbSdk.VolumeHandle to VolumeHandle, translating only
// the kind enum and delegating the rest.
type realVolumeHandle struct {
	handle *msbSdk.VolumeHandle
}

func (w realVolumeHandle) Name() string         { return w.handle.Name() }
func (w realVolumeHandle) Path() string         { return w.handle.Path() }
func (w realVolumeHandle) Kind() VolumeKind     { return volumeKindFrom(w.handle.Kind()) }
func (w realVolumeHandle) CreatedAt() time.Time { return w.handle.CreatedAt() }
func (w realVolumeHandle) QuotaMiB() *uint32    { return w.handle.QuotaMiB() }
func (w realVolumeHandle) UsedBytes() uint64    { return w.handle.UsedBytes() }
func (w realVolumeHandle) CapacityBytes() *uint64 {
	return w.handle.CapacityBytes()
}
func (w realVolumeHandle) DiskFormat() *string { return w.handle.DiskFormat() }
func (w realVolumeHandle) DiskFstype() *string { return w.handle.DiskFstype() }
func (w realVolumeHandle) Labels() map[string]string {
	return w.handle.Labels()
}
