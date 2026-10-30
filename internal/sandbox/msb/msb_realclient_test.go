package msb

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// TestGetFactory exercises the package-level Client factory, which the test
// hook normally overrides.
func TestGetFactory(t *testing.T) {
	c := realGetFactory()
	if _, ok := c.(*realMsbClient); !ok {
		t.Fatalf("Get() = %T, want *realMsbClient", c)
	}
}

// TestRealVolumeHandlePathUnknownValue covers the default branch of
// realVolumeHandle.Path when val holds a non-SDK type.
func TestRealVolumeHandlePathUnknownValue(t *testing.T) {
	v := &realVolumeHandle{val: "not a volume"}
	if got := v.Path(); got != "" {
		t.Errorf("Path() for unknown type = %q, want empty", got)
	}
}

// TestRealMsbClientCreateVolumeError covers the error branch of
// realMsbClient.CreateVolume (invalid empty name fails before touching a VM).
func TestRealMsbClientCreateVolumeError(t *testing.T) {
	c := &realMsbClient{}
	if _, err := c.CreateVolume(context.Background(), ""); err == nil {
		t.Error("CreateVolume() with empty name = nil error, want config error")
	}
}

// TestRealMsbClientImagePruneCanceled covers the adapter without pruning the
// shared microsandbox image cache used by the test environment.
func TestRealMsbClientImagePruneCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&realMsbClient{}).ImagePrune(ctx); err == nil {
		t.Fatal("ImagePrune() with a canceled context = nil error, want cancellation error")
	}
}

// TestRealMsbClientStorageUsageCanceled covers the StorageUsage adapter without
// a runtime: a canceled context fails before the native call.
func TestRealMsbClientStorageUsageCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&realMsbClient{}).StorageUsage(ctx); err == nil {
		t.Fatal("StorageUsage() with a canceled context = nil error, want cancellation error")
	}
}

// TestRealMsbClientImageInspectInjected covers the success path of the
// ImageInspect adapter through the inspect seam.
func TestRealMsbClientImageInspectInjected(t *testing.T) {
	previous := realImageInspect
	t.Cleanup(func() { realImageInspect = previous })
	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return &msbSdk.ImageDetail{Config: &msbSdk.ImageConfig{Digest: "sha256:abc"}}, nil
	}
	cfg, err := (&realMsbClient{}).ImageInspect(context.Background(), "ref")
	if err != nil || cfg == nil || cfg.Digest != "sha256:abc" {
		t.Fatalf("ImageInspect = %v, %v", cfg, err)
	}
}

// TestMockMsbClientGetSandboxInternalBranches covers the getSandboxErr and
// gotSandbox override branches of MockMsbClient.GetSandbox that are not reached
// through the Set* helpers.
func TestMockMsbClientGetSandboxInternalBranches(t *testing.T) {
	ctx := context.Background()

	m := &MockMsbClient{getSandboxErr: errors.New("fail")}
	if _, err := m.GetSandbox(ctx, "x"); err == nil {
		t.Fatal("expected getSandboxErr")
	}

	h := &MockSandboxHandle{Name_: "s"}
	m2 := &MockMsbClient{gotSandbox: h}
	if got, err := m2.GetSandbox(ctx, "x"); err != nil || got != h {
		t.Fatalf("GetSandbox via gotSandbox = %v, %v; want %v", got, err, h)
	}
}

// TestMockMsbClientGetVolumeInternalBranches covers the GetVolumeErr and
// gotVolume override branches of MockMsbClient.GetVolume not reached through
// the Set* helpers.
func TestMockMsbClientGetVolumeInternalBranches(t *testing.T) {
	ctx := context.Background()

	m := &MockMsbClient{GetVolumeErr: errors.New("fail")}
	if _, err := m.GetVolume(ctx, "x"); err == nil {
		t.Fatal("expected GetVolumeErr")
	}

	v := &MockVolumeHandle{Name_: "vol"}
	m2 := &MockMsbClient{gotVolume: v}
	if got, err := m2.GetVolume(ctx, "x"); err != nil || got != v {
		t.Fatalf("GetVolume via gotVolume = %v, %v; want %v", got, err, v)
	}
}

