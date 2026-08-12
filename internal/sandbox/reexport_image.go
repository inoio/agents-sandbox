package sandbox

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/image"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// Re-exported image module symbols preserve the public API of the sandbox
// core so that cmd/opencode-msb continues to compile without changing its
// import paths.

// EnsureImage re-exports the image module's EnsureImage.
func EnsureImage(
	ctx context.Context,
	projectSlug string,
	force bool,
	ui termio.UI,
) (string, string, map[string]string, error) {
	return image.EnsureImage(ctx, projectSlug, force, ui)
}
