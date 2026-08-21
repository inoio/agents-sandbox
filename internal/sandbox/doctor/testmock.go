package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/termio"
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

func checkDockerMockFunc(success bool) func(context.Context) error {
	return func(context.Context) error {
		if success {
			return nil
		}
		return errors.New("mocked docker not available")
	}
}

func MockedCheckAll(t *testing.T, success bool) {
	orig := checkAllFunc
	checkAllFunc = checkAllMockFunc(success)
	t.Cleanup(func() { checkAllFunc = orig })
}

func MockedCheckDocker(t *testing.T, success bool) {
	orig := checkDockerFunc
	checkDockerFunc = checkDockerMockFunc(success)
	t.Cleanup(func() { checkDockerFunc = orig })
}

func InstallFailFast() {
	checkAllFunc = func(context.Context, termio.UI) bool {
		panic("doctor.CheckAll not mocked; use doctor.MockedCheckAll(t, ...) in the test")
	}
	checkDockerFunc = func(context.Context) error {
		panic("doctor.CheckDocker not mocked; use doctor.MockedCheckDocker(t, ...) in the test")
	}
	ensureInstalledFunc = func(context.Context) error {
		panic("doctor.EnsureInstalled not mocked; use doctor.SetEnsureInstalled(t, ...) in the test")
	}
}