// TestSetGetSandboxErrNil covers the else branch of SetGetSandboxErr clearing
// the override.
func TestSetGetSandboxErrNil(t *testing.T) {
	m := &MockMsbClient{}
	if got := m.SetGetSandboxErr(nil); got != m {
		t.Fatal("SetGetSandboxErr(nil) should return the receiver")
	}
	if m.GetSandboxFn != nil {
		t.Fatal("SetGetSandboxErr(nil) should clear GetSandboxFn")
	}
}

// TestMockSandboxHandleImageDefault covers the default branch of
// MockSandboxHandle.Image when no Image_ or Cfg is set.
func TestMockSandboxHandleImageDefault(t *testing.T) {
	h := &MockSandboxHandle{}
	if got := h.Image(); got != "" {
		t.Errorf("Image() = %q, want empty", got)
	}
}

// TestMockSandboxShellStreamFn covers the ShellStreamFn branch of
// MockSandbox.ShellStream.
func TestMockSandboxShellStreamFn(t *testing.T) {
	stub := &streamHandleStub{}
	m := &MockSandbox{
		ShellStreamFn: func(command string) (StreamHandle, error) {
			if command != "cmd" {
				t.Errorf("ShellStreamFn command = %q, want %q", command, "cmd")
			}
			return stub, nil
		},
	}
	got, err := m.ShellStream(context.Background(), "cmd")
	if err != nil {
		t.Fatalf("ShellStream() error = %v", err)
	}
	if got != stub {
		t.Fatal("ShellStream() did not return the ShellStreamFn handle")
	}
}

// TestEmptyStreamHandleClose covers emptyStreamHandle.Close.
func TestEmptyStreamHandleClose(t *testing.T) {
	var h StreamHandle = &emptyStreamHandle{}
	if err := h.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

// TestFailFastShellStreamPanics covers the otherwise-unreachable ShellStream
// method on the fail-fast client.
func TestFailFastShellStreamPanics(t *testing.T) {
	f := &failFastMsbClient{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic from fail-fast ShellStream")
		}
	}()
	_, _ = f.ShellStream(context.Background(), "cmd")
}

// TestFailFastImagePrunePanics covers the fail-fast ImagePrune method.
func TestFailFastImagePrunePanics(t *testing.T) {
	f := &failFastMsbClient{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic from fail-fast ImagePrune")
		}
	}()
	_, _ = f.ImagePrune(context.Background())
}

// TestRealVolumeHandlePathNilVal guards against a nil val.
func TestRealVolumeHandlePathNilVal(t *testing.T) {
	v := &realVolumeHandle{}
	if got := v.Path(); got != "" {
		t.Errorf("Path() with nil val = %q, want empty", got)
	}
}

// TestRealMsbClientImageInspectDetailInjected covers the success path of the
// ImageInspectDetail adapter through the inspect seam, mapping the SDK detail
// onto the local ImageDetail (including a nil config).
func TestRealMsbClientImageInspectDetailInjected(t *testing.T) {
	previous := realImageInspect
	t.Cleanup(func() { realImageInspect = previous })
	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return &msbSdk.ImageDetail{
			Config: &msbSdk.ImageConfig{Digest: "sha256:abc"},
			Layers: []msbSdk.ImageLayer{{Position: 0, DiffID: "sha256:layer0"}},
		}, nil
	}
	detail, err := (&realMsbClient{}).ImageInspectDetail(context.Background(), "ref")
	if err != nil {
		t.Fatalf("ImageInspectDetail() error = %v", err)
	}
	if detail.Config.Digest != "sha256:abc" || len(detail.Layers) != 1 ||
		detail.Layers[0].DiffID != "sha256:layer0" {
		t.Fatalf("ImageInspectDetail() = %+v, want digest sha256:abc and layer sha256:layer0", detail)
	}
}

