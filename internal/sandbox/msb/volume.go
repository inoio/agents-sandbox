package msb

import (
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// VolumeHandle is the subset of *msb.VolumeHandle that the launcher needs.
type VolumeHandle interface {
	Name() string
	Path() string
	Kind() msbSdk.VolumeKind
	CreatedAt() time.Time
	QuotaMiB() *uint32
	UsedBytes() uint64
	CapacityBytes() *uint64
	DiskFormat() *string
	DiskFstype() *string
	Labels() map[string]string
}
