package msb

import (
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// ImageHandle is the subset of *msb.ImageHandle that the launcher needs.
type ImageHandle interface {
	Reference() string
	ManifestDigest() string
	LastUsedAt() time.Time
	SizeBytes() *int64
	CreatedAt() time.Time
}

// ImageDetail is the inspected detail of a cached image: its manifest identity
// plus the parsed OCI config and layer list. It mirrors the useful fields of
// microsandbox's ImageDetail without exposing the SDK handle.
//
//exhaustruct:ignore
type ImageDetail struct {
	Reference      string
	ManifestDigest string
	Architecture   string
	OS             string
	SizeBytes      *int64
	CreatedAt      time.Time
	Config         msbSdk.ImageConfig
	Layers         []msbSdk.ImageLayer
}
