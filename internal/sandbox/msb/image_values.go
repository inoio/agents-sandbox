package msb

import msbSdk "github.com/superradcompany/microsandbox/sdk/go"

// ImageConfig is the SDK's parsed OCI image-config observation. The launcher
// surfaces it directly rather than mirroring its fields.
type ImageConfig = msbSdk.ImageConfig

// ImageLayer is the SDK's one-layer-of-an-image-manifest observation.
type ImageLayer = msbSdk.ImageLayer

// ImagePruneReport is the SDK's image-prune summary observation.
type ImagePruneReport = msbSdk.ImagePruneReport
