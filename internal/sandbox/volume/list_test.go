package volume

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

func TestFormatVolumeTime(t *testing.T) {
	utc := time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC)
	if got := FormatVolumeTime(utc); got != "2026-08-17 10:42:36" {
		t.Errorf("FormatVolumeTime(nonzero) = %q, want %q", got, "2026-08-17 10:42:36")
	}
	if got := FormatVolumeTime(time.Time{}); got != "-" {
		t.Errorf("FormatVolumeTime(zero) = %q, want %q", got, "-")
	}
}

func TestListVolumesShowsBothSchemes(t *testing.T) {
	// A non-home volume comes first, so a filter that stopped the scan is caught.
	mock := &msbtest.MockMsbClient{Volumes: []msb.VolumeHandle{
		&msbtest.MockVolumeHandle{Name_: "some-other-volume"},
		&msbtest.MockVolumeHandle{Name_: "agents-sandbox-home-myproj-1mjusbm3wikhb0-opencode-20260812T123456"},
		&msbtest.MockVolumeHandle{Name_: "myproj-1mjusbm3wikhb0-opencode-20260812T123457"},
	}}
	msbtest.WithMsbMock(t, mock)

	got, err := ListVolumes(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode-20260812T123457", got[1].Name)
}

func TestListVolumes_ReturnsOnlyHomeVolumes(t *testing.T) {
	mockClient := &msbtest.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msbtest.MockVolumeHandle{
				Name_: "agents-sandbox-home-proj-aBc1234D",
				Path_: "/mnt/volumes/agents-sandbox-home-proj-aBc1234D",
				Kind_: "project-home",
			},
			&msbtest.MockVolumeHandle{
				Name_: "agents-sandbox-clone-proj-aBc1234D-1719432000",
				Path_: "/mnt/volumes/agents-sandbox-clone-proj-aBc1234D-1719432000",
				Kind_: "project-clone",
			},
			&msbtest.MockVolumeHandle{Name_: "other-volume", Path_: "/mnt/volumes/other-volume", Kind_: "project-data"},
		},
	}
	msbtest.WithMsbMock(t, mockClient)

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
	mockClient := &msbtest.MockMsbClient{
		ListVolumesErr: context.Canceled,
	}
	msbtest.WithMsbMock(t, mockClient)

	_, err := ListVolumes(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ListVolumes error = %v, want it to wrap %v", err, context.Canceled)
	}
}

func TestListVolumes_EmptyReturnsNil(t *testing.T) {
	mockClient := &msbtest.MockMsbClient{}
	msbtest.WithMsbMock(t, mockClient)

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
	mockClient := &msbtest.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msbtest.MockVolumeHandle{
				Name_:          "agents-sandbox-home-proj",
				Kind_:          "disk",
				QuotaMiB_:      &quota,
				CapacityBytes_: &capacity,
				CreatedAt_:     created,
			},
		},
	}
	msbtest.WithMsbMock(t, mockClient)

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
