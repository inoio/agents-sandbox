package msb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestMockVolumeHandleImplementsInterface(_ *testing.T) {
	var _ VolumeHandle = (*MockVolumeHandle)(nil)
	v := &MockVolumeHandle{}
	_ = v.IsDefault()
	_ = v.QuotaMiB()
	_ = v.UsedBytes()
	_ = v.CapacityBytes()
	_ = v.DiskFormat()
	_ = v.DiskFstype()
	_ = v.Labels()
}

func TestRealVolumeHandle_Kind_UnknownValue(t *testing.T) {
	vh := &realVolumeHandle{val: "not a volume"}
	if got := vh.Kind(); got != msbSdk.VolumeKindDir {
		t.Errorf("Kind() for unknown type = %v, want %v", got, msbSdk.VolumeKindDir)
	}
	if got := vh.Name(); got != "" {
		t.Errorf("Name() for unknown type = %q, want %q", got, "")
	}
}

func TestRealSandboxHandleConfigAndModify(t *testing.T) {
	var h SandboxHandle = &realSandboxHandle{handle: &msbSdk.SandboxHandle{}}
	cfg, err := h.Config()
	if err == nil || cfg != nil {
		t.Errorf("expected Config error on handle without config, got cfg=%v err=%v", cfg, err)
	}
}

func TestRealVolumeHandleFromHandle(t *testing.T) {
	v := &realVolumeHandle{val: &msbSdk.VolumeHandle{}}
	if got := v.Name(); got != "" {
		t.Errorf("Name() = %q, want empty", got)
	}
	if got := v.Path(); got != "" {
		t.Errorf("Path() = %q, want empty", got)
	}
	if got := v.Kind(); got != msbSdk.VolumeKind("") {
		t.Errorf("Kind() = %q, want empty", got)
	}
	if !v.CreatedAt().Equal(time.Time{}) {
		t.Errorf("CreatedAt() = %v, want zero time", v.CreatedAt())
	}
	if v.IsDefault() {
		t.Error("IsDefault() = true, want false")
	}
	if v.QuotaMiB() != nil {
		t.Errorf("QuotaMiB() = %v, want nil", v.QuotaMiB())
	}
	if v.UsedBytes() != 0 {
		t.Errorf("UsedBytes() = %d, want 0", v.UsedBytes())
	}
	if v.CapacityBytes() != nil {
		t.Errorf("CapacityBytes() = %v, want nil", v.CapacityBytes())
	}
	if v.DiskFormat() != nil {
		t.Errorf("DiskFormat() = %v, want nil", v.DiskFormat())
	}
	if v.DiskFstype() != nil {
		t.Errorf("DiskFstype() = %v, want nil", v.DiskFstype())
	}
	if v.Labels() != nil {
		t.Errorf("Labels() = %v, want nil", v.Labels())
	}
}

func TestRealVolumeHandleNameAndPathFromVolume(t *testing.T) {
	v := &realVolumeHandle{val: &msbSdk.Volume{}}
	if got := v.Name(); got != "" {
		t.Errorf("Name() from *Volume = %q, want empty", got)
	}
	if got := v.Path(); got != "" {
		t.Errorf("Path() from *Volume = %q, want empty", got)
	}
}

func TestRealVolumeHandleFromVolumeKind(t *testing.T) {
	v := &realVolumeHandle{val: &msbSdk.Volume{}}
	if got := v.Kind(); got != msbSdk.VolumeKindDir {
		t.Errorf("Kind() from *Volume = %v, want VolumeKindDir", got)
	}
	if got := v.CreatedAt(); !got.Equal(time.Time{}) {
		t.Errorf("CreatedAt() from *Volume = %v, want zero time", got)
	}
	if v.IsDefault() {
		t.Error("IsDefault() from *Volume = true, want false")
	}
	if v.QuotaMiB() != nil {
		t.Errorf("QuotaMiB() from *Volume = %v, want nil", v.QuotaMiB())
	}
	if v.UsedBytes() != 0 {
		t.Errorf("UsedBytes() from *Volume = %d, want 0", v.UsedBytes())
	}
	if v.CapacityBytes() != nil {
		t.Errorf("CapacityBytes() from *Volume = %v, want nil", v.CapacityBytes())
	}
	if v.DiskFormat() != nil {
		t.Errorf("DiskFormat() from *Volume = %v, want nil", v.DiskFormat())
	}
	if v.DiskFstype() != nil {
		t.Errorf("DiskFstype() from *Volume = %v, want nil", v.DiskFstype())
	}
	if v.Labels() != nil {
		t.Errorf("Labels() from *Volume = %v, want nil", v.Labels())
	}
}

