package msb

import (
	"context"
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// TestAttachEndedAbnormally pins the abnormal-end classification: a clean exit
// and an ordinary non-zero exit are normal, while a signal death (-1) or an
// attach error is abnormal (spec: -1 is the guest's signal death; a non-zero
// exit code is a deliberate agent exit, not a crash).
func TestAttachEndedAbnormally(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attachErr error
		exitCode  int
		want      bool
	}{
		{"clean exit", nil, 0, false},
		{"non-zero exit code", nil, 3, false},
		{"signal death", nil, -1, true},
		{"attach error", errors.New("boom"), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AttachEndedAbnormally(tc.attachErr, tc.exitCode); got != tc.want {
				t.Errorf("AttachEndedAbnormally(%v, %d) = %v, want %v", tc.attachErr, tc.exitCode, got, tc.want)
			}
		})
	}
}

// TestSandboxHandleFromPropagatesInfoError pins that a failed identity
// observation fails the handle resolution rather than yielding a handle whose
// status and timestamps are silently zero.
func TestSandboxHandleFromPropagatesInfoError(t *testing.T) {
	cause := errors.New("info boom")
	swapSeam(
		t,
		&realSandboxHandleInfo,
		func(*msbSdk.Sandbox, context.Context) (*msbSdk.SandboxInfo, error) {
			return nil, cause
		},
	)

	if _, err := sandboxHandleFrom(context.Background(), &msbSdk.Sandbox{}); !errors.Is(err, cause) {
		t.Fatalf("sandboxHandleFrom() error = %v, want %v", err, cause)
	}
}

// TestSandboxConfigInspectionRejectsInvalidJSON pins that a config observation
// that is not the inspection envelope is reported, so it is not mistaken for an
// empty config.
func TestSandboxConfigInspectionRejectsInvalidJSON(t *testing.T) {
	if _, err := sandboxConfigInspection("not json"); err == nil {
		t.Fatal("sandboxConfigInspection() error = nil, want a decode error")
	}
}

// TestRealSandboxHandleTimestamps pins that the identity observation's
// timestamps are forwarded, and that an unavailable observation stays zero.
func TestRealSandboxHandleTimestamps(t *testing.T) {
	createdAt := time.Unix(1_700_000_000, 0)
	updatedAt := time.Unix(1_700_000_100, 0)
	h := realSandboxHandle{
		handle: &msbSdk.Sandbox{},
		info:   &msbSdk.SandboxInfo{CreatedAt: &createdAt, UpdatedAt: &updatedAt},
	}
	if got := h.CreatedAt(); !got.Equal(createdAt) {
		t.Errorf("CreatedAt() = %v, want %v", got, createdAt)
	}
	if got := h.UpdatedAt(); !got.Equal(updatedAt) {
		t.Errorf("UpdatedAt() = %v, want %v", got, updatedAt)
	}
}
