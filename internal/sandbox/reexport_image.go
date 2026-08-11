package sandbox

import (
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/image"
)

// EnsureImage is re-exported from the image module so the sandbox core and cmd
// stay compile-compatible without circular imports.
//
//nolint:gochecknoglobals // re-export from image module
var EnsureImage = image.EnsureImage
