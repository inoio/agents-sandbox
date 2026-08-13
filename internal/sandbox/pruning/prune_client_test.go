package pruning

import (
	"context"
	"errors"
	"testing"
	"time"

	moby "github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	cp "gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"
)

// mockDockerClient provides per-call error injection for ImageRemove while
// delegating all other methods to the embedded MockDockerClient.
type mockDockerClient struct {
	docker.MockDockerClient

	removedImages []string
	removeErr     error
	perCallErrs   []error
	callCounter   int
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
	cp.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-msb-home-myproject-digest1"), oldVol("opencode-msb-home-myproject")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true, lastUsed: ancient()},
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

	assertReport(t, report, prunedCounts{vms: 1, volumes: 2, msbImages: 2, dockerImages: 2})

	wantRemoved := []string{entry.Name}
	if len(client.RemovedSandboxes) != 1 || client.RemovedSandboxes[0] != entry.Name {
		t.Errorf("removed sandboxes = %v, want %v", client.RemovedSandboxes, wantRemoved)
	}

	wantVolumes := []string{"opencode-msb-home-myproject-digest1", "opencode-msb-home-myproject"}
	if len(client.RemovedVolumes) != len(wantVolumes) {
		t.Errorf("removed volumes = %v, want %v", client.RemovedVolumes, wantVolumes)
	}

	wantMSBImages := []string{"opencode-msb/runner-myproject:digest1", "opencode-msb/runner-myproject:latest"}
	if len(client.RemovedImages) != len(wantMSBImages) {
		t.Errorf("removed msb images count = %d, want %d", len(client.RemovedImages), len(wantMSBImages))
	}

	wantDockerImages := []string{"opencode-msb/runner-myproject:digest1", "opencode-msb/runner-myproject:latest"}
	if len(dockerMock.removedImages) != len(wantDockerImages) {
		t.Errorf("removed docker images = %v, want %v", dockerMock.removedImages, wantDockerImages)
	}
}

func TestPruneStaleCascade_DryRunDoesNotDelete(t *testing.T) {
	client := &msb.MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-msb-home-myproject-digest1")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()}},
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

	assertReport(t, report, prunedCounts{vms: 1, volumes: 1, msbImages: 1, dockerImages: 1})

	total := len(client.RemovedSandboxes) + len(client.RemovedVolumes) +
		len(client.RemovedImages) + len(dockerMock.removedImages)
	if total != 0 {
		t.Errorf("expected no deletion calls in dry run, got sandboxes=%v volumes=%v images=%v docker=%v",
			client.RemovedSandboxes, client.RemovedVolumes, client.RemovedImages, dockerMock.removedImages)
	}
}

func TestPruneStaleCascade_RemoveErrorWarnsAndStopsCascade(t *testing.T) {
	client := &msb.MockMsbClient{
		RemoveSandboxFn: func(_ context.Context, _ string) error { return errors.New("sandbox locked") },
	}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-msb-home-myproject-digest1")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()}},
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
	cp.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	activeDigest := "digest2"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {
			oldVol("opencode-msb-home-myproject-digest1"),
			oldVol("opencode-msb-home-myproject-digest2"),
			oldVol("opencode-msb-home-myproject"),
		},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:digest2", digest: "digest2", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneActiveVMCleanup(
		context.Background(), client, slug, activeDigest,
		homeBySlugDigest, msbImagesBySlug, false, ui, report,
	)

	assertReport(t, report, prunedCounts{msbImages: 1, dockerImages: 1})

	if len(client.RemovedImages) != 1 || client.RemovedImages[0].Ref != "opencode-msb/runner-myproject:digest1" {
		t.Errorf("removed msb images = %v, want [opencode-msb/runner-myproject:digest1]", client.RemovedImages)
	}

	if len(dockerMock.removedImages) != 1 || dockerMock.removedImages[0] != "opencode-msb/runner-myproject:digest1" {
		t.Errorf("removed docker images = %v, want [opencode-msb/runner-myproject:digest1]", dockerMock.removedImages)
	}
}

