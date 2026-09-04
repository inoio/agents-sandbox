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
	InstallFailFast()

	assertPanics := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: expected panic from fail-fast factory, got none", name)
			}
		}()
		fn()
	}

	assertPanics("CheckDocker", func() { _ = CheckDocker(context.Background()) })
	assertPanics("CheckAll", func() {
		testUI := termio.NewTestMock(t)
		_ = CheckAll(context.Background(), &testUI)
	})
	assertPanics("ensureMsbInstalled", func() { _ = ensureMsbInstalled(context.Background()) })
}
