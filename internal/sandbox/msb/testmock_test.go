package msb

import (
	"context"
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestMockStorageUsageReturnsReport(t *testing.T) {
	want := &msbSdk.StorageUsageReport{}
	m := &MockMsbClient{StorageUsageFn: func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return want, nil
	}}
	got, err := m.StorageUsage(context.Background())
	if err != nil {
		t.Fatalf("StorageUsage() error = %v", err)
	}
	if got != want {
		t.Fatal("StorageUsage() did not return the callback report")
	}
}

func TestMockStorageUsageDefaultsAndError(t *testing.T) {
	m := &MockMsbClient{}
	got, err := m.StorageUsage(context.Background())
	if err != nil {
		t.Fatalf("StorageUsage() default error = %v", err)
	}
	if got == nil {
		t.Fatal("StorageUsage() default report = nil, want empty report")
	}

	m = (&MockMsbClient{}).SetStorageUsageErr(errors.New("boom"))
	if _, err := m.StorageUsage(context.Background()); err == nil {
		t.Fatal("StorageUsage() with SetStorageUsageErr = nil error, want boom")
	}
}

func TestFailFastStorageUsagePanics(t *testing.T) {
	f := &failFastMsbClient{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic from fail-fast StorageUsage")
		}
	}()
	_, _ = f.StorageUsage(context.Background())
}

func TestMockImageHandleNewAccessors(t *testing.T) {
	bytes := int64(123456)
	h := MockImageHandle{
		Reference_:      "agents-sandbox/runner:latest",
		ManifestDigest_: "sha256:abc",
		SizeBytes_:      &bytes,
		CreatedAt_:      time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
	}
	if got := h.SizeBytes(); got == nil || *got != 123456 {
		t.Fatalf("SizeBytes() = %v, want 123456", got)
	}
	if want := "2026-08-17 10:42:36"; h.CreatedAt().Format("2006-01-02 15:04:05") != want {
		t.Fatalf("CreatedAt() = %v, want %s", h.CreatedAt(), want)
	}
	if h.Reference() != "agents-sandbox/runner:latest" || h.ManifestDigest() != "sha256:abc" {
		t.Fatalf("existing accessors broken: %s %s", h.Reference(), h.ManifestDigest())
	}
}

func TestMockSandboxHandleDestroyFn(t *testing.T) {
	called := false
	wantErr := errors.New("destroy boom")
	h := &MockSandboxHandle{DestroyFn: func(context.Context) error {
		called = true
		return wantErr
	}}
	if err := h.Destroy(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Destroy() error = %v, want %v", err, wantErr)
	}
	if !called {
		t.Fatal("Destroy() did not call DestroyFn")
	}
	if h.DidDestroy() {
		t.Error("Destroy() with a DestroyFn must not mark the handle destroyed")
	}
}

func TestMockSandboxHandleConfig(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "agents-sandbox/runner-proj:opencode-latest"}
	h := &MockSandboxHandle{Cfg: cfg}
	got, err := h.Config()
	if err != nil || got != cfg {
		t.Fatalf("Config() = (%v, %v), want the configured config", got, err)
	}

	broken := &MockSandboxHandle{ConfigErr: errors.New("sandbox gone")}
	if _, err := broken.Config(); !errors.Is(err, broken.ConfigErr) {
		t.Fatalf("Config() error = %v, want the configured error", err)
	}
}
