package msb

import (
	"context"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// VolumeHandle is the subset of *msb.Volume that the launcher needs.
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

// realVolumeHandle adapts *msbSdk.Volume to VolumeHandle. The SDK moved the
// mutable measurements (quota, usage, capacity and labels) behind a
// context-bearing Info observation while identity (name, path, kind) stays
// synchronous. The adapter captures one Info observation when the handle is
// resolved, so the launcher's synchronous interface is preserved; a measurement
// the provider did not report stays nil or zero rather than becoming a value.
//
// Each delegation target is a package-level seam (a method expression or
// function) so the thin adapter is unit-testable without a live VM.
type realVolumeHandle struct {
	handle *msbSdk.Volume
	info   *msbSdk.VolumeInfo
}

var (
	realVolumeName = (*msbSdk.Volume).Name
	realVolumePath = (*msbSdk.Volume).Path
	realVolumeKind = (*msbSdk.Volume).Kind
	realVolumeInfo = (*msbSdk.Volume).Info
)

// volumeHandleFrom resolves the Info observation the synchronous handle needs
// and wraps the volume.
func volumeHandleFrom(ctx context.Context, volume *msbSdk.Volume) (realVolumeHandle, error) {
	info, err := realVolumeInfo(volume, ctx)
	if err != nil {
		return realVolumeHandle{}, err
	}
	return realVolumeHandle{handle: volume, info: info}, nil
}

func (w realVolumeHandle) Name() string     { return realVolumeName(w.handle) }
func (w realVolumeHandle) Path() string     { return realVolumePath(w.handle) }
func (w realVolumeHandle) Kind() VolumeKind { return volumeKindFrom(realVolumeKind(w.handle)) }
func (w realVolumeHandle) CreatedAt() time.Time {
	if w.info == nil || w.info.CreatedAt == nil {
		return time.Time{}
	}
	return *w.info.CreatedAt
}

func (w realVolumeHandle) QuotaMiB() *uint32 {
	if w.info == nil {
		return nil
	}
	return w.info.QuotaMiB
}

func (w realVolumeHandle) UsedBytes() uint64 {
	if w.info == nil || w.info.UsedBytes == nil {
		return 0
	}
	return *w.info.UsedBytes
}

func (w realVolumeHandle) CapacityBytes() *uint64 {
	if w.info == nil {
		return nil
	}
	return w.info.CapacityBytes
}

func (w realVolumeHandle) DiskFormat() *string {
	if w.info == nil {
		return nil
	}
	return w.info.DiskFormat
}

func (w realVolumeHandle) DiskFstype() *string {
	if w.info == nil {
		return nil
	}
	return w.info.DiskFstype
}

func (w realVolumeHandle) Labels() map[string]string {
	if w.info == nil {
		return nil
	}
	return w.info.Labels
}
