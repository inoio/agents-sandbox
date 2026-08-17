package pruning

import (
	"context"
	"errors"
	"testing"
	"time"

	mobyImage "github.com/moby/moby/api/types/image"
	moby "github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	cp "gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
)

// mockDockerClient provides per-call error injection for ImageRemove while
// delegating all other methods to the embedded MockDockerClient.
type mockDockerClient struct {
	docker.MockDockerClient

	removedImages []string
	removeErr     error
	perCallErrs   []error
	callCounter   int

	pruneErr     error
	prunedImages int
	pruneCalled  bool
}

func (m *mockDockerClient) ImageRemove(
	_ context.Context,
	imageID string,
	_ moby.ImageRemoveOptions,
) (moby.ImageRemoveResult, error) {
	// Check per-call errors first.
	if m.callCounter < len(m.perCallErrs) {
		err := m.perCallErrs[m.callCounter]
		m.callCounter++
		if err != nil {
			return moby.ImageRemoveResult{}, err
		}
		m.removedImages = append(m.removedImages, imageID)
		return moby.ImageRemoveResult{}, nil
	}
	// Fall back to global removeErr (checked first to maintain legacy behavior).
	if m.removeErr != nil {
		return moby.ImageRemoveResult{}, m.removeErr
	}
	m.removedImages = append(m.removedImages, imageID)
	return moby.ImageRemoveResult{}, nil
}

func (m *mockDockerClient) ImagePrune(_ context.Context, _ moby.ImagePruneOptions) (moby.ImagePruneResult, error) {
	m.pruneCalled = true
	if m.pruneErr != nil {
		return moby.ImagePruneResult{}, m.pruneErr
	}
	return moby.ImagePruneResult{Report: mobyImage.PruneReport{
		ImagesDeleted: make([]mobyImage.DeleteResponse, m.prunedImages),
	}}, nil
}

func newMockUI() *termio.Mock {
	return &termio.Mock{}
}

// pruneThreshold is the threshold used by tests that exercise age-gated removal.
const pruneThreshold = time.Hour

// ancient returns a timestamp far enough in the past that artifacts carrying it
// are never considered recent for the pruneThreshold used in tests.
func ancient() time.Time {
	return time.Now().Add(-2 * time.Hour)
}

// oldVol returns a home volume old enough to be pruned.
func oldVol(name string) volumeWithAge {
	return volumeWithAge{name: name, createdAt: ancient()}
}

// recentVol returns a home volume too young to be pruned.
func recentVol(name string) volumeWithAge {
	return volumeWithAge{name: name, createdAt: time.Now()}
}

// prunedCounts holds the expected pruned counts for all resource types.
type prunedCounts struct {
	vms, volumes, dockerImages, msbImages, taskSandboxes, cloneVolumes int
}

// assertReport checks that all pruned counts match expected values.
func assertReport(t *testing.T, report *StaleReport, want prunedCounts) {
	t.Helper()
	if report.PrunedVMs != want.vms {
		t.Errorf("PrunedVMs = %d, want %d", report.PrunedVMs, want.vms)
	}
	if report.PrunedVolumes != want.volumes {
		t.Errorf("PrunedVolumes = %d, want %d", report.PrunedVolumes, want.volumes)
	}
	if report.PrunedDockerImages != want.dockerImages {
		t.Errorf("PrunedDockerImages = %d, want %d", report.PrunedDockerImages, want.dockerImages)
	}
	if report.PrunedMSBImages != want.msbImages {
		t.Errorf("PrunedMSBImages = %d, want %d", report.PrunedMSBImages, want.msbImages)
	}
	if report.PrunedTaskSandboxes != want.taskSandboxes {
		t.Errorf("PrunedTaskSandboxes = %d, want %d", report.PrunedTaskSandboxes, want.taskSandboxes)
	}
	if report.PrunedCloneVolumes != want.cloneVolumes {
		t.Errorf("PrunedCloneVolumes = %d, want %d", report.PrunedCloneVolumes, want.cloneVolumes)
	}
}

