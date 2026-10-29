package pruning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// keepImageFor asks keepImage about a handle's reference the way PruneImages
// does: parsing the listed reference once and passing the artifact in.
func keepImageFor(
	handle msb.ImageHandle,
	pruneState PruneState,
	keptImageRefs map[string]struct{},
) bool {
	return keepImage(handle, naming.ParseImageTag(handle.Reference()), pruneState, keptImageRefs)
}

func TestPruneImages(t *testing.T) {
	now := time.Now()
	old := time.Now().Add(-15 * 24 * time.Hour)
	older := time.Now().Add(-30 * 24 * time.Hour)

	img := func(ref string, createdAt time.Time) msb.ImageHandle {
		return &msb.MockImageHandle{Reference_: ref, CreatedAt_: createdAt}
	}
	live := func(slug string) PruneState {
		return PruneState{
			Age:    30 * 24 * time.Hour,
			ToKeep: map[state.Key]msb.SandboxHandle{{Slug: slug, Agent: "opencode"}: &msb.MockSandboxHandle{}},
		}
	}

	t.Run("prunes VM-less slug and older surplus of a kept slug", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("agents-sandbox/runner-orphan-1mjusbm3wikhb0:digest1", old),
				img("agents-sandbox/runner-active-1mjusbm3wikhb0:opencode-latest", old),
				img("agents-sandbox/runner-active-1mjusbm3wikhb0:digestOld", older),
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ps := live("active-1mjusbm3wikhb0")
		ps.ToPrune = map[state.Key]msb.SandboxHandle{
			{Slug: "orphan-1mjusbm3wikhb0", Agent: ""}: &msb.MockSandboxHandle{},
		}
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
				img("agents-sandbox/runner-orphan-1mjusbm3wikhb0:digest1", old),
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ps := PruneState{
			ToPrune: map[state.Key]msb.SandboxHandle{
				{Slug: "orphan-1mjusbm3wikhb0", Agent: ""}: &msb.MockSandboxHandle{},
			},
		}
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
				img("agents-sandbox/runner-active-1mjusbm3wikhb0:opencode-latest", old),
				img("agents-sandbox/runner-active-1mjusbm3wikhb0:pi-latest", now),
				img("agents-sandbox/runner-active-1mjusbm3wikhb0:digestOld", older),
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ui := &termio.Mock{}
		ps := PruneState{Age: 30 * 24 * time.Hour, ToKeep: map[state.Key]msb.SandboxHandle{
			{Slug: "active-1mjusbm3wikhb0", Agent: "opencode"}: &msb.MockSandboxHandle{},
			{Slug: "active-1mjusbm3wikhb0", Agent: "pi"}:       &msb.MockSandboxHandle{},
		}}
		r, err := PruneImages(context.Background(), ps, false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 1 {
			t.Errorf("MSBImagesPruned = %d, want 1 (only digestOld)", r.MSBImagesPruned)
		}
		if len(client.RemovedImages) != 1 ||
			client.RemovedImages[0].Ref != "agents-sandbox/runner-active-1mjusbm3wikhb0:digestOld" {
			t.Errorf("RemovedImages = %v, want only digestOld", client.RemovedImages)
		}
	})

	t.Run("reclaims images of a slug that has neither VM nor state", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				img("agents-sandbox/runner-unknown-1mjusbm3wikhb0:digest1", old),
				img("agents-sandbox/runner-unknown-1mjusbm3wikhb0:digest2", old),
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
				img("agents-sandbox/runner-fail-1mjusbm3wikhb0:digest1", old),
				img("agents-sandbox/runner-ok-1mjusbm3wikhb0:digest2", old),
			},
			ImageRemoveFn: func(_ context.Context, ref string, _ bool) error {
				if ref == "agents-sandbox/runner-fail-1mjusbm3wikhb0:digest1" {
					return errors.New("remove boom")
				}
				removed[ref] = true
				return nil
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ui := &termio.Mock{}
		r, err := PruneImages(context.Background(), PruneState{ToPrune: map[state.Key]msb.SandboxHandle{
			{Slug: "fail-1mjusbm3wikhb0", Agent: ""}: &msb.MockSandboxHandle{},
			{Slug: "ok-1mjusbm3wikhb0", Agent: ""}:   &msb.MockSandboxHandle{},
		}}, false, ui)
		if err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if r.MSBImagesPruned != 1 {
			t.Errorf("MSBImagesPruned = %d, want 1 (failed one is skipped)", r.MSBImagesPruned)
		}
		if !removed["agents-sandbox/runner-ok-1mjusbm3wikhb0:digest2"] {
			t.Errorf("expected the non-failing image to be removed, got %v", removed)
		}
		if len(ui.WarnCalls) != 1 {
			t.Errorf("WarnCalls = %v, want 1 warn about the failed removal", ui.WarnCalls)
		}
	})

	t.Run("docker prune error is warned but not fatal", func(t *testing.T) {
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{img("agents-sandbox/runner-dockererr-1mjusbm3wikhb0:digest1", old)},
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

func TestPruneImagesSkipsNonRunnerReferences(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "other/namespace:latest"},
			&msb.MockImageHandle{Reference_: ""},
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
		t.Errorf("MSBImagesPruned = %d, want 0 (non-runner refs skipped)", r.MSBImagesPruned)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none", client.RemovedImages)
	}
}

