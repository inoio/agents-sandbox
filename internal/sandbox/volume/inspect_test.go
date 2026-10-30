package volume

import (
	"context"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestInspectVolumePopulatesDetail(t *testing.T) {
	quota := uint32(10240)
	capacity := uint64(20 * 1024 * 1024 * 1024)
	format := "raw"
	fstype := "ext4"
	mock := &msb.MockMsbClient{}
	mock.Volumes = []msb.VolumeHandle{
		&msb.MockVolumeHandle{
			Name_:          "agents-sandbox-home-proj-opencode-20260817T104236",
			Path_:          "/home/dev/.local/share/agents-sandbox/msb/volumes/agents-sandbox-home-proj",
			Kind_:          msbSdk.VolumeKindDisk,
			CreatedAt_:     time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
			QuotaMiB_:      &quota,
			UsedBytes_:     1024 * 1024 * 1024,
			CapacityBytes_: &capacity,
			DiskFormat_:    &format,
			DiskFstype_:    &fstype,
			Labels_:        map[string]string{"project": "proj"},
		},
	}
	msb.WithMsbMock(t, mock)

	got, err := InspectVolume(context.Background(), "agents-sandbox-home-proj-opencode-20260817T104236")
	if err != nil {
		t.Fatalf("InspectVolume() error = %v", err)
	}
	want := VolumeDetail{
		Name:          "agents-sandbox-home-proj-opencode-20260817T104236",
		Kind:          "disk",
		Path:          "/home/dev/.local/share/agents-sandbox/msb/volumes/agents-sandbox-home-proj",
		QuotaMiB:      &quota,
		UsedBytes:     1024 * 1024 * 1024,
		CapacityBytes: &capacity,
		DiskFormat:    &format,
		DiskFstype:    &fstype,
		Labels:        map[string]string{"project": "proj"},
		CreatedAt:     "2026-08-17 10:42:36",
	}
	assertVolumeDetailEqual(t, got, want)
}

func TestInspectVolumeMissing(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	_, err := InspectVolume(context.Background(), "agents-sandbox-home-missing")
	if err == nil || !strings.Contains(err.Error(), "inspect volume") {
		t.Fatalf("InspectVolume() error = %v, want inspect volume error", err)
	}
}

func assertVolumeDetailEqual(t *testing.T, got, want VolumeDetail) {
	t.Helper()
	if got.Name != want.Name || got.Kind != want.Kind || got.Path != want.Path ||
		got.UsedBytes != want.UsedBytes || got.CreatedAt != want.CreatedAt {
		t.Errorf("InspectVolume() = %+v, want %+v", got, want)
	}
	if !uint32PtrEqual(got.QuotaMiB, want.QuotaMiB) ||
		!uint64PtrEqual(got.CapacityBytes, want.CapacityBytes) ||
		!stringPtrEqual(got.DiskFormat, want.DiskFormat) ||
		!stringPtrEqual(got.DiskFstype, want.DiskFstype) {
		t.Errorf("InspectVolume() pointers = %+v, want %+v", got, want)
	}
	if len(got.Labels) != len(want.Labels) || got.Labels["project"] != want.Labels["project"] {
		t.Errorf("InspectVolume() labels = %v, want %v", got.Labels, want.Labels)
	}
}

func uint32PtrEqual(a, b *uint32) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func uint64PtrEqual(a, b *uint64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
