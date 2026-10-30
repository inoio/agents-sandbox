package pruning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

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

	t.Run("removes state when the last unreferenced volume for slug is removed", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		slug := "stale-1mjusbm3wikhb0"
		// The state records no home volume, so it does not reference the volume
		// below and the reference guard lets the volume be reclaimed; the state
		// then has nothing left to protect and is removed too.
		if err := seedHomeState(
			state.Key{Slug: slug, Agent: ""},
			state.HomeState{},
		); err != nil {
			t.Fatalf("seedHomeState: %v", err)
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
		if err := seedHomeState(
			state.Key{Slug: slug, Agent: ""},
			state.HomeState{HomeVolume: "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"},
		); err != nil {
			t.Fatalf("seedHomeState: %v", err)
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

func TestPruneVolumesSkipsReferencedVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	old := time.Now().Add(-15 * 24 * time.Hour)
	const (
		slug   = "stale-1mjusbm3wikhb0"
		volume = "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"
	)
	key := state.Key{Slug: slug, Agent: ""}
	if err := seedHomeState(key, state.HomeState{HomeVolume: volume}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	client := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volume, CreatedAt_: old}},
	}
	msb.WithMsbMock(t, client)
	ui := &termio.Mock{}
	r, err := PruneVolumes(context.Background(), PruneState{
		ToPrune: map[state.Key]msb.SandboxHandle{key: &msb.MockSandboxHandle{}},
	}, false, ui)
	if err != nil {
		t.Fatalf("PruneVolumes: %v", err)
	}
	if r.VolumesPruned != 0 {
		t.Errorf("VolumesPruned = %d, want 0 (referenced volume kept)", r.VolumesPruned)
	}
	if len(r.Details) != 0 {
		t.Errorf("Details = %v, want none", r.Details)
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none", client.RemovedVolumes)
	}
	want := "keeping home volume " + volume + ": still referenced by " + slug
	if len(ui.WarnCalls) != 1 || ui.WarnCalls[0] != want {
		t.Errorf("WarnCalls = %v, want [%q]", ui.WarnCalls, want)
	}
	if _, err := state.ReadState(key); err != nil {
		t.Errorf("state file for %s should survive, got err=%v", slug, err)
	}
}

func TestPruneVolumesStillRemovesUnreferencedVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	old := time.Now().Add(-15 * 24 * time.Hour)
	const (
		referencedSlug   = "stale-1mjusbm3wikhb0"
		referencedVolume = "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"
		goneSlug         = "gone-1mjusbm3wikhb0"
		goneVolume       = "agents-sandbox-home-gone-1mjusbm3wikhb0-20260806T143022"
	)
	referencedKey := state.Key{Slug: referencedSlug, Agent: ""}
	if err := seedHomeState(referencedKey, state.HomeState{HomeVolume: referencedVolume}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	client := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: referencedVolume, CreatedAt_: old},
			&msb.MockVolumeHandle{Name_: goneVolume, CreatedAt_: old},
		},
	}
	msb.WithMsbMock(t, client)
	ui := &termio.Mock{}
	r, err := PruneVolumes(context.Background(), PruneState{
		ToPrune: map[state.Key]msb.SandboxHandle{
			referencedKey:               &msb.MockSandboxHandle{},
			{Slug: goneSlug, Agent: ""}: &msb.MockSandboxHandle{},
		},
	}, false, ui)
	if err != nil {
		t.Fatalf("PruneVolumes: %v", err)
	}
	if r.VolumesPruned != 1 {
		t.Errorf("VolumesPruned = %d, want 1 (only the unreferenced volume)", r.VolumesPruned)
	}
	if len(client.RemovedVolumes) != 1 || client.RemovedVolumes[0] != goneVolume {
		t.Errorf("RemovedVolumes = %v, want [%s]", client.RemovedVolumes, goneVolume)
	}
	if len(r.Details) != 1 || r.Details[0].Name != goneVolume {
		t.Errorf("Details = %v, want only %s", r.Details, goneVolume)
	}
	if _, err := state.ReadState(referencedKey); err != nil {
		t.Errorf("state file for %s should survive the prune, got err=%v", referencedSlug, err)
	}
}

