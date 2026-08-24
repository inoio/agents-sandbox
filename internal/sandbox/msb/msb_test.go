package msb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
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
	if plan, err := h.Modify(ctx, msbSdk.ModifyOptions{}); err == nil || plan != nil {
		t.Errorf("Modify() = %v, %v; want error on empty handle", plan, err)
	}
}

func TestRealMsbClientLookupErrors(t *testing.T) {
	c := &realMsbClient{}
	ctx := context.Background()

	if _, err := c.GetSandbox(ctx, "does-not-exist"); err == nil {
		t.Error("GetSandbox() = nil error, want error for missing sandbox")
	}
	if _, err := c.CreateSandbox(ctx, "does-not-exist"); err == nil {
		t.Error("CreateSandbox() = nil error, want error without image source")
	}
	if err := c.RemoveSandbox(ctx, "does-not-exist"); err == nil {
		t.Error("RemoveSandbox() = nil error, want error for missing sandbox")
	}
	if _, err := c.GetVolume(ctx, "does-not-exist"); err == nil {
		t.Error("GetVolume() = nil error, want error for missing volume")
	}
	if err := c.ImageGet(ctx, "does-not-exist"); err == nil {
		t.Error("ImageGet() = nil error, want error for missing image")
	}
	if err := c.ImageRemove(ctx, "does-not-exist", false); err == nil {
		t.Error("ImageRemove() = nil error, want error for missing image")
	}
	if _, err := c.ImageInspect(ctx, "does-not-exist"); err == nil {
		t.Error("ImageInspect() = nil error, want error for missing image")
	}
}

func TestRealMsbClientLists(t *testing.T) {
	c := &realMsbClient{}
	ctx := context.Background()

	if handles, err := c.ListSandboxes(ctx, nil); err != nil {
		t.Errorf("ListSandboxes() = %v, %v; want no error", handles, err)
	}
	if handles, err := c.ListVolumes(ctx); err != nil {
		t.Errorf("ListVolumes() = %v, %v; want no error", handles, err)
	}
	if handles, err := c.ImageList(ctx); err != nil {
		t.Errorf("ImageList() = %v, %v; want no error", handles, err)
	}
}

func TestRealMsbClientEnsureInstalled(t *testing.T) {
	c := &realMsbClient{}
	if err := c.EnsureInstalled(context.Background()); err != nil {
		t.Errorf("EnsureInstalled() error = %v, want nil", err)
	}
}

func TestRealMsbClientCreateRemoveVolume(t *testing.T) {
	c := &realMsbClient{}
	ctx := context.Background()
	name := "opencode-sandbox-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	if _, err := c.CreateVolume(ctx, name); err != nil {
		t.Fatalf("CreateVolume() error = %v, want nil", err)
	}
	if err := c.RemoveVolume(ctx, name); err != nil {
		t.Errorf("RemoveVolume() error = %v, want nil", err)
	}
}

func TestRealMsbClientImageLoadErrors(t *testing.T) {
	c := &realMsbClient{}
	ctx := context.Background()

	if err := c.ImageLoad(ctx, "ref", errReader{}); err == nil {
		t.Error("ImageLoad() with failing reader = nil error, want spooling error")
	}
	if err := c.ImageLoad(ctx, "ref", strings.NewReader("")); err == nil {
		t.Error("ImageLoad() with empty archive = nil error, want SDK load error")
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
