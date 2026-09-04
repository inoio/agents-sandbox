package pruning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestPruneVolumes(t *testing.T) {
	old := time.Now().Add(-15 * 24 * time.Hour)

	home := func(name string) msb.VolumeHandle {
		return &msb.MockVolumeHandle{Name_: name, CreatedAt_: old}
	}

	t.Run("prunes stale slug volumes and keeps others", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{
				home("agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"),
				home("agents-sandbox-home-kept-1mjusbm3wikhb0-20260806T143022"),
			},
		}
		msb.WithMsbMock(t, client)
		stateMap := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{
				{Slug: "stale-1mjusbm3wikhb0", Agent: ""}: &msb.MockSandboxHandle{},
			},
		}
		ui := &termio.Mock{}
		r, err := PruneVolumes(context.Background(), stateMap, false, ui)
		if err != nil {
			t.Fatalf("PruneVolumes: %v", err)
		}
		if r.VolumesPruned != 1 {
			t.Errorf("VolumesPruned = %d, want 1", r.VolumesPruned)
		}
		if len(client.RemovedVolumes) != 1 ||
			client.RemovedVolumes[0] != "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022" {
			t.Errorf("RemovedVolumes = %v, want stale volume only", client.RemovedVolumes)
		}
	})

	t.Run("removes state when last volume for slug is removed", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		slug := "stale-1mjusbm3wikhb0"
		if err := state.WriteState(
			state.Key{Slug: slug, Agent: ""},
			state.HomeState{HomeVolume: "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"},
		); err != nil {
			t.Fatalf("WriteState: %v", err)
		}
		client := &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{home("agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022")},
		}
		msb.WithMsbMock(t, client)
		stateMap := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{{Slug: slug, Agent: ""}: &msb.MockSandboxHandle{}},
		}
		ui := &termio.Mock{}
		if _, err := PruneVolumes(context.Background(), stateMap, false, ui); err != nil {
			t.Fatalf("PruneVolumes: %v", err)
		}
		if _, err := state.ReadState(state.Key{Slug: slug, Agent: ""}); !errors.Is(err, state.ErrStateNotFound) {
			t.Errorf("state file for %s should be removed, got err=%v", slug, err)
		}
	})

	t.Run("keeps state when a volume for slug remains", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		slug := "stale-1mjusbm3wikhb0"
		if err := state.WriteState(
			state.Key{Slug: slug, Agent: ""},
			state.HomeState{HomeVolume: "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"},
		); err != nil {
			t.Fatalf("WriteState: %v", err)
		}
		client := &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{
				home("agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"),
				home("agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143023"),
			},
			RemoveVolumeFn: func(_ context.Context, name string) error {
				if name == "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143023" {
					return errors.New("boom")
				}
				return nil
			},
		}
		msb.WithMsbMock(t, client)
		stateMap := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{{Slug: slug, Agent: ""}: &msb.MockSandboxHandle{}},
		}
		ui := &termio.Mock{}
		if _, err := PruneVolumes(context.Background(), stateMap, false, ui); err != nil {
			t.Fatalf("PruneVolumes: %v", err)
		}
		if _, err := state.ReadState(state.Key{Slug: slug, Agent: ""}); err != nil {
			t.Errorf("state file for %s should remain when a volume survives, got err=%v", slug, err)
		}
	})

	t.Run("dry run counts but does not delete", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{home("agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022")},
		}
		msb.WithMsbMock(t, client)
		stateMap := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{
				{Slug: "stale-1mjusbm3wikhb0", Agent: ""}: &msb.MockSandboxHandle{},
			},
		}
		ui := &termio.Mock{}
		r, err := PruneVolumes(context.Background(), stateMap, true, ui)
		if err != nil {
			t.Fatalf("PruneVolumes: %v", err)
		}
		if r.VolumesPruned != 1 {
			t.Errorf("VolumesPruned = %d, want 1", r.VolumesPruned)
		}
		if len(client.RemovedVolumes) != 0 {
			t.Errorf("dry run removed volumes: %v", client.RemovedVolumes)
		}
	})

	t.Run("empty state prunes nothing", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{home("agents-sandbox-home-kept-1mjusbm3wikhb0-20260806T143022")},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneVolumes(context.Background(), PruneState{}, false, ui)
		if err != nil {
			t.Fatalf("PruneVolumes: %v", err)
		}
		if r.VolumesPruned != 0 {
			t.Errorf("VolumesPruned = %d, want 0", r.VolumesPruned)
		}
	})

	t.Run("returns ListVolumes error", func(t *testing.T) {
		client := &msb.MockMsbClient{
			ListVolumesFn: func(context.Context) ([]msb.VolumeHandle, error) {
				return nil, errors.New("list boom")
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		_, err := PruneVolumes(context.Background(), PruneState{}, false, ui)
		if err == nil || !strings.Contains(err.Error(), "list boom") {
			t.Errorf("expected ListVolumes error, got %v", err)
		}
	})

	t.Run("skips unparseable and non-home volumes", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Volumes: []msb.VolumeHandle{
				home("agents-sandbox-home-"),
				&msb.MockVolumeHandle{Name_: "some-other-volume", CreatedAt_: old},
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneVolumes(
			context.Background(),
			PruneState{ToPrune: map[state.Key]msb.SandboxHandle{{Slug: "", Agent: ""}: &msb.MockSandboxHandle{}}},
			false,
			ui,
		)
		if err != nil {
			t.Fatalf("PruneVolumes: %v", err)
		}
		if r.VolumesPruned != 0 {
			t.Errorf("VolumesPruned = %d, want 0 (unparseable/non-home volumes)", r.VolumesPruned)
		}
		if len(client.RemovedVolumes) != 0 {
			t.Errorf("RemovedVolumes = %v, want none", client.RemovedVolumes)
		}
	})
}
