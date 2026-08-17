package pruning

import (
	"context"
	"testing"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestPruneImages(t *testing.T) {
	old := time.Now().Add(-15 * 24 * time.Hour)

	img := func(ref string) msb.ImageHandle {
		return &msb.MockImageHandle{Reference_: ref, LastUsedAt_: old}
	}

	t.Run("prunes orphan slug and surplus digest of active slug", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1"),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:digestCur"),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:digestOld"),
				img("opencode-sandbox/runner-base:latest"), // base excluded
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		snap := LiveState{
			AllVMs:    map[string]bool{"active-1mjusbm3wikhb0": true},
			ActiveVMs: map[string]string{"active-1mjusbm3wikhb0": "digestCur"},
		}
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), snap, 7*24*time.Hour, false, false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 2 {
			t.Errorf("MSBImagesPruned = %d, want 2 (orphan digest1 + active digestOld)", r.MSBImagesPruned)
		}
		if len(client.RemovedImages) != 2 {
			t.Fatalf("RemovedImages = %v, want 2", client.RemovedImages)
		}
	})

	t.Run("dry run counts but does not delete", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{img("opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1")},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), LiveState{}, 7*24*time.Hour, false, true, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 1 {
			t.Errorf("MSBImagesPruned = %d, want 1", r.MSBImagesPruned)
		}
		if len(client.RemovedImages) != 0 {
			t.Errorf("dry run removed images: %v", client.RemovedImages)
		}
	})

	t.Run("all=true uses ActiveVMs keep-set", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{img("opencode-sandbox/runner-stopped-1mjusbm3wikhb0:digest1")},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		snap := LiveState{
			AllVMs:    map[string]bool{"stopped-1mjusbm3wikhb0": true},
			ActiveVMs: map[string]string{},
		}
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), snap, 7*24*time.Hour, true, false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 1 {
			t.Errorf("MSBImagesPruned = %d, want 1 under --all", r.MSBImagesPruned)
		}
	})

	t.Run("young image kept despite orphaned slug", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				&msb.MockImageHandle{
					Reference_:  "opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1",
					LastUsedAt_: time.Now(),
				},
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), LiveState{}, 7*24*time.Hour, false, false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 0 {
			t.Errorf("MSBImagesPruned = %d, want 0 (recent image)", r.MSBImagesPruned)
		}
	})
}
