package vm

import (
	"context"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// TestResumeSandboxByStatusUnavailable pins the two refusals: a known status
// the VM cannot be resumed from is "not available", any other is "unexpected".
func TestResumeSandboxByStatusUnavailable(t *testing.T) {
	for _, tc := range []struct {
		status  msbSdk.SandboxStatus
		message string
	}{
		{msbSdk.SandboxStatusDraining, "is not available"},
		{msbSdk.SandboxStatusPaused, "is not available"},
		{msbSdk.SandboxStatus("bogus"), "unexpected status"},
	} {
		_, _, err := resumeSandboxByStatus(context.Background(), nil, tc.status, "proj")
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("resumeSandboxByStatus(%v) error = %v, want one containing %q", tc.status, err, tc.message)
		}
	}
}
