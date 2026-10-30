package pruning

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	imagetypes "github.com/moby/moby/api/types/image"
	mobyclient "github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestAutoPruneDefaultThresholdLiteral(t *testing.T) {
	if autoPruneDefaultThreshold != 30*24*time.Hour {
		t.Errorf("autoPruneDefaultThreshold = %v, want 30 days", autoPruneDefaultThreshold)
	}
}

// TestAutoPruneZeroThresholdKeepsFreshStoppedVM pins the 0 -> default mapping: a
// zero threshold must behave as 30 days, keeping a freshly stopped VM.
func TestAutoPruneZeroThresholdKeepsFreshStoppedVM(t *testing.T) {
	autoPruneOnce = sync.Once{}
	configpaths.WithMockConfigPaths(t)
	fresh := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now(),
	}
	client := &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{fresh}}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	testUI := termio.NewTestMock(t)

	AutoPrune(context.Background(), 0, false, &testUI)

	if fresh.DidRemove() {
		t.Error("a zero threshold must default to 30 days and keep a fresh stopped VM")
	}
}

func TestAutoPruneRendersSummary(t *testing.T) {
	autoPruneOnce = sync.Once{}
	withAutoPruneMocks(t)
	testUI := termio.NewTestMock(t)

	AutoPrune(context.Background(), time.Hour, false, &testUI)

	if len(testUI.OutCalls) == 0 || !strings.Contains(testUI.OutCalls[0], "Pruned") {
		t.Errorf("OutCalls = %v, want a prune summary", testUI.OutCalls)
	}
}

// TestBuildPruneStateContinuesPastSkippedEntries pins that the loop keeps
// processing after an unparseable name and a task sandbox: a stale project VM
// after them must still be scheduled.
func TestBuildPruneStateContinuesPastSkippedEntries(t *testing.T) {
	stale := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-proj-1mjusbm3wikhb0",
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now().Add(-15 * 24 * time.Hour),
	}
	client := &msb.MockMsbClient{Sandboxes: []msb.SandboxHandle{
		&msb.MockSandboxHandle{
			Name_:      "agents-sandbox-vm-",
			Status_:    msbSdk.SandboxStatusStopped,
			UpdatedAt_: time.Now(),
		},
		&msb.MockSandboxHandle{
			Name_:      "agents-sandbox-task-fill-1mjusbm3wikhb0-opencode-1729",
			Status_:    msbSdk.SandboxStatusStopped,
			UpdatedAt_: time.Now(),
		},
		stale,
	}}
	msb.WithMsbMock(t, client)

	pruneState, err := buildPruneState(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("buildPruneState: %v", err)
	}
	if len(pruneState.ToPrune) != 1 {
		t.Fatalf("ToPrune = %v, want only the stale project VM", pruneState.ToPrune)
	}
	if _, ok := pruneState.ToPrune[state.Key{Slug: "proj-1mjusbm3wikhb0"}]; !ok {
		t.Errorf("stale VM missing from ToPrune: %v", pruneState.ToPrune)
	}
}

func TestCountVolumesByKeySkipsForeign(t *testing.T) {
	handles := []msb.VolumeHandle{
		&msb.MockVolumeHandle{Name_: "foreign-volume"},
		&msb.MockVolumeHandle{Name_: "agents-sandbox-home-proj-1mjusbm3wikhb0-20260812T123456"},
	}
	exists := countVolumesByKey(handles)
	if len(exists) != 1 {
		t.Fatalf("exists = %v, want only the parsed home volume key", exists)
	}
	if got := exists[state.Key{Slug: "proj-1mjusbm3wikhb0"}]; got != 1 {
		t.Errorf("exists[proj] = %d, want 1", got)
	}
}

