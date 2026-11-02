package msb

import (
	"context"
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestMockSandboxDestroy(t *testing.T) {
	sb := &MockSandbox{}
	if err := sb.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v, want nil", err)
	}
	if !sb.DidDestroy() {
		t.Error("expected DidDestroy() true after a successful destroy")
	}

	failing := &MockSandbox{DestroyErr: errors.New("boom")}
	if err := failing.Destroy(context.Background()); err == nil {
		t.Fatal("expected DestroyErr from Destroy()")
	}
	if failing.DidDestroy() {
		t.Error("expected DidDestroy() false after a failed destroy")
	}
}

func TestMockSandboxHandleCreatedAtBackendKind(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC)
	h := &MockSandboxHandle{CreatedAt_: now, BackendKind_: msbSdk.BackendLocal}
	if got := h.CreatedAt(); !got.Equal(now) {
		t.Errorf("CreatedAt() = %v, want %v", got, now)
	}
	if got := h.BackendKind(); got != msbSdk.BackendLocal {
		t.Errorf("BackendKind() = %v, want %v", got, msbSdk.BackendLocal)
	}
}

func TestMockSandboxHandleImageFallsBackToConfig(t *testing.T) {
	viaConfig := &MockSandboxHandle{Cfg: &msbSdk.SandboxConfig{Image: "agents-sandbox/runner:latest"}}
	if got := viaConfig.Image(); got != "agents-sandbox/runner:latest" {
		t.Errorf("Image() via Cfg = %q, want image from config", got)
	}
	explicit := &MockSandboxHandle{Image_: "img:direct"}
	if got := explicit.Image(); got != "img:direct" {
		t.Errorf("Image() explicit = %q, want img:direct", got)
	}
}

// TestMockSandboxDetach pins both detach modes: DetachErr alone, and DetachFn
// observing the context the detach runs under.
func TestMockSandboxDetach(t *testing.T) {
	detachErr := errors.New("detach failed")
	if err := (&MockSandbox{DetachErr: detachErr}).Detach(context.Background()); !errors.Is(err, detachErr) {
		t.Errorf("Detach() = %v, want %v", err, detachErr)
	}

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "caller")
	var seenCallerValue any
	sb := &MockSandbox{DetachErr: detachErr, DetachFn: func(ctx context.Context) error {
		seenCallerValue = ctx.Value(contextKey{})
		return nil
	}}
	if err := sb.Detach(ctx); err != nil {
		t.Errorf("Detach() with DetachFn = %v, want DetachFn's nil", err)
	}
	if seenCallerValue != "caller" {
		t.Error("DetachFn did not receive the caller's context")
	}
}
