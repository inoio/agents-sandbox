package msb

import (
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

func TestIsStoppedStatus(t *testing.T) {
	tests := []struct {
		name   string
		status msbSdk.SandboxStatus
		want   bool
	}{
		{"stopped", msbSdk.SandboxStatusStopped, true},
		{"crashed", msbSdk.SandboxStatusCrashed, true},
		{"running", msbSdk.SandboxStatusRunning, false},
		{"draining", msbSdk.SandboxStatusDraining, false},
		{"paused", msbSdk.SandboxStatusPaused, false},
		// Unknown statuses: IsSandboxActive returns false, so !IsSandboxActive = true.
		// This is a slight deviation from the old switch which returned false for unknown.
		{"unknown", msbSdk.SandboxStatus(""), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStoppedStatus(tt.status); got != tt.want {
				t.Errorf("IsStoppedStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}