// TestPruneHomeVolumesSkipsAndContinues pins that a foreign volume, a volume
// whose key is not being pruned, a still-referenced volume, and a failed
// removal each skip only themselves: a later prunable volume is still removed.
func TestPruneHomeVolumesSkipsAndContinues(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	old := time.Now().Add(-15 * 24 * time.Hour)
	vol := func(name string) msb.VolumeHandle {
		return &msb.MockVolumeHandle{Name_: name, CreatedAt_: old}
	}
	const (
		foreign = "foreign-volume"
		kept    = "agents-sandbox-home-kept-1mjusbm3wikhb0-20260812T123456"
		ref     = "agents-sandbox-home-ref-1mjusbm3wikhb0-20260812T123456"
		fail    = "agents-sandbox-home-fail-1mjusbm3wikhb0-20260812T123456"
		ok      = "agents-sandbox-home-ok-1mjusbm3wikhb0-20260812T123456"
	)
	handles := []msb.VolumeHandle{vol(foreign), vol(kept), vol(ref), vol(fail), vol(ok)}

	client := &msb.MockMsbClient{RemoveVolumeFn: func(_ context.Context, name string) error {
		if name == fail {
			return errors.New("boom")
		}
		return nil
	}}
	msb.WithMsbMock(t, client)

	pruneState := PruneState{ToPrune: map[state.Key]msb.SandboxHandle{
		{Slug: "ref-1mjusbm3wikhb0"}:  &msb.MockSandboxHandle{},
		{Slug: "fail-1mjusbm3wikhb0"}: &msb.MockSandboxHandle{},
		{Slug: "ok-1mjusbm3wikhb0"}:   &msb.MockSandboxHandle{},
	}}
	references := map[string][]state.Key{ref: {{Slug: "still-active"}}}
	ui := &termio.Mock{}
	report := &VolumeReport{}

	removed := pruneHomeVolumes(
		context.Background(), handles, pruneState, references, false, ui, report)

	if report.VolumesPruned != 1 {
		t.Errorf(
			"VolumesPruned = %d, want 1 (only the unreferenced, successfully removed volume)",
			report.VolumesPruned,
		)
	}
	if _, found := removed[state.Key{Slug: "ok-1mjusbm3wikhb0"}]; !found {
		t.Errorf("removed = %v, want the ok key", removed)
	}
	// The failing removal is attempted first, the ok volume second: both must be
	// attempted, so a break-on-error mutant would drop the ok call.
	if len(client.RemovedVolumes) != 2 || client.RemovedVolumes[1] != ok {
		t.Errorf("RemovedVolumes = %v, want the failing then the ok removal attempt", client.RemovedVolumes)
	}
	if !warnContains(ui.WarnCalls, "failed to remove home volume") {
		t.Errorf("WarnCalls = %v, want the removal failure warning", ui.WarnCalls)
	}
	if len(report.Details) != 1 || report.Details[0].Name != ok || report.Details[0].Slug != "ok-1mjusbm3wikhb0" {
		t.Errorf("Details = %v, want the ok volume entry", report.Details)
	}
}

// TestPruneHomeVolumesSkipsForeignEvenWithEmptyKey pins that a foreign volume is
// skipped before its (empty) key is looked up: an empty key in ToPrune must not
// turn it into a prune candidate.
func TestPruneHomeVolumesSkipsForeignEvenWithEmptyKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)
	handles := []msb.VolumeHandle{
		&msb.MockVolumeHandle{Name_: "foreign-volume", CreatedAt_: time.Now()},
	}
	pruneState := PruneState{ToPrune: map[state.Key]msb.SandboxHandle{
		{}: &msb.MockSandboxHandle{},
	}}
	report := &VolumeReport{}

	pruneHomeVolumes(context.Background(), handles, pruneState, nil, false, &termio.Mock{}, report)

	if report.VolumesPruned != 0 || len(client.RemovedVolumes) != 0 {
		t.Errorf("a foreign volume must be skipped: report=%+v removed=%v", report, client.RemovedVolumes)
	}
}

func TestRemoveStateForGoneKeysRemovesEveryKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	keys := []state.Key{
		{Slug: "a-1mjusbm3wikhb0"},
		{Slug: "b-1mjusbm3wikhb0"},
		{Slug: "c-1mjusbm3wikhb0"},
	}
	// A state that records no home volume is the one prune removes; the loop
	// must process every such key.
	for _, k := range keys {
		if err := seedHomeState(k, state.HomeState{}); err != nil {
			t.Fatalf("seedHomeState: %v", err)
		}
	}
	removed := map[state.Key]int{}
	for _, k := range keys {
		removed[k] = 1
	}

	removeStateForGoneKeys(&termio.Mock{}, removed, map[state.Key]int{})

	for _, k := range keys {
		if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
			t.Errorf("state for %v should be removed, got err=%v", k, err)
		}
	}
}