// TestRealMsbClientImageInspectDetailNilConfig covers the nil-config branch of
// the adapter, which leaves the zero-value config in place.
func TestRealMsbClientImageInspectDetailNilConfig(t *testing.T) {
	previous := realImageInspect
	t.Cleanup(func() { realImageInspect = previous })
	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return &msbSdk.ImageDetail{}, nil
	}
	detail, err := (&realMsbClient{}).ImageInspectDetail(context.Background(), "ref")
	if err != nil || detail == nil {
		t.Fatalf("ImageInspectDetail() = %v, %v", detail, err)
	}
	if detail.Config.Digest != "" {
		t.Errorf("ImageInspectDetail() config = %+v, want zero value", detail.Config)
	}
}

// TestMockImageInspectDetail covers the mock's callback and default branches.
func TestMockImageInspectDetail(t *testing.T) {
	m := &MockMsbClient{ImageInspectDetailFn: func(_ context.Context, _ string) (*ImageDetail, error) {
		return &ImageDetail{Reference: "agents-sandbox/runner-x", ManifestDigest: "sha256:x"}, nil
	}}
	detail, err := m.ImageInspectDetail(context.Background(), "ref")
	if err != nil || detail.Reference != "agents-sandbox/runner-x" {
		t.Fatalf("ImageInspectDetail via fn = %+v, %v", detail, err)
	}

	got, err := (&MockMsbClient{}).ImageInspectDetail(context.Background(), "ref")
	if err != nil || got.Reference != "ref" {
		t.Fatalf("ImageInspectDetail default = %+v, %v", got, err)
	}
}

// TestRealMsbClientImageInspectDetailHandleAndError covers the ImageHandle
// branch (metadata copied off the handle) and the inspect-error branch of the
// ImageInspectDetail adapter.
func TestRealMsbClientImageInspectDetailHandleAndError(t *testing.T) {
	previous := realImageInspect
	t.Cleanup(func() { realImageInspect = previous })

	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return &msbSdk.ImageDetail{
			ImageHandle: &msbSdk.ImageHandle{},
			Config:      &msbSdk.ImageConfig{Digest: "sha256:abc"},
		}, nil
	}
	detail, err := (&realMsbClient{}).ImageInspectDetail(context.Background(), "ref")
	if err != nil || detail == nil {
		t.Fatalf("ImageInspectDetail() = %v, %v", detail, err)
	}
	if detail.Reference != "" || detail.ManifestDigest != "" || detail.Architecture != "" ||
		detail.OS != "" || detail.SizeBytes != nil || !detail.CreatedAt.IsZero() {
		t.Errorf("ImageInspectDetail() from empty handle = %+v, want zero metadata", detail)
	}
	if detail.Config.Digest != "sha256:abc" {
		t.Errorf("ImageInspectDetail() config = %+v, want digest sha256:abc", detail.Config)
	}

	realImageInspect = func(_ context.Context, _ string) (*msbSdk.ImageDetail, error) {
		return nil, errors.New("inspect boom")
	}
	if _, err := (&realMsbClient{}).ImageInspectDetail(context.Background(), "ref"); err == nil {
		t.Fatal("ImageInspectDetail() with a failing inspect = nil error, want error")
	}
}

// TestFailFastImageInspectDetailPanics covers the fail-fast ImageInspectDetail.
func TestFailFastImageInspectDetailPanics(t *testing.T) {
	f := &failFastMsbClient{}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic from fail-fast ImageInspectDetail")
		}
	}()
	_, _ = f.ImageInspectDetail(context.Background(), "ref")
}

// TestMockSandboxHandleID covers the mock handle's stable-identity accessor.
func TestMockSandboxHandleID(t *testing.T) {
	h := &MockSandboxHandle{ID_: "sbx-123"}
	if got := h.ID(); got != "sbx-123" {
		t.Errorf("ID() = %q, want %q", got, "sbx-123")
	}
}