// TestPruneVolumesKeepsLiveProjectVolumeWithLeftoverTask is the volume form of
// the Greptile P1: a leftover stopped task for a live project must not make the
// project's unrecorded (e.g. pre-reset) home volume prunable.
func TestPruneVolumesKeepsLiveProjectVolumeWithLeftoverTask(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const slug = "proj-1mjusbm3wikhb0"
	live := &msb.MockSandboxHandle{
		Name_:      slug + "-opencode",
		Status_:    msbSdk.SandboxStatusRunning,
		UpdatedAt_: time.Now(),
	}
	task := &msb.MockSandboxHandle{
		Name_:      slug + "-opencode-1729000000000000000",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now(),
	}
	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{live, task},
		Volumes:   []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: slug + "-opencode-20260806T143022"}},
	}
	msb.WithMsbMock(t, client)

	pruneState, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	r, err := PruneVolumes(context.Background(), pruneState, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("PruneVolumes: %v", err)
	}
	if r.VolumesPruned != 0 || len(client.RemovedVolumes) != 0 {
		t.Errorf(
			"a leftover task pruned the live project's volume: pruned=%d removed=%v",
			r.VolumesPruned,
			client.RemovedVolumes,
		)
	}
}

// TestPruneVolumesKeepsVolumeReferencedFromEitherSlug covers the migration
// window: while an adoption is pending the legacy repo-slug and the checkout-slug
// state directories both exist, and a home volume referenced from either must be
// kept. state.HomeVolumeReferences is slug-agnostic, so the guard must not
// assume the volume's own slug is the only one that can reference it.
func TestPruneVolumesKeepsVolumeReferencedFromEitherSlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const (
		legacySlug   = "legacyrepo-1mjusbm3wikhb0"
		checkoutSlug = "checkoutrepo-2abcd"
		volume       = "agents-sandbox-home-legacyrepo-1mjusbm3wikhb0-20260806T143022"
	)
	for _, slug := range []string{legacySlug, checkoutSlug} {
		if err := seedHomeState(state.Key{Slug: slug, Agent: ""}, state.HomeState{HomeVolume: volume}); err != nil {
			t.Fatalf("seedHomeState(%s): %v", slug, err)
		}
	}
	client := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: volume, CreatedAt_: time.Now().Add(-15 * 24 * time.Hour)},
		},
	}
	msb.WithMsbMock(t, client)
	ui := &termio.Mock{}
	r, err := PruneVolumes(context.Background(), PruneState{
		ToPrune: map[state.Key]msb.SandboxHandle{
			{Slug: legacySlug, Agent: ""}: &msb.MockSandboxHandle{},
		},
	}, false, ui)
	if err != nil {
		t.Fatalf("PruneVolumes: %v", err)
	}
	if r.VolumesPruned != 0 || len(client.RemovedVolumes) != 0 {
		t.Errorf("pruned %d volume(s), removed %v; want the volume kept", r.VolumesPruned, client.RemovedVolumes)
	}
	want := "keeping home volume " + volume + ": still referenced by " + checkoutSlug + ", " + legacySlug
	if len(ui.WarnCalls) != 1 || ui.WarnCalls[0] != want {
		t.Errorf("WarnCalls = %v, want [%q]", ui.WarnCalls, want)
	}
	for _, slug := range []string{legacySlug, checkoutSlug} {
		if _, err := state.ReadState(state.Key{Slug: slug, Agent: ""}); err != nil {
			t.Errorf("state file for %s should survive, got err=%v", slug, err)
		}
	}
}

func TestPruneVolumesFailsClosedOnUnreadableState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const (
		slug   = "stale-1mjusbm3wikhb0"
		volume = "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"
	)
	dir := state.KeyDir(state.Key{Slug: slug, Agent: ""})
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.yaml"), []byte("home_volume: ["), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	client := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: volume, CreatedAt_: time.Now().Add(-15 * 24 * time.Hour)},
		},
	}
	msb.WithMsbMock(t, client)
	_, err := PruneVolumes(context.Background(), PruneState{
		ToPrune: map[state.Key]msb.SandboxHandle{{Slug: slug, Agent: ""}: &msb.MockSandboxHandle{}},
	}, false, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "scan project state references") {
		t.Fatalf("PruneVolumes() error = %v, want a fail-closed reference scan error", err)
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none removed on a reference scan error", client.RemovedVolumes)
	}
}

// staleHomeHandle returns a home volume handle old enough for prune to treat as
// stale. Tests only.
func staleHomeHandle(name string) msb.VolumeHandle {
	return &msb.MockVolumeHandle{Name_: name, CreatedAt_: time.Now().Add(-15 * 24 * time.Hour)}
}