func TestPruneStaleCascade_RemovesVMAndAllArtifacts(t *testing.T) {
	client, _, ui, report := setupPruningFixtures(t)

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-sandbox-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-sandbox-home-myproject-digest1"), oldVol("opencode-sandbox-home-myproject")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-sandbox/runner-myproject:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()},
			{ref: "opencode-sandbox/runner-myproject:latest", digest: "", isLatest: true, lastUsed: ancient()},
		},
	}

	pruneStaleCascade(
		context.Background(),
		client,
		entry,
		pruneThreshold,
		homeBySlugDigest,
		msbImagesBySlug,
		false,
		ui,
		report,
	)

	assertReport(t, report, prunedCounts{vms: 1, volumes: 2, msbImages: 2})

	wantRemoved := []string{entry.Name}
	if len(client.RemovedSandboxes) != 1 || client.RemovedSandboxes[0] != entry.Name {
		t.Errorf("removed sandboxes = %v, want %v", client.RemovedSandboxes, wantRemoved)
	}

	wantVolumes := []string{"opencode-sandbox-home-myproject-digest1", "opencode-sandbox-home-myproject"}
	if len(client.RemovedVolumes) != len(wantVolumes) {
		t.Errorf("removed volumes = %v, want %v", client.RemovedVolumes, wantVolumes)
	}

	wantMSBImages := []string{"opencode-sandbox/runner-myproject:digest1", "opencode-sandbox/runner-myproject:latest"}
	if len(client.RemovedImages) != len(wantMSBImages) {
		t.Errorf("removed msb images count = %d, want %d", len(client.RemovedImages), len(wantMSBImages))
	}
}

func TestPruneStaleCascade_DryRunDoesNotDelete(t *testing.T) {
	client, dockerMock, ui, report := setupPruningFixtures(t)

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-sandbox-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-sandbox-home-myproject-digest1")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-sandbox/runner-myproject:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()},
		},
	}

	pruneStaleCascade(
		context.Background(),
		client,
		entry,
		pruneThreshold,
		homeBySlugDigest,
		msbImagesBySlug,
		true,
		ui,
		report,
	)

	assertReport(t, report, prunedCounts{vms: 1, volumes: 1, msbImages: 1})

	total := len(client.RemovedSandboxes) + len(client.RemovedVolumes) +
		len(client.RemovedImages) + len(dockerMock.removedImages)
	if total != 0 {
		t.Errorf("expected no deletion calls in dry run, got sandboxes=%v volumes=%v images=%v docker=%v",
			client.RemovedSandboxes, client.RemovedVolumes, client.RemovedImages, dockerMock.removedImages)
	}
}

func TestPruneStaleCascade_RemoveErrorWarnsAndStopsCascade(t *testing.T) {
	client, dockerMock, ui, report := setupPruningFixtures(t)
	client.RemoveSandboxFn = func(_ context.Context, _ string) error { return errors.New("sandbox locked") }
	slug := "myproject"
	entry := StaleEntry{Name: "opencode-sandbox-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-sandbox-home-myproject-digest1")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-sandbox/runner-myproject:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()},
		},
	}

	pruneStaleCascade(
		context.Background(),
		client,
		entry,
		pruneThreshold,
		homeBySlugDigest,
		msbImagesBySlug,
		false,
		ui,
		report,
	)

	if report.PrunedVMs != 0 {
		t.Errorf("PrunedVMs = %d, want 0", report.PrunedVMs)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning on sandbox removal error")
	}
	if len(client.RemovedVolumes)+len(client.RemovedImages)+len(dockerMock.removedImages) != 0 {
		t.Error("expected cascade to stop after sandbox removal failure")
	}
}

func TestPruneActiveVMCleanup_KeepsMatchingDigestAndLatest(t *testing.T) {
	client, _, ui, report := setupPruningFixtures(t)

	slug := "myproject"
	activeDigest := "digest2"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {
			oldVol("opencode-sandbox-home-myproject-digest1"),
			oldVol("opencode-sandbox-home-myproject-digest2"),
			oldVol("opencode-sandbox-home-myproject"),
		},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-sandbox/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-sandbox/runner-myproject:digest2", digest: "digest2", isLatest: false},
			{ref: "opencode-sandbox/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneActiveVMCleanup(
		context.Background(), client, slug, activeDigest,
		homeBySlugDigest, msbImagesBySlug, false, ui, report,
	)

	assertReport(t, report, prunedCounts{msbImages: 1})

	if len(client.RemovedImages) != 1 || client.RemovedImages[0].Ref != "opencode-sandbox/runner-myproject:digest1" {
		t.Errorf("removed msb images = %v, want [opencode-sandbox/runner-myproject:digest1]", client.RemovedImages)
	}
}

