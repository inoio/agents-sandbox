package view

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
)

func marshal(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func TestListHeadersMatchMsbOrder(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{"sandbox", SandboxPrinter{}.Headers(), []string{"NAME", "IMAGE", "STATUS", "CREATED"}},
		{"image", ImagePrinter{}.Headers(), []string{"REFERENCE", "DIGEST", "SIZE", "CREATED"}},
		{"volume", VolumePrinter{}.Headers(), []string{"NAME", "KIND", "SIZE", "CREATED"}},
	}
	for _, tt := range tests {
		if !slices.Equal(tt.got, tt.want) {
			t.Errorf("%s headers = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestSandboxPrinterCellsAndJSON(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	info := sandbox.Info{
		Name: "vm1", Image: "img", Status: "stopped", CreatedAt: "1 day ago",
		CreatedAtRaw: created, UpdatedAtRaw: created, Labels: map[string]string{"k": "v"},
	}
	cells := SandboxPrinter{}.Cells(info)
	if len(cells) != 4 || cells[0] != "vm1" || cells[1] != "img" || cells[3] != "1 day ago" {
		t.Errorf("cells = %q", cells)
	}
	want := `[{"name":"vm1","status":"stopped","image":"img","created":"2026-01-02T03:04:05Z",` +
		`"updated":"2026-01-02T03:04:05Z","labels":{"k":"v"}}]`
	if got := marshal(t, SandboxPrinter{}.JSON([]sandbox.Info{info})); got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

func TestImagePrinterTruncatesDigestInCellsOnly(t *testing.T) {
	full := "sha256:c9b7a85bcbd83f0eab313d091efe933c1608e952a082def11eb038841cb66375"
	info := image.Info{Reference: "ref", Digest: full, Size: "1 GiB", CreatedAt: "now"}
	cells := ImagePrinter{}.Cells(info)
	if !slices.Equal(cells, []string{"ref", "sha256:c9b7a85bcbd8", "1 GiB", "now"}) {
		t.Errorf("cells = %q", cells)
	}
	want := `[{"reference":"ref","digest":"` + full + `","size":"1 GiB","created":"now"}]`
	if got := marshal(t, ImagePrinter{}.JSON([]image.Info{info})); got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

func TestTruncateDigestKeepsShortDigest(t *testing.T) {
	if got := truncateDigest("sha256:abc"); got != "sha256:abc" {
		t.Errorf("truncateDigest(short) = %q, want unchanged", got)
	}
}

func TestVolumePrinterCellsAndJSON(t *testing.T) {
	quota := uint32(2)
	info := volume.VolumeInfo{Name: "vol", Kind: "directory", QuotaMiB: &quota, CreatedAt: "now"}
	cells := VolumePrinter{}.Cells(info)
	if !slices.Equal(cells, []string{"vol", "directory", volumeSize("directory", &quota, nil), "now"}) {
		t.Errorf("cells = %q", cells)
	}
	got := marshal(t, VolumePrinter{}.JSON([]volume.VolumeInfo{info}))
	want := `[{"name":"vol","kind":"directory","size":"` + cells[2] + `","created":"now"}]`
	if got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

func TestVolumeSize(t *testing.T) {
	quota := uint32(1)
	capacity := uint64(2048)
	tests := []struct {
		name     string
		kind     string
		quota    *uint32
		capacity *uint64
		wantDash bool
	}{
		{"disk with capacity", "disk", nil, &capacity, false},
		{"disk without capacity", "disk", &quota, nil, true},
		{"directory with quota", "directory", &quota, nil, false},
		{"directory without quota", "directory", nil, &capacity, true},
	}
	for _, tt := range tests {
		got := volumeSize(tt.kind, tt.quota, tt.capacity)
		if (got == "-") != tt.wantDash {
			t.Errorf("%s: volumeSize = %q, wantDash %v", tt.name, got, tt.wantDash)
		}
	}
}
