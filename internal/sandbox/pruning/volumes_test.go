package pruning

import (
	"context"
	"testing"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestPruneVolumes(t *testing.T) {
	old := time.Now().Add(-15 * 24 * time.Hour)

	home := func(_, name string) msb.VolumeHandle {
		return &msb.MockVolumeHandle{Name_: name, CreatedAt_: old}
	}

	t.Run("keeps volume when slug in AllVMs, prunes orphan slug", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{
				home("kept", "opencode-sandbox-home-kept-1mjusbm3wikhb0-20260806T143022"),
				home("orphan", "opencode-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022"),
			},
		}
		msb.WithMsbMock(t, client)
		snap := LiveState{AllVMs: map[string]bool{"kept-1mjusbm3wikhb0": true}}
		ui := &termio.Mock{}
		r, err := PruneVolumes(context.Background(), snap, 7*24*time.Hour, false, false, ui)
		if err != nil {
			t.Fatalf("PruneVolumes: %v", err)
		}
		if r.VolumesPruned != 1 {
			t.Errorf("VolumesPruned = %d, want 1", r.VolumesPruned)
		}
		if len(client.RemovedVolumes) != 1 ||
			client.RemovedVolumes[0] != "opencode-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022" {
			t.Errorf("RemovedVolumes = %v, want orphan volume only", client.RemovedVolumes)
		}
	})

	t.Run("all=true uses ActiveVMs keep-set", func(t *testing.T) {
		// slug has a stopped-but-fresh VM (AllVMs) but no running VM.
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{
				home("stoppedproj", "opencode-sandbox-home-stoppedproj-1mjusbm3wikhb0-20260806T143022"),
			},
		}
		msb.WithMsbMock(t, client)
		snap := LiveState{
			AllVMs:    map[string]bool{"stoppedproj-1mjusbm3wikhb0": true},
			ActiveVMs: map[string]string{},
		}
		ui := &termio.Mock{}
		r, err := PruneVolumes(context.Background(), snap, 7*24*time.Hour, true, false, ui)
		if err != nil {
			t.Fatalf("PruneVolumes: %v", err)
		}
		if r.VolumesPruned != 1 {
			t.Errorf("VolumesPruned = %d, want 1 under --all", r.VolumesPruned)
		}
	})

	t.Run("young volume not pruned even when orphaned", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{
				&msb.MockVolumeHandle{
					Name_:      "opencode-sandbox-home-orphan-1mjusbm3wikhb0-20260806T143022",
					CreatedAt_: time.Now(),
				},
			},
		}
		msb.WithMsbMock(t, client)
		snap := LiveState{AllVMs: map[string]bool{}}
		ui := &termio.Mock{}
		r, err := PruneVolumes(context.Background(), snap, 7*24*time.Hour, false, false, ui)
		if err != nil {
			t.Fatalf("PruneVolumes: %v", err)
		}
		if r.VolumesPruned != 0 {
			t.Errorf("VolumesPruned = %d, want 0 (recent)", r.VolumesPruned)
		}
	})
}
