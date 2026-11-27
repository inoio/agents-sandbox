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

// TestSandboxHandleStatusIsCaptured pins the captured-observation contract:
// Status() reports the observation taken when the handle was resolved, not a
// fresh lookup. A consumer that needs live state re-resolves the handle or uses
// the status-gated Connect.
func TestSandboxHandleStatusIsCaptured(t *testing.T) {
	status := msbSdk.SandboxStatusRunning
	swapSeam(t, &realSandboxHandleInfo, func(*msbSdk.Sandbox, context.Context) (*msbSdk.SandboxInfo, error) {
		return &msbSdk.SandboxInfo{Status: status}, nil
	})
	swapSeam(t, &realSandboxHandleConfigJSON, func(*msbSdk.Sandbox, context.Context) (string, error) {
		return `{"kind":"local"}`, nil
	})

	handle, err := sandboxHandleFrom(context.Background(), &msbSdk.Sandbox{})
	if err != nil {
		t.Fatalf("sandboxHandleFrom() error = %v", err)
	}
	status = msbSdk.SandboxStatusStopped
	if got := handle.Status(); got != SandboxStatusRunning {
		t.Errorf("Status() after the live status changed = %q, want the captured %q", got, SandboxStatusRunning)
	}
}

// stubMetricsStream is a MetricsStream double for the adapter delegation test.
type stubMetricsStream struct{}

func (*stubMetricsStream) Recv(context.Context) (*Metrics, error) {
	return nil, nil //nolint:nilnil // (nil, nil) is the MetricsStream contract's end-of-stream signal
}
func (*stubMetricsStream) Close() error { return nil }

// TestRealSandboxMetricsStreamDelegatesThroughSeam pins that the adapter routes
// the SDK sandbox and the interval through the newMetricsStream seam and returns
// the launcher handle it yields, so the watcher never touches the SDK handle.
func TestRealSandboxMetricsStreamDelegatesThroughSeam(t *testing.T) {
	wanted := &stubMetricsStream{}
	var gotInterval time.Duration
	swapSeam(t, &newMetricsStream, func(
		_ *msbSdk.Sandbox, _ context.Context, interval time.Duration,
	) (MetricsStream, error) {
		gotInterval = interval
		return wanted, nil
	})

	sandbox := realSandbox{sandbox: &msbSdk.Sandbox{}}
	stream, err := sandbox.MetricsStream(context.Background(), 3*time.Second)
	if err != nil {
		t.Fatalf("MetricsStream() error = %v", err)
	}
	if stream != MetricsStream(wanted) {
		t.Errorf("MetricsStream() = %v, want the seam's handle", stream)
	}
	if gotInterval != 3*time.Second {
		t.Errorf("interval = %v, want 3s", gotInterval)
	}
}

// TestRealSandboxHandleMetricsStreamDelegatesThroughSeam pins the same
// delegation for the persisted/unified handle: it shares the resource-control
// role, so a SandboxHandle still satisfies SandboxObserver.
func TestRealSandboxHandleMetricsStreamDelegatesThroughSeam(t *testing.T) {
	wanted := &stubMetricsStream{}
	var gotInterval time.Duration
	swapSeam(t, &newMetricsStream, func(
		_ *msbSdk.Sandbox, _ context.Context, interval time.Duration,
	) (MetricsStream, error) {
		gotInterval = interval
		return wanted, nil
	})

	handle := realSandboxHandle{handle: &msbSdk.Sandbox{}}
	stream, err := handle.MetricsStream(context.Background(), 4*time.Second)
	if err != nil {
		t.Fatalf("MetricsStream() error = %v", err)
	}
	if stream != MetricsStream(wanted) {
		t.Errorf("MetricsStream() = %v, want the seam's handle", stream)
	}
	if gotInterval != 4*time.Second {
		t.Errorf("interval = %v, want 4s", gotInterval)
	}
}

// TestRealMetricsStreamRecvAndClose covers the wrapper's Recv and Close
// delegation through the method-expression seams, so the adapter lines run
// without the native library (the SDK handle has no exported constructor).
func TestRealMetricsStreamRecvAndClose(t *testing.T) {
	ctx := context.Background()
	wantMetrics := &Metrics{MemoryBytes: 1 << 20, MemoryLimitBytes: 2 << 20}
	wantRecvErr := errors.New("recv boom")

	t.Run("Recv delegates", func(t *testing.T) {
		swapSeam(t, &realMetricsStreamRecv, func(
			_ *msbSdk.MetricsStreamHandle, _ context.Context,
		) (*Metrics, error) {
			return wantMetrics, wantRecvErr
		})

		got, err := (realMetricsStream{}).Recv(ctx)
		if got != wantMetrics || !errors.Is(err, wantRecvErr) {
			t.Fatalf("Recv() = (%v, %v); want (%v, %v)", got, err, wantMetrics, wantRecvErr)
		}
	})

	t.Run("Close delegates", func(t *testing.T) {
		called := false
		swapSeam(t, &realMetricsStreamClose, func(*msbSdk.MetricsStreamHandle) error {
			called = true
			return nil
		})

		if err := (realMetricsStream{}).Close(); err != nil || !called {
			t.Fatalf("Close() = %v, called=%v; want nil, true", err, called)
		}
	})
}
