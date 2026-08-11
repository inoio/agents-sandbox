package sandbox

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/doctor"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// Re-exported doctor module symbols. These preserve the public API of the
// sandbox core so that cmd tests can override CheckAll and SetEnsureInstalled
// at the sandbox surface.

// CheckAllFunc is an overridable CheckAll function for testing at the
// sandbox surface. Tests assign to this var and restore via t.Cleanup.
// The default delegates to doctor.CheckAll.
//
//nolint:gochecknoglobals // Re-export surfaces a test seam for cmd tests.
var CheckAllFunc = doctor.CheckAll

// CheckAll runs all prerequisite checks and reports orphaned VMs.
func CheckAll(ctx context.Context, ui termio.UI) bool {
	return CheckAllFunc(ctx, ui)
}

// SetEnsureInstalled re-exports the doctor's SetEnsureInstalled so that
// test code in cmd can override the ensureInstalled seam.
//
//nolint:gochecknoglobals // Re-export of a function value.
var SetEnsureInstalled = doctor.SetEnsureInstalled