func TestPruneActiveVMCleanup_DryRunCountsButDoesNotDelete(t *testing.T) {
	cp.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-msb-home-myproject-digest1"), oldVol("opencode-msb-home-myproject-digest2")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:digest2", digest: "digest2", isLatest: false},
		},
	}

	pruneActiveVMCleanup(
		context.Background(), client, slug, "digest2",
		homeBySlugDigest, msbImagesBySlug, true, ui, report,
	)

	if report.PrunedVolumes != 0 || report.PrunedMSBImages != 1 || report.PrunedDockerImages != 1 {
		t.Errorf("unexpected report counts: volumes=%d msb=%d docker=%d",
			report.PrunedVolumes, report.PrunedMSBImages, report.PrunedDockerImages)
	}
	if len(client.RemovedVolumes)+len(client.RemovedImages)+len(dockerMock.removedImages) != 0 {
		t.Error("expected no deletion calls in dry run")
	}
}

func TestPruneOrphanSlug_RemovesEverything(t *testing.T) {
	cp.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "orphan"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-msb-home-orphan-digest1"), oldVol("opencode-msb-home-orphan")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-orphan:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()},
			{ref: "opencode-msb/runner-orphan:latest", digest: "", isLatest: true, lastUsed: ancient()},
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

	assertReport(t, report, prunedCounts{volumes: 2, msbImages: 2, dockerImages: 2})

	if len(client.RemovedVolumes) != 2 {
		t.Errorf("removed volumes = %v, want 2", client.RemovedVolumes)
	}
	if len(client.RemovedImages) != 2 {
		t.Errorf("removed msb images = %v, want 2", client.RemovedImages)
	}
	if len(dockerMock.removedImages) != 2 {
		t.Errorf("removed docker images = %v, want 2", dockerMock.removedImages)
	}
}