// TestPruneImagesSkipsHashlessSingleSegmentImage pins the ownership boundary on
// the prune path (#383): a single-segment image in the private runtime home that
// lacks the launcher's 14-character slug hash — a manually loaded image — is
// never considered, let alone removed. Prune, list and remove must agree on
// ownership (naming.IsRunnerImageRef), so the documented "never arbitrary cached
// images" guarantee is global.
func TestPruneImagesSkipsHashlessSingleSegmentImage(t *testing.T) {
	for _, ref := range []string{"manually-loaded:latest", "agents-sandbox/runner-manual:latest"} {
		t.Run(ref, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			client := &msb.MockMsbClient{
				Images: []msb.ImageHandle{
					&msb.MockImageHandle{
						Reference_:  ref,
						CreatedAt_:  time.Now().Add(-30 * 24 * time.Hour),
						LastUsedAt_: time.Now().Add(-30 * 24 * time.Hour),
					},
				},
			}
			msb.WithMsbMock(t, client)
			docker.WithNoopDockerMock(t)
			r, err := PruneImages(context.Background(), PruneState{Age: time.Hour}, false, &termio.Mock{})
			if err != nil {
				t.Fatalf("PruneImages: %v", err)
			}
			if r.MSBImagesPruned != 0 {
				t.Errorf("MSBImagesPruned = %d, want 0 (hashless image is foreign)", r.MSBImagesPruned)
			}
			if len(client.RemovedImages) != 0 {
				t.Errorf("RemovedImages = %v, want none", client.RemovedImages)
			}
		})
	}
}

func TestKeepImageNotReferencedWhenKeyAlsoPruned(t *testing.T) {
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	pruneState := PruneState{
		ToKeep:  map[state.Key]msb.SandboxHandle{key: &msb.MockSandboxHandle{}},
		ToPrune: map[state.Key]msb.SandboxHandle{key: &msb.MockSandboxHandle{}},
	}
	handle := &msb.MockImageHandle{Reference_: "agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest"}
	if keepImageFor(handle, pruneState, nil) {
		t.Error("expected a key present in both ToKeep and ToPrune to be treated as not referenced")
	}
}

// TestKeepImageDropsLatestSuffixWithoutAgent pins that the per-agent "-latest"
// rule needs an agent: a ref that only has the suffix (so it parses with an
// empty agent) is not a live per-agent tag and must be reclaimed.
func TestKeepImageDropsLatestSuffixWithoutAgent(t *testing.T) {
	const ref = "proj-1mjusbm3wikhb0-latest"
	pruneState := PruneState{ToKeep: map[state.Key]msb.SandboxHandle{
		{Slug: ref, Agent: ""}: &msb.MockSandboxHandle{},
	}}
	if keepImageFor(&msb.MockImageHandle{Reference_: ref}, pruneState, nil) {
		t.Error("a -latest ref with no parsed agent must not be kept")
	}
}