func TestRealSandboxHandleAccessors(t *testing.T) {
	h := &realSandboxHandle{handle: &msbSdk.SandboxHandle{}}
	if got := h.Name(); got != "" {
		t.Errorf("Name() = %q, want empty", got)
	}
	if got := h.ID(); got != "" {
		t.Errorf("ID() = %q, want empty", got)
	}
	if got := h.Status(); got != msbSdk.SandboxStatus("") {
		t.Errorf("Status() = %q, want empty", got)
	}
	if !h.UpdatedAt().Equal(time.Time{}) {
		t.Errorf("UpdatedAt() = %v, want zero time", h.UpdatedAt())
	}
	if !h.CreatedAt().Equal(time.Time{}) {
		t.Errorf("CreatedAt() = %v, want zero time", h.CreatedAt())
	}
	if got := h.BackendKind(); got != msbSdk.BackendKind("") {
		t.Errorf("BackendKind() = %q, want empty", got)
	}
	if got := h.Image(); got != "" {
		t.Errorf("Image() = %q, want empty (config parse fails)", got)
	}
}

func TestRealSandboxHandleOperationsReturnErrors(t *testing.T) {
	h := &realSandboxHandle{handle: &msbSdk.SandboxHandle{}}
	ctx := context.Background()

	if sb, err := h.Connect(ctx); err == nil || sb != nil {
		t.Errorf("Connect() = %v, %v; want error on empty handle", sb, err)
	}
	if hh, err := h.Refresh(ctx); err == nil || hh != nil {
		t.Errorf("Refresh() = %v, %v; want error on empty handle", hh, err)
	}
	if sb, err := h.Start(ctx); err == nil || sb != nil {
		t.Errorf("Start() = %v, %v; want error on empty handle", sb, err)
	}
	if err := h.Stop(ctx); err == nil {
		t.Error("Stop() = nil, want error on empty handle")
	}
	if err := h.Kill(ctx); err == nil {
		t.Error("Kill() = nil, want error on empty handle")
	}
	if err := h.Remove(ctx); err == nil {
		t.Error("Remove() = nil, want error on empty handle")
	}
	if err := h.Destroy(ctx); err == nil {
		t.Error("Destroy() = nil, want error on empty handle")
	}
	if result, err := h.Compact(ctx, msbSdk.DiskCompactionOptions{}); !IsUnsupportedOperation(err) || result != nil {
		t.Errorf("Compact() = %v, %v; want unsupported-operation error on non-local handle", result, err)
	}
	if plan, err := h.Modify(ctx, msbSdk.ModifyOptions{}); err == nil || plan != nil {
		t.Errorf("Modify() = %v, %v; want error on empty handle", plan, err)
	}
}

// swapSeam replaces a package-level SDK seam for the duration of the test and
// restores the real call on cleanup, so unit tests exercise the adapters
// against fakes without loading the native library or reading real runtime
// state. The real adapter-over-SDK behavior lives in the msb-tagged tests.
func swapSeam[T any](t *testing.T, seam *T, replacement T) {
	t.Helper()
	original := *seam
	*seam = replacement
	t.Cleanup(func() { *seam = original })
}

