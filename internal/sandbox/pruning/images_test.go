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

func TestPruneImages(t *testing.T) {
	old := time.Now().Add(-15 * 24 * time.Hour)

	img := func(ref string) msb.ImageHandle {
		return &msb.MockImageHandle{Reference_: ref, LastUsedAt_: old}
	}

	// State files store the full Docker image ID, while msb image tags carry
	// the shortened form (git.HashID of the full ID).
	fullCur := "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12"
	curTag := git.HashID(fullCur)

	t.Run("prunes stale slug and surplus digest of active slug", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		if err := state.WriteState("active-1mjusbm3wikhb0", state.HomeState{ImageDigest: fullCur}); err != nil {
			t.Fatalf("WriteState: %v", err)
		}
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1"),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:" + curTag),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:digestOld"),
				img("opencode-sandbox/runner-base:latest"),      // base excluded
				img("opencode-sandbox/runner-base-dind:latest"), // base-dind excluded
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		stateMap := PruneState{"orphan-1mjusbm3wikhb0": &msb.MockSandboxHandle{}}
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), stateMap, false, ui)
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
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{img("opencode-sandbox/runner-orphan-1mjusbm3wikhb0:digest1")},
		}
		msb.WithMsbMock(t, client)
		ui := &termio.Mock{}
		r, err := PruneImages(
			context.Background(),
			PruneState{"orphan-1mjusbm3wikhb0": &msb.MockSandboxHandle{}},
			true,
			ui,
		)
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

	t.Run("keeps current digest and prunes only surplus for active slug", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		if err := state.WriteState("active-1mjusbm3wikhb0", state.HomeState{ImageDigest: fullCur}); err != nil {
			t.Fatalf("WriteState: %v", err)
		}
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:" + curTag),
				img("opencode-sandbox/runner-active-1mjusbm3wikhb0:digestOld"),
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), PruneState{}, false, ui)
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

	t.Run("keeps all digests when slug has no state file", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("opencode-sandbox/runner-unknown-1mjusbm3wikhb0:digest1"),
				img("opencode-sandbox/runner-unknown-1mjusbm3wikhb0:digest2"),
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), PruneState{}, false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 0 {
			t.Errorf("MSBImagesPruned = %d, want 0 (no state to determine current digest)", r.MSBImagesPruned)
		}
	})

	t.Run("surplusDigest false for empty digest and missing state", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		if surplusDigest("some-slug", "") {
			t.Error("surplusDigest with empty digest must be false")
		}
		if surplusDigest("no-state-1mjusbm3wikhb0", "digest1") {
			t.Error("surplusDigest without state must be false")
		}
	})

	t.Run("keeps current digest when state stores full image ID", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		fullID := "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12"
		if err := state.WriteState("active-1mjusbm3wikhb0", state.HomeState{ImageDigest: fullID}); err != nil {
			t.Fatalf("WriteState: %v", err)
		}
		// The msb image tag stores the shortened digest; the current image must
		// not be treated as surplus even though the tag never equals the full ID.
		tagDigest := git.HashID(fullID)
		if surplusDigest("active-1mjusbm3wikhb0", tagDigest) {
			t.Errorf("surplusDigest(%q, %q) = true, want false: current image must be kept", tagDigest, fullID)
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
				img("opencode-sandbox/runner-fail-1mjusbm3wikhb0:digest1"),
				img("opencode-sandbox/runner-ok-1mjusbm3wikhb0:digest2"),
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
		r, err := PruneImages(context.Background(), PruneState{
			"fail-1mjusbm3wikhb0": &msb.MockSandboxHandle{},
			"ok-1mjusbm3wikhb0":   &msb.MockSandboxHandle{},
		}, false, ui)
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
		client := &msb.MockMsbClient{Images: []msb.ImageHandle{img("opencode-sandbox/runner-base:latest")}}
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
