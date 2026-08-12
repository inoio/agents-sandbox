package sandbox

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/doctor"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// Re-exported doctor module symbols preserve the public API of the sandbox
// core so that cmd/opencode-msb continues to compile without changing its import paths.

// CheckAll runs all prerequisite checks and reports orphaned VMs.
func CheckAll(ctx context.Context, ui termio.UI) bool {
	return doctor.CheckAll(ctx, ui)
}
