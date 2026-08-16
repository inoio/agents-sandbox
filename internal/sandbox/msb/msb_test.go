package msb

import (
	"context"
	"errors"
	"fmt"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

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
