package msb

import (
	"context"
	"encoding/json"
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

// TestSandboxHandleFromCapturesObservations pins the success path: a resolved
// handle carries the identity and the decoded config observation, so its image
// reads back rather than being dropped as unreadable.
func TestSandboxHandleFromCapturesObservations(t *testing.T) {
	swapSeam(
		t,
		&realSandboxHandleInfo,
		func(*msbSdk.Sandbox, context.Context) (*msbSdk.SandboxInfo, error) {
			return &msbSdk.SandboxInfo{Status: msbSdk.SandboxStatusRunning}, nil
		},
	)
	swapSeam(
		t,
		&realSandboxHandleConfigJSON,
		func(*msbSdk.Sandbox, context.Context) (string, error) {
			return `{"kind":"local","spec":{"image":{"Oci":{"reference":"alpine:3"}}}}`, nil
		},
	)

	handle, err := sandboxHandleFrom(context.Background(), &msbSdk.Sandbox{})
	if err != nil || handle == nil {
		t.Fatalf("sandboxHandleFrom() = (%v, %v), want a handle, nil", handle, err)
	}
	if handle.config == nil {
		t.Fatal("sandboxHandleFrom() did not decode the config observation")
	}
	if got := handle.Image(); got != "alpine:3" {
		t.Errorf("Image() = %q, want alpine:3", got)
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

// TestSandboxConfigInspectionDecodes pins the success value, and
// TestSandboxConfigInspectionRejectsInvalidJSON pins that a config observation
// that is not the inspection envelope is reported (wrapping the JSON error) so
// it is not mistaken for an empty config.
func TestSandboxConfigInspectionDecodes(t *testing.T) {
	inspection, err := sandboxConfigInspection(`{"kind":"local"}`)
	if err != nil || inspection == nil {
		t.Fatalf("sandboxConfigInspection() = (%v, %v), want a value, nil", inspection, err)
	}
}

func TestSandboxConfigInspectionRejectsInvalidJSON(t *testing.T) {
	var syntaxErr *json.SyntaxError
	if _, err := sandboxConfigInspection("not json"); !errors.As(err, &syntaxErr) {
		t.Fatalf("sandboxConfigInspection() error = %v, want it to wrap the JSON syntax error", err)
	}
}

// TestRealSandboxHandleTimestamps pins that the identity observation's
// timestamps are forwarded, and that a present observation with an absent
// timestamp stays zero rather than dereferencing nil.
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

	absent := realSandboxHandle{handle: &msbSdk.Sandbox{}, info: &msbSdk.SandboxInfo{}}
	if got := absent.CreatedAt(); !got.IsZero() {
		t.Errorf("CreatedAt() with no observation timestamp = %v, want zero time", got)
	}
	if got := absent.UpdatedAt(); !got.IsZero() {
		t.Errorf("UpdatedAt() with no observation timestamp = %v, want zero time", got)
	}
}