func TestPruneActiveVMCleanup_DryRunCountsButDoesNotDelete(t *testing.T) {
	client, dockerMock, ui, report := setupPruningFixtures(t)

	slug := "myproject"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-sandbox-home-myproject-digest1"), oldVol("opencode-sandbox-home-myproject-digest2")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-sandbox/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-sandbox/runner-myproject:digest2", digest: "digest2", isLatest: false},
		},
	}

	pruneActiveVMCleanup(
		context.Background(), client, slug, "digest2",
		homeBySlugDigest, msbImagesBySlug, true, ui, report,
	)

	if report.PrunedVolumes != 0 || report.PrunedMSBImages != 1 || report.PrunedDockerImages != 0 {
		t.Errorf("unexpected report counts: volumes=%d msb=%d docker=%d",
			report.PrunedVolumes, report.PrunedMSBImages, report.PrunedDockerImages)
	}
	if len(client.RemovedVolumes)+len(client.RemovedImages)+len(dockerMock.removedImages) != 0 {
		t.Error("expected no deletion calls in dry run")
	}
}

func TestPruneOrphanSlug_RemovesEverything(t *testing.T) {
	client, _, ui, report := setupPruningFixtures(t)

	slug := "orphan"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-sandbox-home-orphan-digest1"), oldVol("opencode-sandbox-home-orphan")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-sandbox/runner-orphan:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()},
			{ref: "opencode-sandbox/runner-orphan:latest", digest: "", isLatest: true, lastUsed: ancient()},
		},
	}

	pruneOrphanSlug(
		context.Background(),
		client,
		slug,
		pruneThreshold,
		homeBySlugDigest,
		msbImagesBySlug,
		report,
		false,
		ui,
	)

	assertReport(t, report, prunedCounts{volumes: 2, msbImages: 2})

	if len(client.RemovedVolumes) != 2 {
		t.Errorf("removed volumes = %v, want 2", client.RemovedVolumes)
	}
	if len(client.RemovedImages) != 2 {
		t.Errorf("removed msb images = %v, want 2", client.RemovedImages)
	}
}

func TestPruneOrphanSlug_KeepsRecentArtifacts(t *testing.T) {
	client, dockerMock, ui, report := setupPruningFixtures(t)

	slug := "orphan"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {recentVol("opencode-sandbox-home-orphan-digest1")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-sandbox/runner-orphan:digest1", digest: "digest1", isLatest: false, lastUsed: time.Now()},
			{ref: "opencode-sandbox/runner-orphan:latest", digest: "", isLatest: true, lastUsed: time.Now()},
		},
	}

	pruneOrphanSlug(
		context.Background(),
		client,
		slug,
		pruneThreshold,
		homeBySlugDigest,
		msbImagesBySlug,
		report,
		false,
		ui,
	)

	if report.PrunedVolumes+report.PrunedMSBImages+report.PrunedDockerImages != 0 {
		t.Errorf("recent artifacts should not be pruned, got %+v", report)
	}
	if len(client.RemovedVolumes)+len(client.RemovedImages)+len(dockerMock.removedImages) != 0 {
		t.Errorf("recent artifacts should not be removed, got volumes=%v msb=%v docker=%v",
			client.RemovedVolumes, client.RemovedImages, dockerMock.removedImages)
	}
}

func TestPruneOrphanSlug_MixedAges_PrunesOnlyOld(t *testing.T) {
	client, _, ui, report := setupPruningFixtures(t)

	slug := "orphan"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-sandbox-home-orphan-old"), recentVol("opencode-sandbox-home-orphan-recent")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-sandbox/runner-orphan:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()},
			{ref: "opencode-sandbox/runner-orphan:latest", digest: "", isLatest: true, lastUsed: time.Now()},
		},
	}

	pruneOrphanSlug(
		context.Background(),
		client,
		slug,
		pruneThreshold,
		homeBySlugDigest,
		msbImagesBySlug,
		report,
		false,
		ui,
	)

	assertReport(t, report, prunedCounts{volumes: 1, msbImages: 1})

	if len(client.RemovedVolumes) != 1 || client.RemovedVolumes[0] != "opencode-sandbox-home-orphan-old" {
		t.Errorf("removed volumes = %v, want only [opencode-sandbox-home-orphan-old]", client.RemovedVolumes)
	}
	if len(client.RemovedImages) != 1 || client.RemovedImages[0].Ref != "opencode-sandbox/runner-orphan:digest1" {
		t.Errorf("removed msb images = %v, want only digest1", client.RemovedImages)
	}
}

