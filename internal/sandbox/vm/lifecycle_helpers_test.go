package vm

import (
	"context"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestResumeSandboxByStatusUnavailable(t *testing.T) {
	unavailable := []msbSdk.SandboxStatus{
		msbSdk.SandboxStatusDraining,
		msbSdk.SandboxStatusPaused,
		msbSdk.SandboxStatus("bogus"),
	}
	for _, status := range unavailable {
		if _, _, err := resumeSandboxByStatus(context.Background(), nil, status, "proj"); err == nil {
			t.Errorf("resumeSandboxByStatus(%v) error = nil, want an error", status)
		}
	}
}