// TestRemoveStateForGoneKeysKeepsStateNamingAVolume pins the #335 guard at the
// unit level: even when the count guard would remove the state, a state that
// names a home volume is kept, so a concurrently created volume is never
// orphaned.
func TestRemoveStateForGoneKeysKeepsStateNamingAVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	kept := state.Key{Slug: "names-a-volume"}
	if err := seedHomeState(kept, state.HomeState{HomeVolume: "vol-1"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	removeStateForGoneKeys(&termio.Mock{}, map[state.Key]int{kept: 1}, map[state.Key]int{kept: 1})

	if _, err := state.ReadState(kept); err != nil {
		t.Errorf("state naming a home volume must survive, got err=%v", err)
	}
}

// TestRemoveStateForGoneKeysKeepsStateNamingARecoveredVolume pins that a pending
// recovered-volume marker (#144) protects the state just as a settled home
// volume does: the state names a volume the project still expects to recover, so
// discarding it would lose the marker.
func TestRemoveStateForGoneKeysKeepsStateNamingARecoveredVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	kept := state.Key{Slug: "pending-recovery"}
	if err := seedHomeState(kept, state.HomeState{RecoveredHomeVolume: "vol-pending"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	removeStateForGoneKeys(&termio.Mock{}, map[state.Key]int{kept: 1}, map[state.Key]int{kept: 1})

	if _, err := state.ReadState(kept); err != nil {
		t.Errorf("state naming a pending recovered volume must survive, got err=%v", err)
	}
}

func TestRenderPruneSummaryDryRunAction(t *testing.T) {
	dry := &termio.Mock{}
	RenderPruneSummary(dry, &StaleReport{PrunedSandboxes: 1}, true)
	if len(dry.OutCalls) != 1 || !strings.HasPrefix(dry.OutCalls[0], "dry-run: Would prune") {
		t.Errorf("OutCalls = %v, want the dry-run action", dry.OutCalls)
	}

	rendered := &termio.Mock{}
	RenderPruneSummary(rendered, &StaleReport{}, false)
	if len(rendered.OutCalls) != 1 || !strings.HasPrefix(rendered.OutCalls[0], "Pruned") {
		t.Errorf("OutCalls = %v, want the real action", rendered.OutCalls)
	}
}

func TestPruneSandboxesVerboseAndDetails(t *testing.T) {
	replaced := stoppedHandle("agents-sandbox-vm-proj1-1mjusbm3wikhb0")
	replaced.RemoveErr = &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"}
	ok := stoppedHandle("agents-sandbox-vm-proj2-1mjusbm3wikhb0")

	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)
	ui := &termio.Mock{}
	pruneState := PruneState{ToPrune: map[state.Key]msb.SandboxHandle{
		{Slug: "proj1-1mjusbm3wikhb0"}: replaced,
		{Slug: "proj2-1mjusbm3wikhb0"}: ok,
	}}

	r, err := PruneSandboxes(context.Background(), pruneState, false, ui)
	if err != nil {
		t.Fatalf("PruneSandboxes: %v", err)
	}
	if len(ui.VerboseCalls) != 1 || !strings.Contains(ui.VerboseCalls[0], "already replaced or removed") {
		t.Errorf("VerboseCalls = %v, want the replaced/removed skip line", ui.VerboseCalls)
	}
	if len(r.Details) != 1 || r.Details[0].Name != ok.Name_ || r.Details[0].Slug != "proj2-1mjusbm3wikhb0" {
		t.Errorf("Details = %v, want the pruned sandbox entry", r.Details)
	}
}

func TestPruneImagesSkipsForeignAndReports(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{Images: []msb.ImageHandle{
		&msb.MockImageHandle{Reference_: "docker.io/library/foreign:1"},
		&msb.MockImageHandle{Reference_: "agents-sandbox/runner-orphan-1mjusbm3wikhb0:digest1"},
	}}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	pruneState := PruneState{ToPrune: map[state.Key]msb.SandboxHandle{
		{Slug: "orphan-1mjusbm3wikhb0"}: &msb.MockSandboxHandle{},
	}}

	r, err := PruneImages(context.Background(), pruneState, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 1 {
		t.Errorf("MSBImagesPruned = %d, want 1 (the orphan, not the foreign ref)", r.MSBImagesPruned)
	}
	if len(r.Details) != 1 || r.Details[0].Name != "agents-sandbox/runner-orphan-1mjusbm3wikhb0:digest1" {
		t.Errorf("Details = %v, want the orphan entry", r.Details)
	}
	if len(r.Details) == 1 && r.Details[0].StaleFor != 0 {
		t.Errorf("Details[0].StaleFor = %v, want 0", r.Details[0].StaleFor)
	}
}

func TestPruneImagesErrorReturnsZeroReport(t *testing.T) {
	client := &msb.MockMsbClient{ImageListFn: func(context.Context) ([]msb.ImageHandle, error) {
		return nil, errors.New("list boom")
	}}
	msb.WithMsbMock(t, client)

	r, err := PruneImages(context.Background(), PruneState{}, false, &termio.Mock{})
	if err == nil {
		t.Fatal("expected an ImageList error")
	}
	if r.MSBImagesPruned != 0 || r.DockerImagesPruned != 0 {
		t.Errorf("report = %+v, want a zero report on error", r)
	}
}

// TestPruneImagesDockerCounts pins that the docker-image count from
// pruneDockerImages is written back into the report.
func TestPruneImagesDockerCounts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	docker.WithDockerMock(t, deletedImagesDockerMock(3))

	r, err := PruneImages(context.Background(), PruneState{}, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.DockerImagesPruned != 3 {
		t.Errorf("DockerImagesPruned = %d, want 3", r.DockerImagesPruned)
	}
}

// TestPruneImagesEmptyAgentLatestTagIsSurplus pins that a "-latest" reference
// with no agent (tag exactly "-latest") is not kept for a live key.
func TestPruneImagesEmptyAgentLatestTagIsSurplus(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{Images: []msb.ImageHandle{
		&msb.MockImageHandle{Reference_: "agents-sandbox/runner-active-1mjusbm3wikhb0:-latest"},
	}}
	msb.WithMsbMock(t, client)
	docker.WithNoopDockerMock(t)
	pruneState := PruneState{ToKeep: map[state.Key]msb.SandboxHandle{
		{Slug: "active-1mjusbm3wikhb0"}: &msb.MockSandboxHandle{},
	}}

	r, err := PruneImages(context.Background(), pruneState, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if r.MSBImagesPruned != 1 {
		t.Errorf("MSBImagesPruned = %d, want 1 (empty-agent -latest tag is surplus)", r.MSBImagesPruned)
	}
}

func TestPruneDockerImagesDryRunReturnsZero(t *testing.T) {
	docker.WithDockerMock(t, deletedImagesDockerMock(3))
	if n := pruneDockerImages(context.Background(), true, &termio.Mock{}); n != 0 {
		t.Errorf("dry-run pruneDockerImages = %d, want 0", n)
	}
	if n := pruneDockerImages(context.Background(), false, &termio.Mock{}); n != 3 {
		t.Errorf("pruneDockerImages = %d, want 3", n)
	}
}

// deletedImagesDockerMock returns a docker mock whose ImagePrune reports n
// deleted images.
func deletedImagesDockerMock(n int) *docker.MockDockerClient {
	return &docker.MockDockerClient{
		ImagePruneFn: func(context.Context, mobyclient.ImagePruneOptions) (mobyclient.ImagePruneResult, error) {
			return mobyclient.ImagePruneResult{
				Report: imagetypes.PruneReport{ImagesDeleted: make([]imagetypes.DeleteResponse, n)},
			}, nil
		},
	}
}

func warnContains(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