// TestPruneImagesKeepsLiveProjectImageWithLeftoverTask is the end-to-end form
// of the Greptile P1: a leftover stopped task sandbox for a live project must
// not cause the project's current runner image to be reclaimed.
func TestPruneImagesKeepsLiveProjectImageWithLeftoverTask(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "proj-1mjusbm3wikhb0"
	live := &msb.MockSandboxHandle{
		Name_:      slug + "-opencode",
		Status_:    msbSdk.SandboxStatusRunning,
		UpdatedAt_: time.Now(),
		Image_:     slug + ":opencode-latest",
	}
	task := &msb.MockSandboxHandle{
		Name_:      slug + "-opencode-1729000000000000000",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now(),
	}
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{live, task},
		Images:    []msb.ImageHandle{&msb.MockImageHandle{Reference_: slug + ":opencode-latest"}},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)

	pruneState, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	r, err := PruneImages(context.Background(), pruneState, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 0 || len(client.RemovedImages) != 0 {
		t.Errorf(
			"a leftover task reclaimed the live project's image: pruned=%d removed=%v",
			r.MSBImagesPruned,
			client.RemovedImages,
		)
	}
}

// TestPruneImagesKeepsRecentImageOfVmLessProject is the build-then-run
// regression: a prebuild loads a project's runner image while it still has no
// VM, and the run's own startup prune must not reclaim it. Both the content ref
// and the legacy prefixed tag survive while within the prune age.
func TestPruneImagesKeepsRecentImageOfVmLessProject(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "built-1mjusbm3wikhb0"
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_: "agents-sandbox/runner-" + slug + ":opencode-latest",
				CreatedAt_: time.Now(),
			},
			&msb.MockImageHandle{Reference_: slug + ":opencode.9f2c1a7b4d6e0f31", CreatedAt_: time.Now()},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	pruneState, err := buildPruneState(context.Background(), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	r, err := PruneImages(context.Background(), pruneState, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 0 {
		t.Errorf("MSBImagesPruned = %d, want 0 (a recent VM-less project's image must survive)", r.MSBImagesPruned)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want none", client.RemovedImages)
	}
}

func TestRenderImageReport(t *testing.T) {
	ui := &termio.Mock{}
	RenderImageReport(ui, ImageReport{
		MSBImagesPruned:    2,
		DockerImagesPruned: 1,
		Details: []StaleEntry{
			{
				Type: StaleTypeMsbImage,
				Name: "agents-sandbox/runner-proj-1mjusbm3wikhb0:old",
				Slug: "proj-1mjusbm3wikhb0",
			},
			{
				Type: StaleTypeMsbImage,
				Name: "agents-sandbox/runner-live-1mjusbm3wikhb0:old",
				Slug: "live-1mjusbm3wikhb0",
			},
		},
	}, true)
	if len(ui.OutCalls) != 1 ||
		!strings.Contains(ui.OutCalls[0], "dry-run: Would prune 2 runner image(s), 1 dangling docker image(s)") {
		t.Errorf("OutCalls = %v, want the dry-run summary", ui.OutCalls)
	}
	if len(ui.VerboseCalls) != 2 {
		t.Fatalf("VerboseCalls = %v, want one line per detail", ui.VerboseCalls)
	}
	if !strings.Contains(ui.VerboseCalls[0], "agents-sandbox/runner-proj-1mjusbm3wikhb0:old") {
		t.Errorf("verbose detail missing image name: %q", ui.VerboseCalls[0])
	}

	empty := &termio.Mock{}
	RenderImageReport(empty, ImageReport{}, false)
	if len(empty.OutCalls) != 1 || empty.OutCalls[0] != "Pruned 0 runner image(s), 0 dangling docker image(s)" {
		t.Errorf("OutCalls = %v, want the empty summary", empty.OutCalls)
	}
	if len(empty.VerboseCalls) != 0 {
		t.Errorf("VerboseCalls = %v, want none without details", empty.VerboseCalls)
	}
}

func TestPruneImagesRunsNativePruneAfterReferenceCleanup(t *testing.T) {
	t.Run("normal prune", func(t *testing.T) {
		events := make([]string, 0, 2)
		client := &msb.MockMsbClient{
			Images: []msb.ImageHandle{
				&msb.MockImageHandle{Reference_: "agents-sandbox/runner-project-1mjusbm3wikhb0:old"},
			},
			ImageRemoveFn: func(_ context.Context, _ string, _ bool) error {
				events = append(events, "remove")
				return nil
			},
			ImagePruneFn: func(context.Context) (*msbSdk.ImagePruneReport, error) {
				events = append(events, "prune")
				return &msbSdk.ImagePruneReport{ManifestsRemoved: 1}, nil
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)

		if _, err := PruneImages(context.Background(), PruneState{}, false, &termio.Mock{}); err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if len(events) != 2 || events[0] != "remove" || events[1] != "prune" {
			t.Errorf("prune events = %v, want [remove prune]", events)
		}
	})

	t.Run("dry run", func(t *testing.T) {
		called := false
		client := &msb.MockMsbClient{
			ImagePruneFn: func(context.Context) (*msbSdk.ImagePruneReport, error) {
				called = true
				return &msbSdk.ImagePruneReport{}, nil
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)

		if _, err := PruneImages(context.Background(), PruneState{}, true, &termio.Mock{}); err != nil {
			t.Fatalf("PruneImages dry run: %v", err)
		}
		if called {
			t.Error("dry-run image prune must not invoke native image prune")
		}
	})

	t.Run("native prune error", func(t *testing.T) {
		client := &msb.MockMsbClient{
			ImagePruneFn: func(context.Context) (*msbSdk.ImagePruneReport, error) {
				return nil, errors.New("native prune failed")
			},
		}
		msb.WithMsbMock(t, client)
		docker.WithNoopDockerMock(t)
		ui := &termio.Mock{}

		if _, err := PruneImages(context.Background(), PruneState{}, false, ui); err != nil {
			t.Fatalf("PruneImages: %v", err)
		}
		if len(ui.WarnCalls) != 1 || !strings.Contains(ui.WarnCalls[0], "native prune failed") {
			t.Errorf("WarnCalls = %v, want native prune warning", ui.WarnCalls)
		}
	})
}

// TestPruneImagesSkipsNativePruneWhenRetaining pins the run-path guard: the
// native Image.Prune reclaims cached image data that no sandbox references, so
// it cannot honor RetainKeys. A prune that retains keys — a `run`/`shell`
// protecting the image it is about to load — must therefore not run it; the
// deferred data is reclaimed by a later prune that retains nothing.
func TestPruneImagesSkipsNativePruneWhenRetaining(t *testing.T) {
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	removed := false
	pruned := false
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31"},
		},
		ImageRemoveFn: func(_ context.Context, _ string, _ bool) error {
			removed = true
			return nil
		},
		ImagePruneFn: func(context.Context) (*msbSdk.ImagePruneReport, error) {
			pruned = true
			return &msbSdk.ImagePruneReport{ManifestsRemoved: 1}, nil
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)

	pruneState := PruneState{RetainKeys: map[state.Key]struct{}{key: {}}}
	if _, err := PruneImages(context.Background(), pruneState, false, &termio.Mock{}); err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if removed {
		t.Error("a ref whose key the invocation retains must not be removed")
	}
	if pruned {
		t.Error("native Image.Prune must not run when the invocation retains image keys")
	}
}

// TestKeepImagePerAgent verifies that image retention is keyed per (slug, agent).
func TestKeepImagePerAgent(t *testing.T) {
	// The per-agent tag for a kept (slug, opencode) is kept while it is recent,
	// while the same project's pi tag is not retained when pi has no kept VM.
	pruneState := PruneState{
		Age: time.Hour,
		ToKeep: map[state.Key]msb.SandboxHandle{
			{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}: &msb.MockSandboxHandle{Name_: "v"},
		},
		ToPrune: map[state.Key]msb.SandboxHandle{},
	}
	oc := &msb.MockImageHandle{
		Reference_:  "agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest",
		LastUsedAt_: time.Now(),
	}
	if !keepImageFor(oc, pruneState, nil) {
		t.Error("expected opencode-latest to be kept")
	}
	pi := &msb.MockImageHandle{Reference_: "agents-sandbox/runner-proj-1mjusbm3wikhb0:pi-latest"}
	if keepImageFor(pi, pruneState, nil) {
		t.Error("expected pi-latest to be pruned (no kept pi VM)")
	}
	// A prefix-free ref of the live (slug, agent) is kept the same way.
	prefixFree := &msb.MockImageHandle{
		Reference_:  "proj-1mjusbm3wikhb0:opencode-latest",
		LastUsedAt_: time.Now(),
	}
	if !keepImageFor(prefixFree, pruneState, nil) {
		t.Error("expected a prefix-free opencode-latest ref to be kept")
	}
}

// TestKeepImageKeepsRefHeldByKeptSandboxRegardlessOfAge pins the first retention
// rule: a kept sandbox holds its exact content ref, so it survives any age — a
// running VM must never lose the image it is using.
func TestKeepImageKeepsRefHeldByKeptSandboxRegardlessOfAge(t *testing.T) {
	const ref = "proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31"
	pruneState := PruneState{
		Age: time.Hour,
		ToKeep: map[state.Key]msb.SandboxHandle{
			{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}: &msb.MockSandboxHandle{Image_: ref},
		},
	}
	handle := &msb.MockImageHandle{Reference_: ref, LastUsedAt_: time.Now().Add(-48 * time.Hour)}
	if !keepImageFor(handle, pruneState, map[string]struct{}{ref: {}}) {
		t.Error("expected a ref held by a kept sandbox to be kept regardless of age")
	}
}

// TestKeepImageKeepsRecentVariantOfLiveKey pins the second retention rule: a
// dead key's content ref is surplus, but a live key's ref used within the prune
// age is exactly the variant a toggling user reuses.
func TestKeepImageKeepsRecentVariantOfLiveKey(t *testing.T) {
	pruneState := PruneState{
		Age: time.Hour,
		ToKeep: map[state.Key]msb.SandboxHandle{
			{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}: &msb.MockSandboxHandle{},
		},
	}
	handle := &msb.MockImageHandle{
		Reference_:  "proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31",
		LastUsedAt_: time.Now().Add(-time.Minute),
	}
	if !keepImageFor(handle, pruneState, nil) {
		t.Error("expected a variant of a live key used within the age to be kept")
	}
}

// TestKeepImageDropsAgedVariantOfLiveKey bounds a live project's cache: a
// variant not used within the window is reclaimed even while the project lives.
func TestKeepImageDropsAgedVariantOfLiveKey(t *testing.T) {
	pruneState := PruneState{
		Age: 24 * time.Hour,
		ToKeep: map[state.Key]msb.SandboxHandle{
			{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}: &msb.MockSandboxHandle{},
		},
	}
	handle := &msb.MockImageHandle{
		Reference_:  "proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31",
		LastUsedAt_: time.Now().Add(-48 * time.Hour),
	}
	if keepImageFor(handle, pruneState, nil) {
		t.Error("expected an aged variant of a live key to be pruned")
	}
}

// TestKeepImageKeepsRecentRefOfVmLessKey pins the retention rule that lets a
// build be reused by the next run: a project with no VM is not "dead" for its
// images, so a ref used within the prune age survives the run's own startup
// prune instead of being reclaimed and reimported.
func TestKeepImageKeepsRecentRefOfVmLessKey(t *testing.T) {
	pruneState := PruneState{Age: time.Hour}
	handle := &msb.MockImageHandle{
		Reference_:  "built-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31",
		LastUsedAt_: time.Now(),
	}
	if !keepImageFor(handle, pruneState, nil) {
		t.Error("expected a recent ref of a VM-less key to be kept")
	}
}

// TestKeepImageDropsAgedRefOfVmLessKey bounds the same rule: a VM-less key's ref
// is still reclaimed once it ages out, so a retired project's images do not
// linger past the prune window.
func TestKeepImageDropsAgedRefOfVmLessKey(t *testing.T) {
	pruneState := PruneState{Age: time.Hour}
	handle := &msb.MockImageHandle{
		Reference_:  "gone-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31",
		LastUsedAt_: time.Now().Add(-48 * time.Hour),
	}
	if keepImageFor(handle, pruneState, nil) {
		t.Error("expected an aged ref of a VM-less key to be pruned")
	}
}

// TestKeepImageRetainsCurrentRunKey pins the current-run exemption: the key the
// invocation is about to use keeps its image even when its project VM is being
// pruned in the same pass and the image has aged out, so a run never destroys
// the image it is about to import.
func TestKeepImageRetainsCurrentRunKey(t *testing.T) {
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	pruneState := PruneState{
		Age:        time.Hour,
		ToPrune:    map[state.Key]msb.SandboxHandle{key: &msb.MockSandboxHandle{}},
		RetainKeys: map[state.Key]struct{}{key: {}},
	}
	handle := &msb.MockImageHandle{
		Reference_:  "agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest",
		LastUsedAt_: time.Now().Add(-48 * time.Hour),
	}
	if !keepImageFor(handle, pruneState, nil) {
		t.Error("a key retained by the current run must keep its image")
	}
}

// TestKeepImageHasNoRecencyProtectionWhenAgeZero pins the guard: a zero prune
// age disables the recency rule entirely, so only refs held by a kept sandbox
// survive.
func TestKeepImageHasNoRecencyProtectionWhenAgeZero(t *testing.T) {
	pruneState := PruneState{
		ToKeep: map[state.Key]msb.SandboxHandle{
			{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}: &msb.MockSandboxHandle{},
		},
	}
	handle := &msb.MockImageHandle{
		Reference_:  "proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31",
		LastUsedAt_: time.Now(),
	}
	if keepImageFor(handle, pruneState, nil) {
		t.Error("a zero prune age must not keep a variant by recency")
	}
}

func TestImageWithinAge(t *testing.T) {
	now := time.Now()
	ref := "proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31"

	t.Run("zero age never protects", func(t *testing.T) {
		handle := &msb.MockImageHandle{Reference_: ref, LastUsedAt_: now}
		if imageWithinAge(handle, 0) {
			t.Error("age 0 must not count an image as within age")
		}
	})

	t.Run("last used within age", func(t *testing.T) {
		handle := &msb.MockImageHandle{Reference_: ref, LastUsedAt_: now.Add(-time.Minute)}
		if !imageWithinAge(handle, time.Hour) {
			t.Error("expected an image used a minute ago to be within a one-hour age")
		}
	})

	t.Run("falls back to created at when last used is zero", func(t *testing.T) {
		handle := &msb.MockImageHandle{Reference_: ref, CreatedAt_: now.Add(-time.Minute)}
		if !imageWithinAge(handle, time.Hour) {
			t.Error("expected CreatedAt to stand in for a zero LastUsedAt")
		}
	})

	t.Run("aged image is outside the window", func(t *testing.T) {
		handle := &msb.MockImageHandle{Reference_: ref, LastUsedAt_: now.Add(-48 * time.Hour)}
		if imageWithinAge(handle, 24*time.Hour) {
			t.Error("expected an image last used 48h ago to be outside a 24h age")
		}
	})

	t.Run("zero timestamps are not within age", func(t *testing.T) {
		handle := &msb.MockImageHandle{Reference_: ref}
		if imageWithinAge(handle, 24*time.Hour) {
			t.Error("an image with no timestamps must not count as within age")
		}
	})

	t.Run("a future timestamp is within any positive age", func(t *testing.T) {
		// A future LastUsedAt makes time.Since negative, so it is within even the
		// smallest positive age and pins the strict comparison downstream.
		handle := &msb.MockImageHandle{Reference_: ref, LastUsedAt_: now.Add(time.Hour)}
		if !imageWithinAge(handle, time.Nanosecond) {
			t.Error("a future-dated image must count as within a positive age")
		}
	})

	t.Run("a non-positive age short-circuits before any timestamp", func(t *testing.T) {
		// Even a future-dated image is unprotected when the age disables the rule.
		handle := &msb.MockImageHandle{Reference_: ref, LastUsedAt_: now.Add(time.Hour)}
		if imageWithinAge(handle, 0) {
			t.Error("a zero age must not keep even a future-dated image")
		}
	})
}

// TestKeepImageDropsAgentlessRefOfLiveKey pins that a ref that parses without an
// agent is not a live per-agent tag, so a live key's fresh agent-less ref is
// reclaimed rather than protected by recency.
func TestKeepImageDropsAgentlessRefOfLiveKey(t *testing.T) {
	const ref = "proj-1mjusbm3wikhb0-latest"
	pruneState := PruneState{
		Age: time.Hour,
		ToKeep: map[state.Key]msb.SandboxHandle{
			{Slug: ref, Agent: ""}: &msb.MockSandboxHandle{},
		},
	}
	handle := &msb.MockImageHandle{Reference_: ref, LastUsedAt_: time.Now()}
	if keepImageFor(handle, pruneState, nil) {
		t.Error("an agent-less ref of a live key must not be kept even when fresh")
	}
}

// TestPruneImagesKeepsPerAgentTagsOfLiveSlug verifies that a live slug keeps
// exactly its recent per-agent refs and reclaims every other (digest) ref.
func TestPruneImagesKeepsPerAgentTagsOfLiveSlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	now := time.Now()
	old := time.Now().Add(-15 * 24 * time.Hour)
	older := time.Now().Add(-30 * 24 * time.Hour)
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_: "agents-sandbox/runner-active-1mjusbm3wikhb0:opencode-latest",
				CreatedAt_: old,
			},
			&msb.MockImageHandle{
				Reference_: "agents-sandbox/runner-active-1mjusbm3wikhb0:pi-latest",
				CreatedAt_: now,
			},
			&msb.MockImageHandle{
				Reference_: "agents-sandbox/runner-active-1mjusbm3wikhb0:olddigest",
				CreatedAt_: older,
			},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	ps := PruneState{Age: 30 * 24 * time.Hour, ToKeep: map[state.Key]msb.SandboxHandle{
		{Slug: "active-1mjusbm3wikhb0", Agent: "opencode"}: &msb.MockSandboxHandle{},
		{Slug: "active-1mjusbm3wikhb0", Agent: "pi"}:       &msb.MockSandboxHandle{},
	}}
	r, err := PruneImages(context.Background(), ps, false, ui)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 1 {
		t.Errorf("MSBImagesPruned = %d, want 1 (reclaim non -latest ref)", r.MSBImagesPruned)
	}
	if len(client.RemovedImages) != 1 ||
		client.RemovedImages[0].Ref != "agents-sandbox/runner-active-1mjusbm3wikhb0:olddigest" {
		t.Errorf("RemovedImages = %v, want only the non -latest ref", client.RemovedImages)
	}
}