func TestPruneOrphanSlug_KeepsRecentArtifacts(t *testing.T) {
	cp.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "orphan"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {recentVol("opencode-msb-home-orphan-digest1")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-orphan:digest1", digest: "digest1", isLatest: false, lastUsed: time.Now()},
			{ref: "opencode-msb/runner-orphan:latest", digest: "", isLatest: true, lastUsed: time.Now()},
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
	cp.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "orphan"
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-msb-home-orphan-old"), recentVol("opencode-msb-home-orphan-recent")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-orphan:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()},
			{ref: "opencode-msb/runner-orphan:latest", digest: "", isLatest: true, lastUsed: time.Now()},
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

	assertReport(t, report, prunedCounts{volumes: 1, msbImages: 1, dockerImages: 1})

	if len(client.RemovedVolumes) != 1 || client.RemovedVolumes[0] != "opencode-msb-home-orphan-old" {
		t.Errorf("removed volumes = %v, want only [opencode-msb-home-orphan-old]", client.RemovedVolumes)
	}
	if len(client.RemovedImages) != 1 || client.RemovedImages[0].Ref != "opencode-msb/runner-orphan:digest1" {
		t.Errorf("removed msb images = %v, want only digest1", client.RemovedImages)
	}
	if len(dockerMock.removedImages) != 1 || dockerMock.removedImages[0] != "opencode-msb/runner-orphan:digest1" {
		t.Errorf("removed docker images = %v, want only digest1", dockerMock.removedImages)
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
				Name_:      "opencode-msb-vm-myproject-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: oldTime,
			},
			// Active VM for activeproject -> cleanup non-matching digests.
			&msb.MockSandboxHandle{
				Name_:      "opencode-msb-vm-activeproject-1mjusbm3wikhb0-main",
				Status_:    msbSdk.SandboxStatusRunning,
				UpdatedAt_: recentTime,
				Image_:     "opencode-msb/runner-activeproject-1mjusbm3wikhb0:digest2",
			},
			// Task sandbox -> always pruned.
			&msb.MockSandboxHandle{
				Name_:      "opencode-msb-task-fill-proj",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: oldTime,
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "opencode-msb-home-myproject-1mjusbm3wikhb0-digest1", CreatedAt_: ancient()},
			&msb.MockVolumeHandle{
				Name_:      "opencode-msb-home-activeproject-1mjusbm3wikhb0-digest1",
				CreatedAt_: ancient(),
			},
			&msb.MockVolumeHandle{
				Name_:      "opencode-msb-home-activeproject-1mjusbm3wikhb0-digest2",
				CreatedAt_: ancient(),
			},
			&msb.MockVolumeHandle{Name_: "opencode-msb-clone-myproject-1mjusbm3wikhb0-abc123", CreatedAt_: ancient()},
			&msb.MockVolumeHandle{
				Name_:      "opencode-msb-clone-activeproject-1mjusbm3wikhb0-def456",
				CreatedAt_: ancient(),
			},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_:  "opencode-msb/runner-myproject-1mjusbm3wikhb0:digest1",
				LastUsedAt_: ancient(),
			},
			&msb.MockImageHandle{
				Reference_:  "opencode-msb/runner-activeproject-1mjusbm3wikhb0:digest1",
				LastUsedAt_: ancient(),
			},
			&msb.MockImageHandle{
				Reference_:  "opencode-msb/runner-activeproject-1mjusbm3wikhb0:digest2",
				LastUsedAt_: ancient(),
			},
			&msb.MockImageHandle{Reference_: "opencode-msb/runner-orphan:latest", LastUsedAt_: ancient()},
		},
	}
	docker.WithNoopDockerMock(t)
	ui := newMockUI()

	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	if err := state.WriteState("activeproject-1mjusbm3wikhb0", state.HomeState{
		HomeVolume: "opencode-msb-home-activeproject-1mjusbm3wikhb0-digest2",
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
		prunedCounts{vms: 1, taskSandboxes: 1, volumes: 2, cloneVolumes: 1, msbImages: 3, dockerImages: 3},
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
				Name_:      "opencode-msb-vm-commonproj-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: recentTime,
				Image_:     "opencode-msb/runner-commonproj-1mjusbm3wikhb0:digestCur",
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "opencode-msb-home-commonproj-1mjusbm3wikhb0-20260810T120000"},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: "opencode-msb/runner-commonproj-1mjusbm3wikhb0:digestCur"},
			&msb.MockImageHandle{Reference_: "opencode-msb/runner-commonproj-1mjusbm3wikhb0:digestOld"},
			&msb.MockImageHandle{Reference_: "opencode-msb/runner-commonproj-1mjusbm3wikhb0:latest"},
		},
	}
	docker.WithNoopDockerMock(t)
	ui := newMockUI()

	state.SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	if err := state.WriteState(slug, state.HomeState{
		HomeVolume: "opencode-msb-home-commonproj-1mjusbm3wikhb0-20260810T120000",
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

	// Surplus digestOld is pruned (msb + docker); digestCur and :latest survive.
	assertReport(t, report, prunedCounts{volumes: 0, msbImages: 1, dockerImages: 1})
}

func TestPruneActiveVMDockerImages_AllFail_LogWarnings(t *testing.T) {
	dockerMock := &mockDockerClient{
		removeErr: errors.New("image does not exist"),
	}
	docker.WithDockerMock(t, dockerMock)
	report := &StaleReport{}
	ui := newMockUI()

	// Two images: digest1 is not :latest and != digest2, latest is skipped.
	slug := "myproject"
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneActiveVMDockerImages(context.Background(), slug, "digest2", msbImagesBySlug, false, ui, report)

	// No images should be counted as pruned when Docker removal always fails.
	if report.PrunedDockerImages != 0 {
		t.Errorf("PrunedDockerImages = %d, want 0", report.PrunedDockerImages)
	}
	// No images should appear in the removed list (all calls failed).
	if len(dockerMock.removedImages) != 0 {
		t.Errorf("removedImages = %v, want [] (all calls failed)", dockerMock.removedImages)
	}
	// There should be a warn message about the failed docker image.
	if len(ui.WarnCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
	if len(ui.VerboseCalls) != 0 {
		t.Errorf("VerboseCalls = %d, want 0, got %v", len(ui.VerboseCalls), ui.VerboseCalls)
	}
}

func TestPruneActiveVMDockerImages_PartialFailure(t *testing.T) {
	dockerMock := &mockDockerClient{
		// First call succeeds, second call fails (if a second call is made).
		perCallErrs: []error{
			nil,
			errors.New("image does not exist"),
		},
	}
	docker.WithDockerMock(t, dockerMock)
	report := &StaleReport{}
	ui := newMockUI()

	// Two images: digest1 is not :latest and != digest2, latest is skipped.
	slug := "myproject"
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneActiveVMDockerImages(context.Background(), slug, "digest2", msbImagesBySlug, false, ui, report)

	// The first (and only non-latest) image should be pruned successfully.
	if report.PrunedDockerImages != 1 {
		t.Errorf("PrunedDockerImages = %d, want 1", report.PrunedDockerImages)
	}
	// Only the first image should be in the removed list.
	if len(dockerMock.removedImages) != 1 || dockerMock.removedImages[0] != "opencode-msb/runner-myproject:digest1" {
		t.Errorf("removedImages = %v, want [opencode-msb/runner-myproject:digest1]", dockerMock.removedImages)
	}
}

// TestPruneStaleCascade_DockerRemoveFails verifies that when Docker image removal
// fails during a stale VM cascade, MSB image removal and volume removal still
// succeed, and the docker failure only affects the docker pruned count.
func TestPruneStaleCascade_DockerRemoveFails_DependentOpsSucceed(t *testing.T) {
	cp.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{}
	dockerMock := &mockDockerClient{
		// First Docker removal succeeds, second fails.
		perCallErrs: []error{nil, errors.New("image not found")},
	}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-abc123", Slug: slug}
	homeBySlugDigest := map[string][]volumeWithAge{
		slug: {oldVol("opencode-msb-home-myproject-digest1")},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false, lastUsed: ancient()},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true, lastUsed: ancient()},
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

	assertReport(t, report, prunedCounts{vms: 1, volumes: 1, msbImages: 2, dockerImages: 1})
	// Verify MSB removals.
	if len(client.RemovedVolumes) != 1 {
		t.Errorf("removed volumes = %v, want [opencode-msb-home-myproject-digest1]", client.RemovedVolumes)
	}
	if len(client.RemovedImages) != 2 {
		t.Errorf("removed MSB images count = %d, want 2", len(client.RemovedImages))
	}
	// Docker: only the first image removed (second fails, doesn't count).
	if len(dockerMock.removedImages) != 1 {
		t.Errorf("removed docker images = %v, want 1", dockerMock.removedImages)
	}
	// There should be a warn message about the failed docker image.
	if len(ui.WarnCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
	if len(ui.VerboseCalls) != 0 {
		t.Errorf("VerboseCalls = %d, want 0, got %v", len(ui.VerboseCalls), ui.VerboseCalls)
	}
}

func TestPruneCloneVolumes_DryRunDoesNotDelete(t *testing.T) {
	cp.WithMockConfigPaths(t)
	client := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "opencode-msb-clone-orphanproj-abc123"},
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

func TestPrune_DockerRemoveFails_PartialReport(t *testing.T) {
	cp.WithMockConfigPaths(t)
	oldTime := time.Now().Add(-2 * time.Hour)

	client := &msb.MockMsbClient{
		Sandboxes: []msb.SandboxHandle{
			// Stale VM for myproject -> cascade removes everything.
			&msb.MockSandboxHandle{
				Name_:      "opencode-msb-vm-myproject-1mjusbm3wikhb0",
				Status_:    msbSdk.SandboxStatusStopped,
				UpdatedAt_: oldTime,
			},
		},
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: "opencode-msb-home-myproject-1mjusbm3wikhb0-digest1", CreatedAt_: oldTime},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{
				Reference_:  "opencode-msb/runner-myproject-1mjusbm3wikhb0:digest1",
				LastUsedAt_: oldTime,
			},
			&msb.MockImageHandle{
				Reference_:  "opencode-msb/runner-myproject-1mjusbm3wikhb0:latest",
				LastUsedAt_: oldTime,
			},
		},
	}

	dockerMock := &mockDockerClient{
		// First succeeds, second fails.
		perCallErrs: []error{nil, errors.New("image not found")},
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

	// 1 stale VM is pruned.
	assertReport(t, report, prunedCounts{vms: 1, volumes: 1, msbImages: 2, dockerImages: 1})
	// There should be a warn message about the failed docker image.
	if len(ui.WarnCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
	if len(ui.VerboseCalls) != 0 {
		t.Errorf("VerboseCalls = %d, want 0, got %v", len(ui.VerboseCalls), ui.VerboseCalls)
	}
}
