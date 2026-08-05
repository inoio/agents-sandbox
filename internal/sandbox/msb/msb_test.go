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