// TestPruneImagesPreservesFreshSurplus reproduces the "imported msb image pruned
// after the user quit" bug: a fresh runner image was loaded into microsandbox
// but, because the user quit at the image-change home-volume prompt, its digest
// was never recorded in the state file. A live slug must keep its recent
// per-agent refs and reclaim only the surplus digest ref, so the next run does
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
				Reference_: "agents-sandbox/runner-active-1mjusbm3wikhb0:opencode-latest",
				CreatedAt_: old,
			},
			// A freshly imported replacement for another agent.
			&msb.MockImageHandle{
				Reference_: "agents-sandbox/runner-active-1mjusbm3wikhb0:pi-latest",
				CreatedAt_: now,
			},
			// A genuinely old surplus digest: reclaimed.
			&msb.MockImageHandle{
				Reference_: "agents-sandbox/runner-active-1mjusbm3wikhb0:digestOld",
				CreatedAt_: older,
			},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	ps := PruneState{Age: 30 * 24 * time.Hour, ToKeep: map[state.Key]msb.SandboxHandle{
		{Slug: "active-1mjusbm3wikhb0", Agent: "opencode"}: &msb.MockSandboxHandle{},
		{Slug: "active-1mjusbm3wikhb0", Agent: "pi"}:       &msb.MockSandboxHandle{},
	}}
	r, err := PruneImages(context.Background(), ps, false, ui)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 1 {
		t.Errorf("MSBImagesPruned = %d, want 1 (only the old surplus digest)", r.MSBImagesPruned)
	}
	if len(client.RemovedImages) != 1 ||
		client.RemovedImages[0].Ref != "agents-sandbox/runner-active-1mjusbm3wikhb0:digestOld" {
		t.Errorf("RemovedImages = %v, want only the old surplus digest kept fresh image", client.RemovedImages)
	}
}

