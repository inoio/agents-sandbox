package doctor

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
)

// ensureInstalled is indirected so tests can stub the SDK runtime download.
// It defaults to the real EnsureInstalled; tests reassign it and restore via
// t.Cleanup.
var ensureInstalled = func(ctx context.Context) error { //nolint:gochecknoglobals // test seam, swapped in tests
	return msb.Get().EnsureInstalled(ctx)
}

// SetEnsureInstalled replaces the ensureInstalled factory used by the
// doctor package. The original factory is returned so callers can restore
// it after their test.
//
// Usage from an external test package:
//
//	orig := doctor.SetEnsureInstalled(func(ctx context.Context) error {
//	    return nil // succeed
//	})
//	t.Cleanup(func() { doctor.SetEnsureInstalled(orig) })
func SetEnsureInstalled(f func(ctx context.Context) error) func(ctx context.Context) error {
	orig := ensureInstalled
	ensureInstalled = f
	return orig
}

// CheckAllFunc is an overridable CheckAll function for testing.
// Tests override it and restore via t.Cleanup.
//
//nolint:gochecknoglobals // test seam, swapped in external test packages
var CheckAllFunc = checkAllReal