func TestPrune_WithMocks_CoversAllCases(t *testing.T) {
	cp.WithMockConfigPaths(t)
	oldTime := time.Now().Add(-2 * time.Hour)
	recentTime := time.Now().Add(-5 * time.Minute)

	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// Stale VM for myproject -> cascade removes everything.
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-myproject-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: oldTime,
			},
			// Active VM for activeproject -> cleanup non-matching digests.
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-activeproject-1mjusbm3wikhb0-main",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: recentTime,
				Image_:     "opencode-sandbox/runner-activeproject-1mjusbm3wikhb0:digest2",
			},
			// Task sandbox -> always pruned.
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-task-fill-proj",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: oldTime,
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{
				Name_:      "opencode-sandbox-home-myproject-1mjusbm3wikhb0-digest1",
				CreatedAt_: ancient(),
			},
			&msb.MockVolumeHandle{
				Name_:      "opencode-sandbox-home-activeproject-1mjusbm3wikhb0-digest1",
				CreatedAt_: ancient(),
			},
			&msb.MockVolumeHandle{
				Name_:      "opencode-sandbox-home-activeproject-1mjusbm3wikhb0-digest2",
				CreatedAt_: ancient(),
			},
			&msb.MockVolumeHandle{
				Name_:      "opencode-sandbox-clone-myproject-1mjusbm3wikhb0-abc123",
				CreatedAt_: ancient(),
			},
			&msb.MockVolumeHandle{
				Name_:      "opencode-sandbox-clone-activeproject-1mjusbm3wikhb0-def456",
				CreatedAt_: ancient(),
			},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_:  "opencode-sandbox/runner-myproject-1mjusbm3wikhb0:digest1",
				LastUsedAt_: ancient(),
			},
			&msb.MockImageHandle{
				Reference_:  "opencode-sandbox/runner-activeproject-1mjusbm3wikhb0:digest1",
				LastUsedAt_: ancient(),
			},
			&msb.MockImageHandle{
				Reference_:  "opencode-sandbox/runner-activeproject-1mjusbm3wikhb0:digest2",
				LastUsedAt_: ancient(),
			},
			&msb.MockImageHandle{Reference_: "opencode-sandbox/runner-orphan:latest", LastUsedAt_: ancient()},
		},
	}
	docker.WithNoopDockerMock(t)
	ui := newMockUI()

	cp.WithMockConfigPaths(t)
	if err := state.WriteState("activeproject-1mjusbm3wikhb0", state.HomeState{
		HomeVolume: "opencode-sandbox-home-activeproject-1mjusbm3wikhb0-digest2",
	}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	oldGet := msb.Get
	msb.Get = func() MsbClient { return client }
	defer func() { msb.Get = oldGet }()

	report, err := catalogAndPrune(context.Background(), 30*time.Minute, false, ui)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}

	assertReport(
		t,
		report,
		prunedCounts{vms: 1, taskSandboxes: 1, volumes: 2, cloneVolumes: 1, msbImages: 3, dockerImages: 0},
	)
}

// TestPrune_StoppedRecentVM_PreservesImage verifies that a stopped-but-not-yet
// stale VM is not misclassified as an orphan: its currently used image digest
// and the :latest tag must survive, and only surplus digests are pruned.
func TestPrune_StoppedRecentVM_PreservesImage(t *testing.T) {
	recentTime := time.Now().Add(-5 * time.Minute)
	slug := "commonproj-1mjusbm3wikhb0"

	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// Stopped but recent: within threshold, must be preserved.
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-commonproj-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: recentTime,
				Image_:     "opencode-sandbox/runner-commonproj-1mjusbm3wikhb0:digestCur",
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "opencode-sandbox-home-commonproj-1mjusbm3wikhb0-20260810T120000"},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "opencode-sandbox/runner-commonproj-1mjusbm3wikhb0:digestCur"},
			&msb.MockImageHandle{Reference_: "opencode-sandbox/runner-commonproj-1mjusbm3wikhb0:digestOld"},
			&msb.MockImageHandle{Reference_: "opencode-sandbox/runner-commonproj-1mjusbm3wikhb0:latest"},
		},
	}
	docker.WithNoopDockerMock(t)
	ui := newMockUI()

	cp.WithMockConfigPaths(t)
	if err := state.WriteState(slug, state.HomeState{
		HomeVolume: "opencode-sandbox-home-commonproj-1mjusbm3wikhb0-20260810T120000",
	}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	oldGet := msb.Get
	msb.Get = func() MsbClient { return client }
	defer func() { msb.Get = oldGet }()

	report, err := catalogAndPrune(context.Background(), 30*time.Minute, false, ui)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}

	// Surplus digestOld is pruned (msb only); digestCur and :latest survive.
	assertReport(t, report, prunedCounts{volumes: 0, msbImages: 1, dockerImages: 0})
}

