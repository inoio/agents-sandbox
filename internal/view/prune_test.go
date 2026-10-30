package view

import (
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
)

func TestNewPruneReportJSONMapsCountsAndDetails(t *testing.T) {
	report := &pruning.StaleReport{
		PrunedSandboxes: 1, PrunedVolumes: 2, PrunedSandboxImages: 3, PrunedDockerImages: 4,
		Details: []pruning.StaleEntry{{Name: "vm", Slug: "s", Digest: "d", StaleFor: 90 * time.Second}},
	}
	got := NewPruneReportJSON(report, true)
	if !got.DryRun || got.Sandboxes != 1 || got.Volumes != 2 || got.Images != 3 || got.DockerImages != 4 {
		t.Errorf("counts = %+v", got)
	}
	if len(got.Details) != 1 || got.Details[0].StaleForSeconds != 90 || got.Details[0].Name != "vm" {
		t.Errorf("details = %+v", got.Details)
	}
}

func TestNewPruneReportJSONIncludesStorageAndImageReport(t *testing.T) {
	physical := uint64(4096)
	report := &pruning.StaleReport{
		SkippedImagesInUse:  4,
		ImageBytesReclaimed: &physical,
		Storage: pruning.StorageReport{
			FilesRemoved:           3,
			LogicalBytesRemoved:    12288,
			PhysicalBytesReclaimed: &physical,
		},
	}
	got := NewPruneReportJSON(report, false)
	if got.SkippedImagesInUse != 4 || got.ImageBytesReclaimed == nil || *got.ImageBytesReclaimed != physical {
		t.Errorf("image fields = %+v", got)
	}
	if got.RuntimeFilesRemoved != 3 || got.RuntimeLogicalBytesRemoved != 12288 ||
		got.RuntimePhysicalBytesReclaimed == nil {
		t.Errorf("storage fields = %+v", got)
	}

	encoded := marshal(t, got)
	for _, key := range []string{
		`"skipped_images_in_use":4`,
		`"image_bytes_reclaimed":4096`,
		`"runtime_files_removed":3`,
		`"runtime_logical_bytes_removed":12288`,
		`"runtime_physical_bytes_reclaimed":4096`,
	} {
		if !strings.Contains(encoded, key) {
			t.Errorf("JSON missing %s: %s", key, encoded)
		}
	}
}

func TestNewPruneReportJSONUnknownBytesMarshalNull(t *testing.T) {
	got := NewPruneReportJSON(&pruning.StaleReport{Storage: pruning.StorageReport{FilesRemoved: 1}}, false)
	encoded := marshal(t, got)
	for _, key := range []string{`"image_bytes_reclaimed":null`, `"runtime_physical_bytes_reclaimed":null`} {
		if !strings.Contains(encoded, key) {
			t.Errorf("unknown bytes must marshal as null, missing %s: %s", key, encoded)
		}
	}
}

func TestNewImageReportJSONIncludesNativeReport(t *testing.T) {
	physical := uint64(2048)
	got := NewImageReportJSON(
		pruning.ImageReport{MSBImagesPruned: 1, SkippedInUse: 4, BytesReclaimed: &physical},
		false,
	)
	if got.SkippedInUse != 4 || got.BytesReclaimed == nil || *got.BytesReclaimed != physical {
		t.Errorf("native fields = %+v", got)
	}
	encoded := marshal(t, got)
	for _, key := range []string{`"skipped_in_use":4`, `"bytes_reclaimed":2048`} {
		if !strings.Contains(encoded, key) {
			t.Errorf("JSON missing %s: %s", key, encoded)
		}
	}
}

func TestGroupReportJSONsKeepEmptyDetailsAsArray(t *testing.T) {
	values := []any{
		NewImageReportJSON(pruning.ImageReport{MSBImagesPruned: 1}, false),
		NewVolumeReportJSON(pruning.VolumeReport{VolumesPruned: 1}, false),
		NewSandboxReportJSON(pruning.SandboxReport{VMsPruned: 1}, false),
	}
	for _, value := range values {
		if got := marshal(t, value); !strings.Contains(got, `"details":[]`) {
			t.Errorf("empty details must marshal as [], got %s", got)
		}
	}
}
