package volume

import (
	"context"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
)

func TestListVolumes_ReturnsOnlyHomeVolumes(t *testing.T) {
	mockClient := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{
				Name_: "opencode-msb-home-proj-aBc1234D",
				Path_: "/mnt/volumes/opencode-msb-home-proj-aBc1234D",
				Kind_: "project-home",
			},
			&msb.MockVolumeHandle{
				Name_: "opencode-msb-clone-proj-aBc1234D-1719432000",
				Path_: "/mnt/volumes/opencode-msb-clone-proj-aBc1234D-1719432000",
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
	if result[0].Name != "opencode-msb-home-proj-aBc1234D" {
		t.Errorf("expected name opencode-msb-home-proj-aBc1234D, got %q", result[0].Name)
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