func TestPruneVolumesKeepsStateWhenReferencedVolumeSiblingRemoved(t *testing.T) {
	// The guard holds back the referenced volume, so removed[key] == 1 while
	// exists[key] == 2, and the state names the surviving referenced volume.
	// removeStateForGoneKeys must not delete the state and orphan the referenced
	// volume for the next prune to reclaim: the count guard skips the key, and
	// the state-content guard would keep it anyway.
	configpaths.WithMockConfigPaths(t)
	const (
		slug       = "stale-1mjusbm3wikhb0"
		referenced = "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"
		sibling    = "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143023"
	)
	if err := seedHomeState(
		state.Key{Slug: slug, Agent: ""},
		state.HomeState{HomeVolume: referenced},
	); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	client := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{staleHomeHandle(referenced), staleHomeHandle(sibling)},
	}
	msb.WithMsbMock(t, client)
	ui := &termio.Mock{}
	r, err := PruneVolumes(context.Background(), PruneState{
		ToPrune: map[state.Key]msb.SandboxHandle{{Slug: slug, Agent: ""}: &msb.MockSandboxHandle{}},
	}, false, ui)
	if err != nil {
		t.Fatalf("PruneVolumes: %v", err)
	}
	if r.VolumesPruned != 1 || len(r.Details) != 1 || r.Details[0].Name != sibling {
		t.Errorf("report = %+v, want only %s pruned", r, sibling)
	}
	if len(client.RemovedVolumes) != 1 || client.RemovedVolumes[0] != sibling {
		t.Errorf("RemovedVolumes = %v, want [%s]", client.RemovedVolumes, sibling)
	}
	if _, err := state.ReadState(state.Key{Slug: slug, Agent: ""}); err != nil {
		t.Errorf(
			"state file for %s should survive while referenced volume %s remains, got err=%v",
			slug,
			referenced,
			err,
		)
	}
}

func TestPruneVolumesDryRunWarnsOnReferencedVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const (
		slug       = "stale-1mjusbm3wikhb0"
		referenced = "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"
		sibling    = "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143023"
	)
	if err := seedHomeState(
		state.Key{Slug: slug, Agent: ""},
		state.HomeState{HomeVolume: referenced},
	); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	client := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{staleHomeHandle(referenced), staleHomeHandle(sibling)},
	}
	msb.WithMsbMock(t, client)
	ui := &termio.Mock{}
	r, err := PruneVolumes(context.Background(), PruneState{
		ToPrune: map[state.Key]msb.SandboxHandle{{Slug: slug, Agent: ""}: &msb.MockSandboxHandle{}},
	}, true, ui)
	if err != nil {
		t.Fatalf("PruneVolumes: %v", err)
	}
	if r.VolumesPruned != 1 || len(r.Details) != 1 || r.Details[0].Name != sibling {
		t.Errorf("report = %+v, want only %s counted as would-prune", r, sibling)
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("dry run removed volumes: %v", client.RemovedVolumes)
	}
	want := "keeping home volume " + referenced + ": still referenced by " + slug
	if len(ui.WarnCalls) != 1 || ui.WarnCalls[0] != want {
		t.Errorf("WarnCalls = %v, want [%q]", ui.WarnCalls, want)
	}
}

// TestCompareStateKey pins the ordering contract: slug first, then agent. Every
// branch must return a sign, since the stable iteration of removeStateForGoneKeys
// depends on it.
func TestCompareStateKey(t *testing.T) {
	a := state.Key{Slug: "a-1mjusbm3wikhb0", Agent: "opencode"}
	b := state.Key{Slug: "b-1mjusbm3wikhb0", Agent: "opencode"}
	c := state.Key{Slug: "a-1mjusbm3wikhb0", Agent: "pi"}

	if got := compareStateKey(a, b); got >= 0 {
		t.Errorf("compareStateKey(a, b) = %d, want < 0 (slug order)", got)
	}
	if got := compareStateKey(b, a); got <= 0 {
		t.Errorf("compareStateKey(b, a) = %d, want > 0 (slug order)", got)
	}
	if got := compareStateKey(a, c); got >= 0 {
		t.Errorf("compareStateKey(a, c) = %d, want < 0 (agent order, same slug)", got)
	}
	if got := compareStateKey(c, a); got <= 0 {
		t.Errorf("compareStateKey(c, a) = %d, want > 0 (agent order, same slug)", got)
	}
	if got := compareStateKey(a, a); got != 0 {
		t.Errorf("compareStateKey(a, a) = %d, want 0", got)
	}
}