// TestPruneImagesDryRunReportsAgedVariants covers the variant report: a live
// project's cached variants survive while used within the prune age; an aged one
// is reclaimed, and the dry-run report names which content id went.
func TestPruneImagesDryRunReportsAgedVariants(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "proj-1mjusbm3wikhb0"
	heldRef := slug + ":opencode.9f2c1a7b4d6e0f31"
	recentRef := slug + ":opencode.1122334455667788"
	agedRef := slug + ":opencode.0011223344556677"
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: heldRef, LastUsedAt_: time.Now().Add(-72 * time.Hour)},
			&msb.MockImageHandle{Reference_: recentRef, LastUsedAt_: time.Now().Add(-time.Minute)},
			&msb.MockImageHandle{Reference_: agedRef, LastUsedAt_: time.Now().Add(-48 * time.Hour)},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	pruneState := PruneState{
		Age: 24 * time.Hour,
		ToKeep: map[state.Key]msb.SandboxHandle{
			{Slug: slug, Agent: "opencode"}: &msb.MockSandboxHandle{Image_: heldRef},
		},
	}
	r, err := PruneImages(context.Background(), pruneState, true, &termio.Mock{})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 1 {
		t.Errorf("MSBImagesPruned = %d, want 1 (only the aged variant)", r.MSBImagesPruned)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("dry run removed images: %v", client.RemovedImages)
	}
	if len(r.Details) != 1 || r.Details[0].Name != agedRef {
		t.Fatalf("Details = %v, want only the aged variant", r.Details)
	}
	if r.Details[0].Digest != "0011223344556677" {
		t.Errorf("Details[0].Digest = %q, want the removed variant's content id", r.Details[0].Digest)
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
				Reference_: "agents-sandbox/runner-gone-1mjusbm3wikhb0:opencode-latest",
				CreatedAt_: old,
			},
			&msb.MockImageHandle{Reference_: "agents-sandbox/runner-gone-1mjusbm3wikhb0:old", CreatedAt_: old},
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

