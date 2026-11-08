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
// the kind enum and delegating the rest. Each delegation target is a
// package-level seam (a method expression) so the thin adapter is
// unit-testable without a live VM.
type realVolumeHandle struct {
	handle *msbSdk.VolumeHandle
}

var (
	realVolumeName          = (*msbSdk.VolumeHandle).Name
	realVolumePath          = (*msbSdk.VolumeHandle).Path
	realVolumeKind          = (*msbSdk.VolumeHandle).Kind
	realVolumeCreatedAt     = (*msbSdk.VolumeHandle).CreatedAt
	realVolumeQuotaMiB      = (*msbSdk.VolumeHandle).QuotaMiB
	realVolumeUsedBytes     = (*msbSdk.VolumeHandle).UsedBytes
	realVolumeCapacityBytes = (*msbSdk.VolumeHandle).CapacityBytes
	realVolumeDiskFormat    = (*msbSdk.VolumeHandle).DiskFormat
	realVolumeDiskFstype    = (*msbSdk.VolumeHandle).DiskFstype
	realVolumeLabels        = (*msbSdk.VolumeHandle).Labels
)

func (w realVolumeHandle) Name() string         { return realVolumeName(w.handle) }
func (w realVolumeHandle) Path() string         { return realVolumePath(w.handle) }
func (w realVolumeHandle) Kind() VolumeKind     { return volumeKindFrom(realVolumeKind(w.handle)) }
func (w realVolumeHandle) CreatedAt() time.Time { return realVolumeCreatedAt(w.handle) }
func (w realVolumeHandle) QuotaMiB() *uint32    { return realVolumeQuotaMiB(w.handle) }
func (w realVolumeHandle) UsedBytes() uint64    { return realVolumeUsedBytes(w.handle) }
func (w realVolumeHandle) CapacityBytes() *uint64 {
	return realVolumeCapacityBytes(w.handle)
}
func (w realVolumeHandle) DiskFormat() *string { return realVolumeDiskFormat(w.handle) }
func (w realVolumeHandle) DiskFstype() *string { return realVolumeDiskFstype(w.handle) }
func (w realVolumeHandle) Labels() map[string]string {
	return realVolumeLabels(w.handle)
}