func TestRenderVolumeReport(t *testing.T) {
	ui := &termio.Mock{}
	RenderVolumeReport(ui, VolumeReport{
		VolumesPruned: 1,
		Details: []StaleEntry{
			{
				Type: StaleTypeVolume,
				Name: "agents-sandbox-home-proj-1mjusbm3wikhb0-20260806T143022",
				Slug: "proj-1mjusbm3wikhb0",
			},
		},
	}, false)
	if len(ui.OutCalls) != 1 || ui.OutCalls[0] != "Pruned 1 home volume(s)" {
		t.Errorf("OutCalls = %v, want the summary", ui.OutCalls)
	}
	if len(ui.VerboseCalls) != 1 || !strings.Contains(ui.VerboseCalls[0], "proj-1mjusbm3wikhb0") {
		t.Errorf("VerboseCalls = %v, want the detail line", ui.VerboseCalls)
	}

	empty := &termio.Mock{}
	RenderVolumeReport(empty, VolumeReport{}, true)
	if len(empty.OutCalls) != 1 || empty.OutCalls[0] != "dry-run: Would prune 0 home volume(s)" {
		t.Errorf("OutCalls = %v, want the dry-run summary", empty.OutCalls)
	}
	if len(empty.VerboseCalls) != 0 {
		t.Errorf("VerboseCalls = %v, want none without details", empty.VerboseCalls)
	}
}

// TestRemoveStateForGoneKeysProcessesKeysAfterASkippedOne pins that a key held
// back by the exists guard does not stop later keys from being processed: the
// iteration visits keys in a stable order, so "gone" is removed though "kept"
// is skipped.
// TestPruneVolumesKeepsStateCommittedAfterTheSnapshot reproduces #335
// deterministically: prune snapshots the volumes and references, sweeps, and
// only afterwards removes state. A concurrent run that commits a freshly created
// home volume to the state in that window must not have its state removed. The
// volume listing never contains the new volume, so the old removed >= exists
// decision deleted the state that named it and orphaned the volume.
func TestPruneVolumesKeepsStateCommittedAfterTheSnapshot(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const (
		slug          = "stale-1mjusbm3wikhb0"
		staleVolume   = "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143022"
		concurrentVol = "agents-sandbox-home-stale-1mjusbm3wikhb0-20260806T143099"
	)
	key := state.Key{Slug: slug, Agent: ""}
	// The snapshot sees a key whose state records no volume and one reclaimable
	// volume, exactly the shape the race begins from.
	if err := seedHomeState(key, state.HomeState{}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	client := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{staleHomeHandle(staleVolume)},
		RemoveVolumeFn: func(_ context.Context, name string) error {
			if name != staleVolume {
				return nil
			}
			// A concurrent run commits a fresh home volume to state after prune's
			// snapshots but before its state removal.
			return seedHomeState(key, state.HomeState{HomeVolume: concurrentVol})
		},
	}
	msb.WithMsbMock(t, client)
	ui := &termio.Mock{}
	r, err := PruneVolumes(context.Background(), PruneState{
		ToPrune: map[state.Key]msb.SandboxHandle{key: &msb.MockSandboxHandle{}},
	}, false, ui)
	if err != nil {
		t.Fatalf("PruneVolumes: %v", err)
	}
	if r.VolumesPruned != 1 || len(client.RemovedVolumes) != 1 || client.RemovedVolumes[0] != staleVolume {
		t.Fatalf("expected only the stale volume removed: report=%+v removed=%v", r, client.RemovedVolumes)
	}
	st, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("state committed after the snapshot must survive, got err=%v", err)
	}
	if st.HomeVolume != concurrentVol {
		t.Errorf("state HomeVolume = %q, want %q", st.HomeVolume, concurrentVol)
	}
}

func TestRemoveStateForGoneKeysProcessesKeysAfterASkippedOne(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	kept := state.Key{Slug: "aaaa-kept"}
	gone := state.Key{Slug: "bbbb-gone"}
	for _, k := range []state.Key{kept, gone} {
		if err := seedHomeState(k, state.HomeState{}); err != nil {
			t.Fatalf("seedHomeState(%v): %v", k, err)
		}
	}
	ui := &termio.Mock{}
	removeStateForGoneKeys(
		ui,
		map[state.Key]int{kept: 1, gone: 1},
		map[state.Key]int{kept: 2, gone: 0},
	)
	if _, err := state.ReadState(kept); err != nil {
		t.Errorf("kept key state must survive, got %v", err)
	}
	if _, err := state.ReadState(gone); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("gone key state must be removed, got %v", err)
	}
}

func TestRemoveStateForGoneKeysWarnsOnRemoveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "prunewarn-a"}
	// A non-empty directory named state.yaml makes the state unreadable, so
	// RemoveHomeStateIf returns an error and prune warns rather than discarding
	// a document it cannot parse.
	stateYAML := filepath.Join(state.KeyDir(k), "state.yaml")
	if err := os.MkdirAll(stateYAML, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateYAML, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ui := &termio.Mock{}
	removeStateForGoneKeys(ui, map[state.Key]int{k: 1}, nil)
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning when state removal fails")
	}
}
