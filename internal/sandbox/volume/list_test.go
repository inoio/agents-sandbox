package volume

import (
	"context"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func TestListVolumesParseRoundTrip(t *testing.T) {
	name := HomeVolumeName(state.Key{Slug: "proj-abc123", Agent: "opencode"})
	info := naming.ArtifactFor(name)
	if info.Slug != "proj-abc123" || info.Agent != "opencode" {
		t.Errorf("ArtifactFor(%q) = %+v, want slug=proj-abc123 agent=opencode", name, info)
	}
}

func TestListVolumes_ReturnsOnlyHomeVolumes(t *testing.T) {
	mockClient := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{
				Name_: "agents-sandbox-home-proj-aBc1234D",
				Path_: "/mnt/volumes/agents-sandbox-home-proj-aBc1234D",
				Kind_: "project-home",
			},
			&msb.MockVolumeHandle{
				Name_: "agents-sandbox-clone-proj-aBc1234D-1719432000",
				Path_: "/mnt/volumes/agents-sandbox-clone-proj-aBc1234D-1719432000",
				Kind_: "project-clone",
			},
			&msb.MockVolumeHandle{Name_: "other-volume", Path_: "/mnt/volumes/other-volume", Kind_: "project-data"},
		},
	}
	msb.WithMsbMock(t, mockClient)

	result, err := ListVolumes(context.Background())
	if err != nil {
		t.Fatalf("ListVolumes returned error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 home volume, got %d", len(result))
	}
	if result[0].Name != "agents-sandbox-home-proj-aBc1234D" {
		t.Errorf("expected name agents-sandbox-home-proj-aBc1234D, got %q", result[0].Name)
	}
	if result[0].Kind != "project-home" {
		t.Errorf("expected kind project-home, got %q", result[0].Kind)
	}
}

func TestListVolumes_ErrorFromMsb(t *testing.T) {
	mockClient := &msb.MockMsbClient{
		ListVolumesErr: context.Canceled,
	}
	msb.WithMsbMock(t, mockClient)

	_, err := ListVolumes(context.Background())
	if err == nil {
		t.Fatal("expected error from ListVolumes, got nil")
	}
}

func TestListVolumes_EmptyReturnsNil(t *testing.T) {
	mockClient := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mockClient)

	result, err := ListVolumes(context.Background())
	if err != nil {
		t.Fatalf("ListVolumes returned error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty result, got %d volumes", len(result))
	}
}

func TestListVolumes_PopulatesMetadata(t *testing.T) {
	quota := uint32(1024)
	var capacity uint64 = 2 * 1024 * 1024 * 1024
	created := time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC)
	mockClient := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{
				Name_:          "agents-sandbox-home-proj",
				Kind_:          "disk",
				QuotaMiB_:      &quota,
				CapacityBytes_: &capacity,
				CreatedAt_:     created,
			},
		},
	}
	msb.WithMsbMock(t, mockClient)

	result, err := ListVolumes(context.Background())
	if err != nil {
		t.Fatalf("ListVolumes returned error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(result))
	}
	v := result[0]
	if v.QuotaMiB == nil || *v.QuotaMiB != quota {
		t.Errorf("QuotaMiB = %v, want %d", v.QuotaMiB, quota)
	}
	if v.CapacityBytes == nil || *v.CapacityBytes != capacity {
		t.Errorf("CapacityBytes = %v, want %d", v.CapacityBytes, capacity)
	}
	if v.CreatedAt != "2026-08-17 10:42:36" {
		t.Errorf("CreatedAt = %q, want %q", v.CreatedAt, "2026-08-17 10:42:36")
	}
}

func TestFormatVolumeTime(t *testing.T) {
	utc := time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC)
	if got := FormatVolumeTime(utc); got != "2026-08-17 10:42:36" {
		t.Errorf("FormatVolumeTime(nonzero) = %q, want %q", got, "2026-08-17 10:42:36")
	}
	if got := FormatVolumeTime(time.Time{}); got != "-" {
		t.Errorf("FormatVolumeTime(zero) = %q, want %q", got, "-")
	}
}
