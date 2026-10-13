package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
)

// MockedEnsureInstalled replaces the ensureInstalled factory used by the
// doctor package. The original factory is returned so callers can restore
// it after their test.
//
// Usage from an external test package:
//
//	orig := doctor.SetEnsureInstalled(func(ctx context.Context) error {
//	    return nil // succeed
//	})
//	t.Cleanup(func() { doctor.SetEnsureInstalled(orig) })
func MockedEnsureInstalled(t *testing.T, raise bool) {
	orig := ensureInstalledFunc
	ensureInstalledFunc = ensureInstalledMockFunc(raise)
	t.Cleanup(func() { ensureInstalledFunc = orig })
}

func ensureInstalledMockFunc(raise bool) func(ctx context.Context) error {
	return func(context.Context) error {
		if raise {
			return errors.New("mocked error")
		}
		return nil
	}
}

func checkAllMockFunc(success bool) func(context.Context, termio.UI) bool {
	return func(_ context.Context, _ termio.UI) bool { return success }
}

func MockedCheckAll(t *testing.T, success bool) {
	orig := checkAllFunc
	checkAllFunc = checkAllMockFunc(success)
	t.Cleanup(func() { checkAllFunc = orig })
}

// MockedSelectedBackend overrides the selected-backend seam so a test can
// exercise both the msb and moby check collections hermetically.
func MockedSelectedBackend(t *testing.T, name string) {
	orig := selectedBackend
	selectedBackend = func() string { return name }
	t.Cleanup(func() { selectedBackend = orig })
}

// MockedCheckDocker replaces the Docker prerequisite check with one returning err.
func MockedCheckDocker(t *testing.T, err error) {
	orig := checkDocker
	checkDocker = func(context.Context) error { return err }
	t.Cleanup(func() { checkDocker = orig })
}

// MockedRealCheckAll restores the real CheckAll aggregation, so a test can
// exercise full check collection through the CLI despite the fail-fast default.
func MockedRealCheckAll(t *testing.T) {
	orig := checkAllFunc
	checkAllFunc = realCheckAll
	t.Cleanup(func() { checkAllFunc = orig })
}

func InstallFailFast() {
	checkAllFunc = func(context.Context, termio.UI) bool {
		panic("doctor.CheckAll not mocked; use doctor.MockedCheckAll(t, ...) in the test")
	}
	ensureInstalledFunc = func(context.Context) error {
		panic("doctor.EnsureInstalled not mocked; use doctor.SetEnsureInstalled(t, ...) in the test")
	}
}
