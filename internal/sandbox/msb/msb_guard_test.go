package msb

import (
	"context"
	"testing"
)

func TestInstallFailFastGet_CausesPanicWithoutMock(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("fail-fast msb.Get should panic when no mock is installed, but did not")
		}
	}()
	InstallFailFastGet()
	Get().EnsureInstalled(context.Background())
}

func TestInstallFailFastGet_OptInMockDoesNotPanic(t *testing.T) {
	WithMsbMock(t, &MockMsbClient{})
	Get().EnsureInstalled(context.Background())
	// no panic -> pass
}