// TestPruneImagesKeepsImageReferencedByKeptVM reproduces the prune warning
// "failed to remove msb image ... FOREIGN KEY constraint failed": a kept
// (running or not-yet-stale stopped) VM may reference its runner image by a
// digest ref rather than a per-agent "-latest" tag (pre-redesign VMs). Such an
// image is still in use by the sandbox and must not be pruned.
func TestPruneImagesKeepsImageReferencedByKeptVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_: "agents-sandbox/runner-proj-1mjusbm3wikhb0:4e8oeqohbmalok",
				CreatedAt_: time.Now().Add(-15 * 24 * time.Hour),
			},
		},
	}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	ps := PruneState{
		ToKeep: map[state.Key]msb.SandboxHandle{
			{Slug: "proj-1mjusbm3wikhb0", Agent: ""}: &msb.MockSandboxHandle{
				Name_:   "agents-sandbox-vm-proj-1mjusbm3wikhb0",
				Status_: msbSdk.SandboxStatusStopped,
				Image_:  "agents-sandbox/runner-proj-1mjusbm3wikhb0:4e8oeqohbmalok",
			},
		},
	}
	r, err := PruneImages(context.Background(), ps, false, ui)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 0 {
		t.Errorf("MSBImagesPruned = %d, want 0 (image referenced by kept VM)", r.MSBImagesPruned)
	}
	if len(client.RemovedImages) != 0 {
		t.Errorf("RemovedImages = %v, want 0 (digest image of kept VM must not be removed)", client.RemovedImages)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("WarnCalls = %v, want 0", ui.WarnCalls)
	}
}
