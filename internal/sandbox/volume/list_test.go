package volume

import (
	"testing"
)

func TestFilterVolumesByPrefix(t *testing.T) {
	handles := []volumeHandle{
		{name: "opencode-msb-home-proj-aBc1234D"},
		{name: "opencode-msb-clone-proj-aBc1234D-1719432000"},
		{name: "old-style-proj-opencode-home-sha256-abc"},
		{name: "random-volume"},
	}
	got := filterVolumes(handles)
	if len(got) != 1 {
		t.Fatalf("expected 1 home volume, got %d", len(got))
	}
	if got[0] != "opencode-msb-home-proj-aBc1234D" {
		t.Errorf("expected opencode-msb-home-proj-aBc1234D, got %q", got[0])
	}
}
