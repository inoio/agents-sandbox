package sandbox

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/doctor"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// CheckAll runs all prerequisite checks and reports orphaned VMs.
func CheckAll(ctx context.Context, ui termio.UI) bool {
	return doctor.CheckAll(ctx, ui)
}
