package pruning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestPruneImages(t *testing.T) {
	now := time.Now()
	old := time.Now().Add(-15 * 24 * time.Hour)
	older := time.Now().Add(-30 * 24 * time.Hour)

	img := func(ref string, createdAt time.Time) msb.ImageHandle {
		return &msb.MockImageHandle{Reference_: ref, CreatedAt_: createdAt}
	}
	live := func(slug string) PruneState {
		return PruneState{ToKeep: map[string]struct{}{slug: {}}}
	}

	t.Run("prunes VM-less slug and older surplus of a kept slug", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1", old),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:opencode-latest", old),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:digestOld", older),
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ps := live("active-1mjusbm3wikhb0")
		ps.ToPrune = map[string]msb.SandboxHandle{"orphan-1mjusbm3wikhb0": &msb.MockSandboxHandle{}}
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), ps, false, ui)
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

	t.Run("reclaims all images of a VM-less slug", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1", old),
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ps := PruneState{ToPrune: map[string]msb.SandboxHandle{"orphan-1mjusbm3wikhb0": &msb.MockSandboxHandle{}}}
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), ps, true, ui)
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

	t.Run("keeps per-agent latest tags of a live slug, prunes surplus", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:opencode-latest", old),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:pi-latest", now),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:digestOld", older),
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), live("active-1mjusbm3wikhb0"), false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 1 {
			t.Errorf("MSBImagesPruned = %d, want 1 (only digestOld)", r.MSBImagesPruned)
		}
		if len(client.RemovedImages) != 1 ||
			client.RemovedImages[0].Ref != "opencode-sandbox/runner-active-1mjusbm3wikhb0:digestOld" {
			t.Errorf("RemovedImages = %v, want only digestOld", client.RemovedImages)
		}
	})

	t.Run("reclaims images of a slug that has neither VM nor state", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-unknown-1mjusbm3wikhb0:digest1", old),
				img("opencode-sandbox/runner-unknown-1mjusbm3wikhb0:digest2", old),
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), PruneState{}, false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 2 {
			t.Errorf("MSBImagesPruned = %d, want 2 (VM-less slug has no keepable image)", r.MSBImagesPruned)
		}
	})

	t.Run("returns ImageList error", func(t *testing.T) {
		client := &msb.MockMsbClient{
			ImageListFn: func(context.Context) ([]msb.ImageHandle, error) {
				return nil, errors.New("list boom")
			},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		_, err := PruneImages(context.Background(), PruneState{}, false, ui)
		if err == nil || !strings.Contains(err.Error(), "list boom") {
			t.Errorf("expected ImageList error, got %v", err)
		}
	})

	t.Run("continues past an ImageRemove error", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		removed := make(map[string]bool)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-fail-1mjusbm3wikhb0:digest1", old),
				img("opencode-sandbox/runner-ok-1mjusbm3wikhb0:digest2", old),
			},
			ImageRemoveFn: func(_ context.Context, ref string, _ bool) error {
				if ref == "opencode-sandbox/runner-fail-1mjusbm3wikhb0:digest1" {
					return errors.New("remove boom")
				}
				removed[ref] = true
				return nil
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), PruneState{ToPrune: map[string]msb.SandboxHandle{
			"fail-1mjusbm3wikhb0": &msb.MockSandboxHandle{},
			"ok-1mjusbm3wikhb0":   &msb.MockSandboxHandle{},
		}}, false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 1 {
			t.Errorf("MSBImagesPruned = %d, want 1 (failed one is skipped)", r.MSBImagesPruned)
		}
		if !removed["opencode-sandbox/runner-ok-1mjusbm3wikhb0:digest2"] {
			t.Errorf("expected the non-failing image to be removed, got %v", removed)
		}
		if len(ui.WarnCalls) != 1 {
			t.Errorf("WarnCalls = %v, want 1 warn about the failed removal", ui.WarnCalls)
		}
	})

	t.Run("docker prune error is warned but not fatal", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{img("opencode-sandbox/runner-dockererr-1mjusbm3wikhb0:digest1", old)},
		}
		msb.WithMsbMock(t, client)
		docker.WithDefaultErrorDockerMock(t)
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), PruneState{}, false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.DockerImagesPruned != 0 {
			t.Errorf("DockerImagesPruned = %d, want 0 on error", r.DockerImagesPruned)
		}
		if len(ui.WarnCalls) == 0 {
			t.Errorf("expected a warn about the docker prune failure, got %v", ui.WarnCalls)
		}
	})
}

