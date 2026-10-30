package msb

import (
	"context"
	"fmt"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestIsUnsupportedOperation(t *testing.T) {
	err := &msbSdk.Error{Kind: msbSdk.ErrUnsupportedOperation, Message: "nope"}
	if !IsUnsupportedOperation(err) {
		t.Error("IsUnsupportedOperation(direct) = false, want true")
	}
	if !IsUnsupportedOperation(fmt.Errorf("wrap: %w", err)) {
		t.Error("IsUnsupportedOperation(wrapped) = false, want true")
	}
	if IsUnsupportedOperation(&msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound}) {
		t.Error("IsUnsupportedOperation(other kind) = true, want false")
	}
	if IsUnsupportedOperation(nil) {
		t.Error("IsUnsupportedOperation(nil) = true, want false")
	}
}

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
