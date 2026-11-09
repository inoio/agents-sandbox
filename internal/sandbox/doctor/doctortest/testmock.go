//deadcode:ignore reason="test support: referenced only by _test.go files"
package doctortest

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// MockedEnsureInstalled replaces the ensure-installed seam doctor's checkMsb
// reaches, and restores the previous seam when the test ends.
//
// Usage:
//
//	doctortest.MockedEnsureInstalled(t, true) // fail
func MockedEnsureInstalled(t *testing.T, raise bool) {
	orig := doctor.SetEnsureInstalled(ensureInstalledMockFunc(raise))
	t.Cleanup(func() { doctor.SetEnsureInstalled(orig) })
}

// errMockedEnsureInstalled is the cause the ensureInstalled mock returns, so
// tests can assert the cause survives wrapping through errors.Is.
var errMockedEnsureInstalled = errors.New("mocked error")

func ensureInstalledMockFunc(raise bool) func(ctx context.Context) error {
	return func(context.Context) error {
		if raise {
			return errMockedEnsureInstalled
		}
		return nil
	}
}

func checkAllMockFunc(success bool) func(context.Context, docker.Client, termio.Logger) bool {
	return func(_ context.Context, _ docker.Client, _ termio.Logger) bool { return success }
}

func checkDockerMockFunc(success bool) func(context.Context, docker.Client) error {
	return func(context.Context, docker.Client) error {
		if success {
			return nil
		}
		return errors.New("mocked docker not available")
	}
}

func MockedCheckAll(t *testing.T, success bool) {
	orig := doctor.SetCheckAll(checkAllMockFunc(success))
	t.Cleanup(func() { doctor.SetCheckAll(orig) })
}

func MockedCheckDocker(t *testing.T, success bool) {
	orig := doctor.SetCheckDocker(checkDockerMockFunc(success))
	t.Cleanup(func() { doctor.SetCheckDocker(orig) })
}

func InstallFailFast() {
	doctor.SetCheckAll(func(context.Context, docker.Client, termio.Logger) bool {
		panic("doctor.CheckAll not mocked; use doctortest.MockedCheckAll(t, ...) in the test")
	})
	doctor.SetCheckDocker(func(context.Context, docker.Client) error {
		panic("doctor.CheckDocker not mocked; use doctortest.MockedCheckDocker(t, ...) in the test")
	})
	doctor.SetEnsureInstalled(func(context.Context) error {
		panic("doctor.EnsureInstalled not mocked; use doctortest.MockedEnsureInstalled(t, ...) in the test")
	})
}
