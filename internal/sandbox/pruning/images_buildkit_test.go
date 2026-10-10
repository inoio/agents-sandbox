package pruning

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestPruneImagesCallsBuilderPrune(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	calls := 0
	restore := replaceBuilderCachePruner(t, func(context.Context) error {
		calls++
		return nil
	})
	defer restore()

	ui := &termio.Mock{}
	r, err := PruneImages(context.Background(), PruneState{}, false, ui)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if calls != 1 {
		t.Errorf("builder cache prune invoked %d times, want 1", calls)
	}
	if !r.BuildkitCachePruned {
		t.Error("BuildkitCachePruned = false, want true after a successful prune")
	}
}

func TestPruneImagesWarnsButContinuesOnBuilderPruneError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	restore := replaceBuilderCachePruner(t, func(context.Context) error {
		return errors.New("buildctl boom")
	})
	defer restore()

	ui := &termio.Mock{}
	r, err := PruneImages(context.Background(), PruneState{}, false, ui)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.BuildkitCachePruned {
		t.Error("BuildkitCachePruned = true, want false on error")
	}
	if len(ui.WarnCalls) == 0 {
		t.Errorf("expected a warn about the builder cache prune failure, got %v", ui.WarnCalls)
	}
}

func TestPruneImagesSkipsBuilderPruneOnDryRun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	calls := 0
	restore := replaceBuilderCachePruner(t, func(context.Context) error {
		calls++
		return nil
	})
	defer restore()

	ui := &termio.Mock{}
	r, err := PruneImages(context.Background(), PruneState{}, true, ui)
	if err != nil {
		t.Fatalf("PruneImages dry run: %v", err)
	}
	if calls != 0 {
		t.Errorf("builder cache prune invoked %d times on dry run, want 0", calls)
	}
	if r.BuildkitCachePruned {
		t.Error("BuildkitCachePruned = true on dry run, want false")
	}
}

// replaceBuilderCachePruner overrides the package-level builder cache prune seam
// for the duration of a test, restoring the previous implementation afterwards.
func replaceBuilderCachePruner(t *testing.T, fn func(context.Context) error) func() {
	t.Helper()
	original := pruneBuilderCache
	pruneBuilderCache = fn
	t.Cleanup(func() { pruneBuilderCache = original })
	return func() { pruneBuilderCache = original }
}