// TestRealMsbClientAdapterErrors pins that every adapter propagates the SDK
// error through its seam.
func TestRealMsbClientAdapterErrors(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("sdk failed")

	swapSeam(t, &getSandbox, func(context.Context, string) (*msbSdk.SandboxHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &createSandbox, func(context.Context, string, ...msbSdk.SandboxOption) (*msbSdk.Sandbox, error) {
		return nil, wantErr
	})
	swapSeam(t, &listSandboxesWith, func(context.Context, ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error) {
		return nil, wantErr
	})
	swapSeam(t, &getVolume, func(context.Context, string) (*msbSdk.VolumeHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &createVolume, func(context.Context, string, ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		return nil, wantErr
	})
	swapSeam(t, &listVolumes, func(context.Context) ([]*msbSdk.VolumeHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &removeVolume, func(context.Context, string) error { return wantErr })
	swapSeam(t, &imageGet, func(context.Context, string) (*msbSdk.ImageHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &imageList, func(context.Context) ([]*msbSdk.ImageHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &imageRemove, func(context.Context, string, bool) error { return wantErr })
	swapSeam(t, &imageLoad, func(context.Context, string, ...string) ([]*msbSdk.ImageHandle, error) {
		return nil, wantErr
	})
	swapSeam(t, &realImageInspect, func(context.Context, string) (*msbSdk.ImageDetail, error) {
		return nil, wantErr
	})
	swapSeam(t, &imagePrune, func(context.Context) (*msbSdk.ImagePruneReport, error) {
		return nil, wantErr
	})
	swapSeam(t, &storageUsage, func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return nil, wantErr
	})
	swapSeam(t, &storagePrune, func(context.Context, msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error) {
		return nil, wantErr
	})

	c := &realMsbClient{}
	cases := []struct {
		name string
		call func() error
	}{
		{"GetSandbox", func() error { _, err := c.GetSandbox(ctx, "x"); return err }},
		{"CreateSandbox", func() error { _, err := c.CreateSandbox(ctx, "x"); return err }},
		{"ListSandboxes", func() error { _, err := c.ListSandboxes(ctx, nil); return err }},
		{"GetVolume", func() error { _, err := c.GetVolume(ctx, "x"); return err }},
		{"CreateVolume", func() error { _, err := c.CreateVolume(ctx, "x"); return err }},
		{"ListVolumes", func() error { _, err := c.ListVolumes(ctx); return err }},
		{"RemoveVolume", func() error { return c.RemoveVolume(ctx, "x") }},
		{"ImageGet", func() error { return c.ImageGet(ctx, "x") }},
		{"ImageList", func() error { _, err := c.ImageList(ctx); return err }},
		{"ImageRemove", func() error { return c.ImageRemove(ctx, "x", false) }},
		{"ImageLoad", func() error { return c.ImageLoad(ctx, "x", strings.NewReader("")) }},
		{"ImageInspect", func() error { _, err := c.ImageInspect(ctx, "x"); return err }},
		{"ImagePrune", func() error { _, err := c.ImagePrune(ctx); return err }},
		{"StorageUsage", func() error { _, err := c.StorageUsage(ctx); return err }},
		{"PruneStorage", func() error { _, err := c.PruneStorage(ctx, msbSdk.StoragePruneOptions{}); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, wantErr) {
				t.Fatalf("%s error = %v, want the seam error", tc.name, err)
			}
		})
	}
}

// TestRealMsbClientLists pins the slice mapping of the three list adapters.
func TestRealMsbClientLists(t *testing.T) {
	ctx := context.Background()
	swapSeam(t, &listSandboxesWith, func(context.Context, ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error) {
		return &msbSdk.SandboxPage{Sandboxes: []*msbSdk.SandboxHandle{{}}}, nil
	})
	swapSeam(t, &listVolumes, func(context.Context) ([]*msbSdk.VolumeHandle, error) {
		return []*msbSdk.VolumeHandle{{}}, nil
	})
	swapSeam(t, &imageList, func(context.Context) ([]*msbSdk.ImageHandle, error) {
		return []*msbSdk.ImageHandle{{}}, nil
	})

	c := &realMsbClient{}
	if handles, err := c.ListSandboxes(ctx, nil); err != nil || len(handles) != 1 {
		t.Errorf("ListSandboxes() = %d handles, %v; want 1, nil", len(handles), err)
	}
	if handles, err := c.ListVolumes(ctx); err != nil || len(handles) != 1 {
		t.Errorf("ListVolumes() = %d handles, %v; want 1, nil", len(handles), err)
	}
	if handles, err := c.ImageList(ctx); err != nil || len(handles) != 1 {
		t.Errorf("ImageList() = %d handles, %v; want 1, nil", len(handles), err)
	}
}

// TestRealMsbClientListSandboxesPagination drives the paging loop and the label
// and cursor option branches. The first page carries the label option; the
// second, reached through NextCursor, adds the cursor option.
func TestRealMsbClientListSandboxesPagination(t *testing.T) {
	ctx := context.Background()
	next := "page-2"
	pages := []*msbSdk.SandboxPage{
		{Sandboxes: []*msbSdk.SandboxHandle{{}}, NextCursor: &next},
		{Sandboxes: []*msbSdk.SandboxHandle{{}}, NextCursor: nil},
	}
	var optionCounts []int
	swapSeam(
		t,
		&listSandboxesWith,
		func(_ context.Context, opts ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error) {
			optionCounts = append(optionCounts, len(opts))
			return pages[len(optionCounts)-1], nil
		},
	)

	handles, err := (&realMsbClient{}).ListSandboxes(ctx, map[string]string{"project": "demo"})
	if err != nil || len(handles) != 2 {
		t.Fatalf("ListSandboxes() = %d handles, %v; want 2 over two pages", len(handles), err)
	}
	if len(optionCounts) != 2 || optionCounts[0] != 1 || optionCounts[1] != 2 {
		t.Errorf("ListSandboxesWith option counts = %v, want [1 2]", optionCounts)
	}
}

// TestRealMsbClientEnsureInstalled covers EnsureInstalled's error path through
// the installer seam. The real SDK installer is deliberately not exercised: it
// downloads a runtime over the network, which makes a unit test slow and flaky,
// and the success path (including the private home it must name) is covered by
// TestRealClientEnsureInstalledPassesPrivateHome.
func TestRealMsbClientEnsureInstalled(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	previous := skipRuntimeInstall
	skipRuntimeInstall = false
	t.Cleanup(func() { skipRuntimeInstall = previous })

	installErr := errors.New("installer unavailable")
	orig := ensureRuntime
	ensureRuntime = func(
		context.Context, msbSdk.RuntimeConfig, msbSdk.InstallOptions,
	) (msbSdk.ResolvedRuntime, error) {
		return msbSdk.ResolvedRuntime{}, installErr
	}
	t.Cleanup(func() { ensureRuntime = orig })

	if err := (&realMsbClient{}).EnsureInstalled(context.Background()); !errors.Is(err, installErr) {
		t.Errorf("EnsureInstalled() error = %v, want %v", err, installErr)
	}
}

func TestRealMsbClientCreateRemoveVolume(t *testing.T) {
	ctx := context.Background()
	var createdName, removedName string
	swapSeam(t, &createVolume, func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		createdName = name
		return &msbSdk.Volume{}, nil
	})
	swapSeam(t, &removeVolume, func(_ context.Context, name string) error {
		removedName = name
		return nil
	})

	c := &realMsbClient{}
	if _, err := c.CreateVolume(ctx, "vol"); err != nil {
		t.Fatalf("CreateVolume() error = %v, want nil", err)
	}
	if err := c.RemoveVolume(ctx, "vol"); err != nil {
		t.Errorf("RemoveVolume() error = %v, want nil", err)
	}
	if createdName != "vol" || removedName != "vol" {
		t.Errorf("create=%q remove=%q, want both %q", createdName, removedName, "vol")
	}
}

// TestRealMsbClientSandboxAdapters covers the GetSandbox and CreateSandbox
// success paths, wrapping the seam's SDK handle in the adapter.
func TestRealMsbClientSandboxAdapters(t *testing.T) {
	ctx := context.Background()
	swapSeam(t, &getSandbox, func(context.Context, string) (*msbSdk.SandboxHandle, error) {
		return &msbSdk.SandboxHandle{}, nil
	})
	swapSeam(t, &createSandbox, func(context.Context, string, ...msbSdk.SandboxOption) (*msbSdk.Sandbox, error) {
		return &msbSdk.Sandbox{}, nil
	})

	c := &realMsbClient{}
	if handle, err := c.GetSandbox(ctx, "s"); err != nil || handle == nil {
		t.Errorf("GetSandbox() = %v, %v; want a handle, nil", handle, err)
	}
	if sandbox, err := c.CreateSandbox(ctx, "s"); err != nil || sandbox == nil {
		t.Errorf("CreateSandbox() = %v, %v; want a sandbox, nil", sandbox, err)
	}
}

// TestRealMsbClientGetVolume covers the GetVolume success path.
func TestRealMsbClientGetVolume(t *testing.T) {
	swapSeam(t, &getVolume, func(context.Context, string) (*msbSdk.VolumeHandle, error) {
		return &msbSdk.VolumeHandle{}, nil
	})
	if handle, err := (&realMsbClient{}).GetVolume(context.Background(), "v"); err != nil || handle == nil {
		t.Errorf("GetVolume() = %v, %v; want a handle, nil", handle, err)
	}
}

// TestRealMsbClientImageAdapters covers the single-object image adapter success
// paths (ImageList is covered by TestRealMsbClientLists).
func TestRealMsbClientImageAdapters(t *testing.T) {
	ctx := context.Background()
	swapSeam(t, &imageGet, func(context.Context, string) (*msbSdk.ImageHandle, error) {
		return &msbSdk.ImageHandle{}, nil
	})
	swapSeam(t, &imageRemove, func(context.Context, string, bool) error { return nil })
	swapSeam(t, &imageLoad, func(context.Context, string, ...string) ([]*msbSdk.ImageHandle, error) {
		return []*msbSdk.ImageHandle{{}}, nil
	})
	swapSeam(t, &imagePrune, func(context.Context) (*msbSdk.ImagePruneReport, error) {
		return &msbSdk.ImagePruneReport{ManifestsRemoved: 2}, nil
	})
	swapSeam(t, &storageUsage, func(context.Context) (*msbSdk.StorageUsageReport, error) {
		return &msbSdk.StorageUsageReport{}, nil
	})

	c := &realMsbClient{}
	if err := c.ImageGet(ctx, "ref"); err != nil {
		t.Errorf("ImageGet() = %v, want nil", err)
	}
	if err := c.ImageRemove(ctx, "ref", true); err != nil {
		t.Errorf("ImageRemove() = %v, want nil", err)
	}
	if err := c.ImageLoad(ctx, "ref", strings.NewReader("")); err != nil {
		t.Errorf("ImageLoad() = %v, want nil", err)
	}
	if report, err := c.ImagePrune(ctx); err != nil || report.ManifestsRemoved != 2 {
		t.Errorf("ImagePrune() = %v, %v; want 2 manifests removed", report, err)
	}
	if report, err := c.StorageUsage(ctx); err != nil || report == nil {
		t.Errorf("StorageUsage() = %v, %v; want a report, nil", report, err)
	}
}

// TestRealMsbClientImageLoadErrors covers the spooling failure before the SDK
// seam is reached; the SDK load failure is covered by
// TestRealMsbClientAdapterErrors.
func TestRealMsbClientImageLoadErrors(t *testing.T) {
	if err := (&realMsbClient{}).ImageLoad(context.Background(), "ref", errReader{}); err == nil {
		t.Error("ImageLoad() with failing reader = nil error, want spooling error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestIsSandboxActive(t *testing.T) {
	tests := []struct {
		name   string
		status msbSdk.SandboxStatus
		want   bool
	}{
		{"running", msbSdk.SandboxStatusRunning, true},
		{"draining", msbSdk.SandboxStatusDraining, true},
		{"paused", msbSdk.SandboxStatusPaused, true},
		{"starting", msbSdk.SandboxStatusStarting, true},
		{"created", msbSdk.SandboxStatusCreated, false},
		{"stopped", msbSdk.SandboxStatusStopped, false},
		{"crashed", msbSdk.SandboxStatusCrashed, false},
		{"unknown", msbSdk.SandboxStatus(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSandboxActive(tt.status); got != tt.want {
				t.Errorf("IsSandboxActive(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestMockListSandboxesRecordsLabels(t *testing.T) {
	m := &MockMsbClient{}
	m.ListSandboxesFn = func(_ context.Context, labels map[string]string) ([]SandboxHandle, error) {
		if labels["k"] != "v" {
			t.Errorf("expected label k=v, got %v", labels)
		}
		return nil, nil
	}
	_, err := m.ListSandboxes(context.Background(), map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMockImageInspectUsesFn(t *testing.T) {
	called := false
	m := &MockMsbClient{ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
		called = true
		return &msbSdk.ImageConfig{}, nil
	}}
	cfg, err := m.ImageInspect(context.Background(), "ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called || cfg == nil {
		t.Error("expected ImageInspectFn to be called and return a config")
	}
}

func TestIsNotFound(t *testing.T) {
	notFound := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no such sandbox"}
	other := &msbSdk.Error{Kind: msbSdk.ErrSandboxAlreadyExists, Message: "exists"}
	plain := errors.New("some other failure")

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not found kind", notFound, true},
		{"wrapped not found", fmt.Errorf("outer: %w", notFound), true},
		{"other kind", other, false},
		{"plain error", plain, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotFound(tt.err); got != tt.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsVolumeNotFound(t *testing.T) {
	volumeNotFound := &msbSdk.Error{Kind: msbSdk.ErrVolumeNotFound, Message: "no such volume"}
	sandboxNotFound := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no such sandbox"}
	plain := errors.New("some other failure")

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"volume not found kind", volumeNotFound, true},
		{"wrapped volume not found", fmt.Errorf("outer: %w", volumeNotFound), true},
		{"sandbox not found kind", sandboxNotFound, false},
		{"plain error", plain, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsVolumeNotFound(tt.err); got != tt.want {
				t.Errorf("IsVolumeNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsVolumeAlreadyExists(t *testing.T) {
	volumeExists := &msbSdk.Error{Kind: msbSdk.ErrVolumeAlreadyExists, Message: "volume exists"}
	sandboxExists := &msbSdk.Error{Kind: msbSdk.ErrSandboxAlreadyExists, Message: "sandbox exists"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"volume already exists kind", volumeExists, true},
		{"wrapped volume already exists", fmt.Errorf("outer: %w", volumeExists), true},
		{"sandbox already exists kind", sandboxExists, false},
		{"plain error", errors.New("some other failure"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsVolumeAlreadyExists(tt.err); got != tt.want {
				t.Errorf("IsVolumeAlreadyExists(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsReplaced(t *testing.T) {
	replaced := &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "handle was replaced"}
	other := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no such sandbox"}
	plain := errors.New("some other failure")

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"replaced kind", replaced, true},
		{"wrapped replaced", fmt.Errorf("outer: %w", replaced), true},
		{"other kind", other, false},
		{"plain error", plain, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReplaced(tt.err); got != tt.want {
				t.Errorf("IsReplaced(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRealSandboxDelegatesToSDK(t *testing.T) {
	ctx := context.Background()

	t.Run("Attach", func(t *testing.T) {
		original := realSandboxAttach
		t.Cleanup(func() { realSandboxAttach = original })
		called := false
		realSandboxAttach = func(_ *msbSdk.Sandbox, _ context.Context, _ string, _ ...string) (int, error) {
			called = true
			return 3, nil
		}
		code, err := (realSandbox{}).Attach(ctx, "cmd")
		if err != nil || code != 3 || !called {
			t.Fatalf("Attach() = (%d, %v), called=%v; want (3, nil, true)", code, err, called)
		}
	})

	t.Run("AttachWith", func(t *testing.T) {
		original := realSandboxAttachWith
		t.Cleanup(func() { realSandboxAttachWith = original })
		called := false
		realSandboxAttachWith = func(
			_ *msbSdk.Sandbox, _ context.Context, _ string, _ []string, _ ...msbSdk.AttachOption,
		) (int, error) {
			called = true
			return 4, nil
		}
		code, err := (realSandbox{}).AttachWith(ctx, "cmd", []string{"a"})
		if err != nil || code != 4 || !called {
			t.Fatalf("AttachWith() = (%d, %v), called=%v; want (4, nil, true)", code, err, called)
		}
	})

	t.Run("Detach", func(t *testing.T) {
		original := realSandboxDetach
		t.Cleanup(func() { realSandboxDetach = original })
		called := false
		realSandboxDetach = func(_ *msbSdk.Sandbox, _ context.Context) error {
			called = true
			return nil
		}
		if err := (realSandbox{}).Detach(ctx); err != nil || !called {
			t.Fatalf("Detach() = %v, called=%v; want nil, true", err, called)
		}
	})

	t.Run("Stop", func(t *testing.T) {
		original := realSandboxStop
		t.Cleanup(func() { realSandboxStop = original })
		called := false
		realSandboxStop = func(_ *msbSdk.Sandbox, _ context.Context, _ ...msbSdk.StopOption) error {
			called = true
			return nil
		}
		if err := (realSandbox{}).Stop(ctx); err != nil || !called {
			t.Fatalf("Stop() = %v, called=%v; want nil, true", err, called)
		}
	})

	t.Run("Destroy", func(t *testing.T) {
		original := realSandboxDestroy
		t.Cleanup(func() { realSandboxDestroy = original })
		wantErr := errors.New("destroy failed")
		realSandboxDestroy = func(_ *msbSdk.Sandbox, _ context.Context, _ ...msbSdk.DestroyOption) error {
			return wantErr
		}
		if err := (realSandbox{}).Destroy(ctx); !errors.Is(err, wantErr) {
			t.Fatalf("Destroy() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("Close", func(t *testing.T) {
		original := realSandboxClose
		t.Cleanup(func() { realSandboxClose = original })
		called := false
		realSandboxClose = func(_ *msbSdk.Sandbox) error {
			called = true
			return nil
		}
		if err := (realSandbox{}).Close(); err != nil || !called {
			t.Fatalf("Close() = %v, called=%v; want nil, true", err, called)
		}
	})
}

func TestGetVMStatus(t *testing.T) {
	tests := []struct {
		name    string
		status  msbSdk.SandboxStatus
		want    VMStatusKind
		wantErr bool
	}{
		{"running", msbSdk.SandboxStatusRunning, VMStatusActive, false},
		{"draining", msbSdk.SandboxStatusDraining, VMStatusActive, false},
		{"paused", msbSdk.SandboxStatusPaused, VMStatusActive, false},
		{"starting", msbSdk.SandboxStatusStarting, VMStatusStarting, false},
		{"created", msbSdk.SandboxStatusCreated, VMStatusCreated, false},
		{"stopped", msbSdk.SandboxStatusStopped, VMStatusStopped, false},
		{"crashed", msbSdk.SandboxStatusCrashed, VMStatusStopped, false},
		{"unknown", msbSdk.SandboxStatus(""), VMStatusUnknown, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetVMStatus(tt.status)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetVMStatus(%q) error = %v, wantErr %v", tt.status, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("GetVMStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIsSandboxInactive(t *testing.T) {
	tests := []struct {
		name   string
		status msbSdk.SandboxStatus
		want   bool
	}{
		{"created", msbSdk.SandboxStatusCreated, true},
		{"stopped", msbSdk.SandboxStatusStopped, true},
		{"crashed", msbSdk.SandboxStatusCrashed, true},
		{"starting", msbSdk.SandboxStatusStarting, false},
		{"running", msbSdk.SandboxStatusRunning, false},
		{"draining", msbSdk.SandboxStatusDraining, false},
		{"paused", msbSdk.SandboxStatusPaused, false},
		{"unknown", msbSdk.SandboxStatus(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSandboxInactive(tt.status); got != tt.want {
				t.Errorf("IsSandboxInactive(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}
