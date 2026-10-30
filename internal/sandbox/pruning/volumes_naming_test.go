package pruning

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// TestCountVolumesByKeyBothSchemes pins that a legacy-prefixed and a
// prefix-free volume are both classified, and a foreign name is not.
func TestCountVolumesByKeyBothSchemes(t *testing.T) {
	handles := []msb.VolumeHandle{
		&msb.MockVolumeHandle{Name_: "agents-sandbox-home-myproj-1mjusbm3wikhb0-opencode-20260812T123456"},
		&msb.MockVolumeHandle{Name_: "myproj-1mjusbm3wikhb0-opencode-20260812T123457"},
		&msb.MockVolumeHandle{Name_: "some-other-volume"},
	}

	exists := countVolumesByKey(handles)
	if got := exists[state.Key{Slug: "myproj-1mjusbm3wikhb0", Agent: "opencode"}]; got != 2 {
		t.Errorf("count = %d, want 2 (legacy + prefix-free)", got)
	}
	if len(exists) != 1 {
		t.Errorf("exists = %v, want only the home-volume key", exists)
	}
}
