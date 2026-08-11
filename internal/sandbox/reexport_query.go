package sandbox

import (
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/image"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/session"
)

// Info is a re-export of session.Info so that cmd (which references sandbox.Info)
// continues to compile without changes after the session module was extracted.
type Info = session.Info

// ImageInfo is an alias for image.Info from the image module.
type ImageInfo = image.Info

// ListImages re-exports the image module's ListImages through the sandbox core.
//
//nolint:gochecknoglobals // Re-export preserves sandbox core public API
var ListImages = image.ListImages