// TestPruneImagesKeepsPerAgentTagsOfLiveSlug verifies that a live slug keeps
// exactly its per-agent "-latest" refs and reclaims every other (digest) ref.
func TestPruneImagesKeepsPerAgentTagsOfLiveSlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	now := time.Now()
	old := time.Now().Add(-15 * 24 * time.Hour)
	older := time.Now().Add(-30 * 24 * time.Hour)
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_: "opencode-sandbox/runner-active-1mjusbm3wikhb0:opencode-latest",
				CreatedAt_: old,
			},
			&msb.MockImageHandle{
				Reference_: "opencode-sandbox/runner-active-1mjusbm3wikhb0:pi-latest",
				CreatedAt_: now,
			},
			&msb.MockImageHandle{
				Reference_: "opencode-sandbox/runner-active-1mjusbm3wikhb0:olddigest",
				CreatedAt_: older,
			},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	ps := PruneState{ToKeep: map[string]struct{}{"active-1mjusbm3wikhb0": {}}}
	r, err := PruneImages(context.Background(), ps, false, ui)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 1 {
		t.Errorf("MSBImagesPruned = %d, want 1 (reclaim non -latest ref)", r.MSBImagesPruned)
	}
	if len(client.RemovedImages) != 1 ||
		client.RemovedImages[0].Ref != "opencode-sandbox/runner-active-1mjusbm3wikhb0:olddigest" {
		t.Errorf("RemovedImages = %v, want only the non -latest ref", client.RemovedImages)
	}
}

// TestPruneImagesPreservesFreshSurplus reproduces the "imported msb image pruned
// after the user quit" bug: a fresh runner image was loaded into microsandbox
// but, because the user quit at the image-change home-volume prompt, its digest
// was never recorded in the state file. A live slug must keep its per-agent
// "-latest" refs and reclaim only the surplus digest ref, so the next run does
// not re-import the fresh image.
func TestPruneImagesPreservesFreshSurplus(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	now := time.Now()
	old := time.Now().Add(-15 * 24 * time.Hour)
	older := time.Now().Add(-30 * 24 * time.Hour)
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			// A per-agent latest tag for the live slug.
			&msb.MockImageHandle{
				Reference_: "opencode-sandbox/runner-active-1mjusbm3wikhb0:opencode-latest",
				CreatedAt_: old,
			},
			// A freshly imported replacement for another agent.
			&msb.MockImageHandle{
				Reference_: "opencode-sandbox/runner-active-1mjusbm3wikhb0:pi-latest",
				CreatedAt_: now,
			},
			// A genuinely old surplus digest: reclaimed.
			&msb.MockImageHandle{
				Reference_: "opencode-sandbox/runner-active-1mjusbm3wikhb0:digestOld",
				CreatedAt_: older,
			},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	ps := PruneState{ToKeep: map[string]struct{}{"active-1mjusbm3wikhb0": {}}}
	r, err := PruneImages(context.Background(), ps, false, ui)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 1 {
		t.Errorf("MSBImagesPruned = %d, want 1 (only the old surplus digest)", r.MSBImagesPruned)
	}
	if len(client.RemovedImages) != 1 ||
		client.RemovedImages[0].Ref != "opencode-sandbox/runner-active-1mjusbm3wikhb0:digestOld" {
		t.Errorf("RemovedImages = %v, want only the old surplus digest kept fresh image", client.RemovedImages)
	}
}

// TestPruneImagesReclaimsKilledProjectImages covers the dangling-image gap: a
// project whose VM was removed outside the prune flow (e.g. kill --force) leaves
// its msb images behind. With no VM, the slug is VM-less and its images are
// reclaimed, including any current/latest-tagged image.
func TestPruneImagesReclaimsKilledProjectImages(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	old := time.Now().Add(-15 * 24 * time.Hour)
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_: "opencode-sandbox/runner-gone-1mjusbm3wikhb0:opencode-latest",
				CreatedAt_: old,
			},
			&msb.MockImageHandle{Reference_: "opencode-sandbox/runner-gone-1mjusbm3wikhb0:old", CreatedAt_: old},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	r, err := PruneImages(context.Background(), PruneState{}, false, ui)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 2 {
		t.Errorf("MSBImagesPruned = %d, want 2 (VM-less slug images reclaimed)", r.MSBImagesPruned)
	}
	if len(client.RemovedImages) != 2 {
		t.Errorf("RemovedImages = %v, want 2 (both killed-project images reclaimed)", client.RemovedImages)
	}
}
