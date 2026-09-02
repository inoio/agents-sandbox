package pruning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// fullCur is the full Docker image ID a state file stores; msb image tags carry
// the shortened form (git.HashID of the full ID).
const fullCur = "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12"

func TestPruneImages(t *testing.T) {
	now := time.Now()
	old := time.Now().Add(-15 * 24 * time.Hour)
	older := time.Now().Add(-30 * 24 * time.Hour)
	curTag := git.HashID(fullCur)

	img := func(ref string, createdAt time.Time) msb.ImageHandle {
		return &msb.MockImageHandle{Reference_: ref, CreatedAt_: createdAt}
	}
	live := func(slug string) PruneState {
		return PruneState{ToKeep: map[string]struct{}{slug: {}}}
	}

	t.Run("prunes VM-less slug and older surplus of a kept slug", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		if err := state.WriteState("active-1mjusbm3wikhb0", state.HomeState{ImageDigest: fullCur}); err != nil {
			t.Fatalf("WriteState: %v", err)
		}
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1", old),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:"+curTag, old),
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

	t.Run("keeps current and newer images of a live slug, prunes older surplus", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		if err := state.WriteState("active-1mjusbm3wikhb0", state.HomeState{ImageDigest: fullCur}); err != nil {
			t.Fatalf("WriteState: %v", err)
		}
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:"+curTag, old),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:newer", now),
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

	t.Run("isCurrentOrNewer true for current image stored as full ID", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		if err := state.WriteState("active-1mjusbm3wikhb0", state.HomeState{ImageDigest: fullCur}); err != nil {
			t.Fatalf("WriteState: %v", err)
		}
		h := img("opencode-sandbox/runner-active-1mjusbm3wikhb0:"+curTag, old)
		if !isCurrentOrNewer("active-1mjusbm3wikhb0", curTag, h, []msb.ImageHandle{h}) {
			t.Errorf("current image must be kept even though tag != full ID")
		}
	})

	t.Run("isCurrentOrNewer conservative without state", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		h := img("opencode-sandbox/runner-unknown-1mjusbm3wikhb0:digest1", old)
		if !isCurrentOrNewer("no-state-1mjusbm3wikhb0", "digest1", h, []msb.ImageHandle{h}) {
			t.Error("isCurrentOrNewer without state must be conservative (keep)")
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

// TestPruneImagesPreservesFreshSurplus reproduces the "imported msb image pruned
// after the user quit" bug: a fresh runner image was loaded into microsandbox
// but, because the user quit at the image-change home-volume prompt, its digest
// was never recorded in the state file. The live VM's current image and the fresh
// replacement must be kept, and only the older surplus digest reclaimed, so the
// next run does not re-import the fresh image.
func TestPruneImagesPreservesFreshSurplus(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	now := time.Now()
	old := time.Now().Add(-15 * 24 * time.Hour)
	older := time.Now().Add(-30 * 24 * time.Hour)
	fullOld := "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12"
	if err := state.WriteState("active-1mjusbm3wikhb0", state.HomeState{ImageDigest: fullOld}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	curTag := git.HashID(fullOld)
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			// The currently-used image (matching the state digest).
			&msb.MockImageHandle{
				Reference_: "opencode-sandbox/runner-active-1mjusbm3wikhb0:" + curTag,
				CreatedAt_: old,
			},
			// A freshly imported replacement: same digest gap but newer.
			&msb.MockImageHandle{
				Reference_: "opencode-sandbox/runner-active-1mjusbm3wikhb0:newdigest",
				CreatedAt_: now,
			},
			// A genuinely old surplus digest: older than the current image.
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
// reclaimed, including the previously-current image.
func TestPruneImagesReclaimsKilledProjectImages(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	old := time.Now().Add(-15 * 24 * time.Hour)
	fullOld := "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12"
	if err := state.WriteState("gone-1mjusbm3wikhb0", state.HomeState{ImageDigest: fullOld}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	curTag := git.HashID(fullOld)
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "opencode-sandbox/runner-gone-1mjusbm3wikhb0:" + curTag, CreatedAt_: old},
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
