package doctortest

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestInstallFailFastFactoriesPanic(t *testing.T) {
	MockedCheckAll(t, true)
	MockedCheckDocker(t, true)
	MockedEnsureInstalled(t, false)

	InstallFailFast()

	assertFailFastFactoriesPanic(t)
}

// TestMockedFactoriesRestoreFailFastOnCleanup proves each Mocked* helper puts
// the fail-fast default back when its test ends. A func-pointer comparison is
// useless here — two closures minted from the same literal compare equal — so
// the restored factory's behaviour is asserted: the fail-fast default panics.
func TestMockedFactoriesRestoreFailFastOnCleanup(t *testing.T) {
	t.Run("install mocks", func(t *testing.T) {
		MockedCheckAll(t, true)
		MockedCheckDocker(t, true)
		MockedEnsureInstalled(t, false)
	})

	assertFailFastFactoriesPanic(t)
}

func assertFactoryPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic from fail-fast factory, got none", name)
		}
	}()
	fn()
}

// assertFailFastFactoriesPanic asserts the CheckAll and CheckDocker factories are
// the fail-fast default installed by InstallFailFast. It deliberately stays off
// the ensure-installed factory: ensureMsbInstalled is unexported, so its
// fail-fast default is asserted from doctor's own internal tests instead.
func assertFailFastFactoriesPanic(t *testing.T) {
	t.Helper()
	assertFactoryPanics(t, "CheckDocker", func() { _ = doctor.CheckDocker(context.Background(), docker.Get()) })
	assertFactoryPanics(t, "CheckAll", func() {
		testUI := termio.NewTestMock(t)
		_ = doctor.CheckAll(context.Background(), docker.Get(), &testUI)
	})
}