func TestPruneDockerImages_ErrorWarns(t *testing.T) {
	_, dockerMock, ui, report := setupPruningFixtures(t)
	dockerMock.pruneErr = errors.New("image prune failed")

	pruneDockerImages(context.Background(), false, ui, report)

	// No images should be counted as pruned when the prune call fails.
	if report.PrunedDockerImages != 0 {
		t.Errorf("PrunedDockerImages = %d, want 0", report.PrunedDockerImages)
	}
	if !dockerMock.pruneCalled {
		t.Error("expected ImagePrune to be called")
	}
	// There should be a warn message about the failed prune.
	if len(ui.WarnCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
}

func TestPruneDockerImages_CountsDeleted(t *testing.T) {
	_, dockerMock, ui, report := setupPruningFixtures(t)
	dockerMock.prunedImages = 3

	pruneDockerImages(context.Background(), false, ui, report)

	if report.PrunedDockerImages != 3 {
		t.Errorf("PrunedDockerImages = %d, want 3", report.PrunedDockerImages)
	}
	if !dockerMock.pruneCalled {
		t.Error("expected ImagePrune to be called")
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("WarnCalls = %d, want 0, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
}

func TestPruneDockerImages_DryRunSkips(t *testing.T) {
	_, dockerMock, ui, report := setupPruningFixtures(t)
	dockerMock.prunedImages = 3

	pruneDockerImages(context.Background(), true, ui, report)

	if report.PrunedDockerImages != 0 {
		t.Errorf("PrunedDockerImages = %d, want 0 in dry run", report.PrunedDockerImages)
	}
	if dockerMock.pruneCalled {
		t.Error("expected ImagePrune not to be called in dry run")
	}
}

func TestPruneCloneVolumes_DryRunDoesNotDelete(t *testing.T) {
	cp.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "opencode-sandbox-clone-orphanproj-abc123"},
		},
	}
	docker.WithNoopDockerMock(t)
	ui := newMockUI()

	oldGet := msb.Get
	msb.Get = func() MsbClient { return client }
	defer func() { msb.Get = oldGet }()

	report, err := catalogAndPrune(context.Background(), 30*time.Minute, true, ui)
	if err != nil {
		t.Fatalf("catalogAndPrune returned error: %v", err)
	}

	assertReport(t, report, prunedCounts{cloneVolumes: 1})

	if len(client.RemovedVolumes) != 0 {
		t.Errorf("expected no RemoveVolume calls in dry run, got %v", client.RemovedVolumes)
	}
}

func TestPrune_DockerPruneFails_PartialReport(t *testing.T) {
	cp.WithMockConfigPaths(t)
	oldTime := time.Now().Add(-2 * time.Hour)

	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// Stale VM for myproject -> cascade removes everything.
			&msb.MockSandboxHandle{
				Name_:      "opencode-sandbox-vm-myproject-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: oldTime,
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "opencode-sandbox-home-myproject-1mjusbm3wikhb0-digest1", CreatedAt_: oldTime},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_:  "opencode-sandbox/runner-myproject-1mjusbm3wikhb0:digest1",
				LastUsedAt_: oldTime,
			},
			&msb.MockImageHandle{
				Reference_:  "opencode-sandbox/runner-myproject-1mjusbm3wikhb0:latest",
				LastUsedAt_: oldTime,
			},
		},
	}

	dockerMock := &mockDockerClient{
		pruneErr: errors.New("prune failed"),
	}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()

	oldGet := msb.Get
	msb.Get = func() MsbClient { return client }
	defer func() { msb.Get = oldGet }()

	report, err := catalogAndPrune(context.Background(), 30*time.Minute, false, ui)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}

	// VM, volumes, and msb images are pruned; the failed docker prune counts nothing.
	assertReport(t, report, prunedCounts{vms: 1, volumes: 1, msbImages: 2, dockerImages: 0})
	// There should be a warn message about the failed docker prune.
	if len(ui.WarnCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
	if len(ui.VerboseCalls) != 0 {
		t.Errorf("VerboseCalls = %d, want 0, got %v", len(ui.VerboseCalls), ui.VerboseCalls)
	}
}
