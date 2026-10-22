package doctor

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestCheckAllUsesMockedCheckAll(t *testing.T) {
	tests := []struct {
		name    string
		success bool
	}{
		{"success", true},
		{"failure", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testUI := termio.NewTestMock(t)
			MockedCheckAll(t, tc.success)

			if got := CheckAll(context.Background(), &testUI); got != tc.success {
				t.Errorf("CheckAll() = %v, want %v", got, tc.success)
			}
		})
	}
}

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

// assertFailFastFactoriesPanic asserts every doctor factory is the fail-fast
// default installed by InstallFailFast.
func assertFailFastFactoriesPanic(t *testing.T) {
	t.Helper()
	assertFactoryPanics(t, "CheckDocker", func() { _ = CheckDocker(context.Background()) })
	assertFactoryPanics(t, "CheckAll", func() {
		testUI := termio.NewTestMock(t)
		_ = CheckAll(context.Background(), &testUI)
	})
	assertFactoryPanics(t, "ensureMsbInstalled", func() { _ = ensureMsbInstalled(context.Background()) })
}
