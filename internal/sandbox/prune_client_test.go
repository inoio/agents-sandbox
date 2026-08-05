package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
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
	_ client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	// Check per-call errors first.
	if m.callCounter < len(m.perCallErrs) {
		err := m.perCallErrs[m.callCounter]
		m.callCounter++
		if err != nil {
			return client.ImageRemoveResult{}, err
		}
		m.removedImages = append(m.removedImages, imageID)
		return client.ImageRemoveResult{}, nil
	}
	// Fall back to global removeErr (checked first to maintain legacy behavior).
	if m.removeErr != nil {
		return client.ImageRemoveResult{}, m.removeErr
	}
	m.removedImages = append(m.removedImages, imageID)
	return client.ImageRemoveResult{}, nil
}

func newMockUI() *stdio.Mock {
	return &stdio.Mock{}
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
	client := &MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string]map[string]string{
		slug: {
			"digest1": "opencode-msb-home-myproject-digest1",
			"":        "opencode-msb-home-myproject",
		},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneStaleCascade(context.Background(), client, entry, homeBySlugDigest, msbImagesBySlug, false, ui, report)

	assertReport(t, report, prunedCounts{vms: 1, volumes: 2, msbImages: 2, dockerImages: 2})

	wantRemoved := []string{entry.Name}
	if len(client.removedSandboxes) != 1 || client.removedSandboxes[0] != entry.Name {
		t.Errorf("removed sandboxes = %v, want %v", client.removedSandboxes, wantRemoved)
	}

	wantVolumes := []string{"opencode-msb-home-myproject-digest1", "opencode-msb-home-myproject"}
	if len(client.removedVolumes) != len(wantVolumes) {
		t.Errorf("removed volumes = %v, want %v", client.removedVolumes, wantVolumes)
	}

	wantMSBImages := []string{"opencode-msb/runner-myproject:digest1", "opencode-msb/runner-myproject:latest"}
	if len(client.removedImages) != len(wantMSBImages) {
		t.Errorf("removed msb images count = %d, want %d", len(client.removedImages), len(wantMSBImages))
	}

	wantDockerImages := []string{"opencode-msb/runner-myproject:digest1", "opencode-msb/runner-myproject:latest"}
	if len(dockerMock.removedImages) != len(wantDockerImages) {
		t.Errorf("removed docker images = %v, want %v", dockerMock.removedImages, wantDockerImages)
	}
}

func TestPruneStaleCascade_DryRunDoesNotDelete(t *testing.T) {
	client := &MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string]map[string]string{
		slug: {"digest1": "opencode-msb-home-myproject-digest1"},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false}},
	}

	pruneStaleCascade(context.Background(), client, entry, homeBySlugDigest, msbImagesBySlug, true, ui, report)

	assertReport(t, report, prunedCounts{vms: 1, volumes: 1, msbImages: 1, dockerImages: 1})

	total := len(client.removedSandboxes) + len(client.removedVolumes) +
		len(client.removedImages) + len(dockerMock.removedImages)
	if total != 0 {
		t.Errorf("expected no deletion calls in dry run, got sandboxes=%v volumes=%v images=%v docker=%v",
			client.removedSandboxes, client.removedVolumes, client.removedImages, dockerMock.removedImages)
	}
}

func TestPruneStaleCascade_RemoveErrorWarnsAndStopsCascade(t *testing.T) {
	client := &MockMsbClient{removeSandboxErr: errors.New("sandbox locked")}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string]map[string]string{
		slug: {"digest1": "opencode-msb-home-myproject-digest1"},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false}},
	}

	pruneStaleCascade(context.Background(), client, entry, homeBySlugDigest, msbImagesBySlug, false, ui, report)

	if report.PrunedVMs != 0 {
		t.Errorf("PrunedVMs = %d, want 0", report.PrunedVMs)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning on sandbox removal error")
	}
	if len(client.removedVolumes)+len(client.removedImages)+len(dockerMock.removedImages) != 0 {
		t.Error("expected cascade to stop after sandbox removal failure")
	}
}

func TestPruneActiveVMCleanup_KeepsMatchingDigestAndLatest(t *testing.T) {
	client := &MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	activeDigest := "digest2"
	homeBySlugDigest := map[string]map[string]string{
		slug: {
			"digest1": "opencode-msb-home-myproject-digest1",
			"digest2": "opencode-msb-home-myproject-digest2",
			"":        "opencode-msb-home-myproject",
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

	assertReport(t, report, prunedCounts{volumes: 1, msbImages: 1, dockerImages: 1})

	if len(client.removedVolumes) != 1 || client.removedVolumes[0] != "opencode-msb-home-myproject-digest1" {
		t.Errorf("removed volumes = %v, want [opencode-msb-home-myproject-digest1]", client.removedVolumes)
	}

	if len(client.removedImages) != 1 || client.removedImages[0].Ref != "opencode-msb/runner-myproject:digest1" {
		t.Errorf("removed msb images = %v, want [opencode-msb/runner-myproject:digest1]", client.removedImages)
	}

	if len(dockerMock.removedImages) != 1 || dockerMock.removedImages[0] != "opencode-msb/runner-myproject:digest1" {
		t.Errorf("removed docker images = %v, want [opencode-msb/runner-myproject:digest1]", dockerMock.removedImages)
	}
}

func TestPruneActiveVMCleanup_DryRunCountsButDoesNotDelete(t *testing.T) {
	client := &MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	homeBySlugDigest := map[string]map[string]string{
		slug: {
			"digest1": "opencode-msb-home-myproject-digest1",
			"digest2": "opencode-msb-home-myproject-digest2",
		},
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

	if report.PrunedVolumes != 1 || report.PrunedMSBImages != 1 || report.PrunedDockerImages != 1 {
		t.Errorf("unexpected report counts: volumes=%d msb=%d docker=%d",
			report.PrunedVolumes, report.PrunedMSBImages, report.PrunedDockerImages)
	}
	if len(client.removedVolumes)+len(client.removedImages)+len(dockerMock.removedImages) != 0 {
		t.Error("expected no deletion calls in dry run")
	}
}

func TestPruneOrphanSlug_RemovesEverything(t *testing.T) {
	client := &MockMsbClient{}
	dockerMock := &mockDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "orphan"
	homeBySlugDigest := map[string]map[string]string{
		slug: {
			"digest1": "opencode-msb-home-orphan-digest1",
			"":        "opencode-msb-home-orphan",
		},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-orphan:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-orphan:latest", digest: "", isLatest: true},
		},
	}

	pruneOrphanSlug(context.Background(), client, slug, homeBySlugDigest, msbImagesBySlug, report, false, ui)

	assertReport(t, report, prunedCounts{volumes: 2, msbImages: 2, dockerImages: 2})

	if len(client.removedVolumes) != 2 {
		t.Errorf("removed volumes = %v, want 2", client.removedVolumes)
	}
	if len(client.removedImages) != 2 {
		t.Errorf("removed msb images = %v, want 2", client.removedImages)
	}
	if len(dockerMock.removedImages) != 2 {
		t.Errorf("removed docker images = %v, want 2", dockerMock.removedImages)
	}
}

func TestPruneCloneVolume_RemovesWhenNoActiveVM(t *testing.T) {
	client := &MockMsbClient{}
	ui := newMockUI()
	report := &StaleReport{}

	activeVMDigests := map[string]string{}
	cv := "opencode-msb-clone-myproject-abc123"

	pruneCloneVolume(context.Background(), client, cv, nil, activeVMDigests, false, ui, report)

	if report.PrunedCloneVolumes != 1 {
		t.Errorf("PrunedCloneVolumes = %d, want 1", report.PrunedCloneVolumes)
	}
	if len(client.removedVolumes) != 1 || client.removedVolumes[0] != cv {
		t.Errorf("removed volumes = %v, want [%s]", client.removedVolumes, cv)
	}
}

func TestPruneCloneVolume_KeepsWhenActiveVMExists(t *testing.T) {
	client := &MockMsbClient{}
	ui := newMockUI()
	report := &StaleReport{}

	activeVMDigests := map[string]string{"myproject": "digest1"}
	cv := "opencode-msb-clone-myproject-abc123"

	pruneCloneVolume(context.Background(), client, cv, nil, activeVMDigests, false, ui, report)

	if report.PrunedCloneVolumes != 0 {
		t.Errorf("PrunedCloneVolumes = %d, want 0", report.PrunedCloneVolumes)
	}
	if len(client.removedVolumes) != 0 {
		t.Errorf("expected no volume removal, got %v", client.removedVolumes)
	}
}

func TestPruneCloneVolume_DryRunDoesNotDelete(t *testing.T) {
	client := &MockMsbClient{}
	ui := newMockUI()
	report := &StaleReport{}

	cv := "opencode-msb-clone-myproject-abc123"
	pruneCloneVolume(context.Background(), client, cv, nil, map[string]string{}, true, ui, report)

	if report.PrunedCloneVolumes != 1 {
		t.Errorf("PrunedCloneVolumes = %d, want 1", report.PrunedCloneVolumes)
	}
	if len(client.removedVolumes) != 0 {
		t.Error("expected no deletion in dry run")
	}
}

func TestPrune_WithMocks_CoversAllCases(t *testing.T) {
	oldTime := time.Now().Add(-2 * time.Hour)
	recentTime := time.Now().Add(-5 * time.Minute)

	client := &MockMsbClient{
		Sandboxes: []SandboxHandle{
			// Stale VM for myproject -> cascade removes everything.
			&MockSandboxHandle{
				Name_:      "opencode-msb-vm-myproject-1mjusbm3wikhb0",
				Status_:    msb.SandboxStatusStopped,
				UpdatedAt_: oldTime,
			},
			// Active VM for activeproject -> cleanup non-matching digests.
			&MockSandboxHandle{
				Name_:      "opencode-msb-vm-activeproject-1mjusbm3wikhb0-main",
				Status_:    msb.SandboxStatusRunning,
				UpdatedAt_: recentTime,
				Image_:     "opencode-msb/runner-activeproject-1mjusbm3wikhb0:digest2",
			},
			// Task sandbox -> always pruned.
			&MockSandboxHandle{
				Name_:      "opencode-msb-task-fill-proj",
				Status_:    msb.SandboxStatusStopped,
				UpdatedAt_: oldTime,
			},
		},
		Volumes: []VolumeHandle{
			&MockVolumeHandle{Name_: "opencode-msb-home-myproject-1mjusbm3wikhb0-digest1"},
			&MockVolumeHandle{Name_: "opencode-msb-home-activeproject-1mjusbm3wikhb0-digest1"},
			&MockVolumeHandle{Name_: "opencode-msb-home-activeproject-1mjusbm3wikhb0-digest2"},
			&MockVolumeHandle{Name_: "opencode-msb-clone-myproject-1mjusbm3wikhb0-abc123"},
			&MockVolumeHandle{Name_: "opencode-msb-clone-activeproject-1mjusbm3wikhb0-def456"},
		},
		Images: []ImageHandle{
			&MockImageHandle{Reference_: "opencode-msb/runner-myproject-1mjusbm3wikhb0:digest1"},
			&MockImageHandle{Reference_: "opencode-msb/runner-activeproject-1mjusbm3wikhb0:digest1"},
			&MockImageHandle{Reference_: "opencode-msb/runner-activeproject-1mjusbm3wikhb0:digest2"},
			&MockImageHandle{Reference_: "opencode-msb/runner-orphan:latest"},
		},
	}
	docker.WithNoopDockerMock(t)
	ui := newMockUI()

	oldNewMsbClient := newMsbClient
	newMsbClient = func() MsbClient { return client }
	defer func() { newMsbClient = oldNewMsbClient }()

	report, err := Prune(context.Background(), 30*time.Minute, false, ui)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}

	assertReport(
		t,
		report,
		prunedCounts{vms: 1, taskSandboxes: 1, volumes: 2, cloneVolumes: 1, msbImages: 3, dockerImages: 3},
	)
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
	// There should be a verbose message about the failed docker image.
	if len(ui.VerboseCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
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
	client := &MockMsbClient{}
	dockerMock := &mockDockerClient{
		// First Docker removal succeeds, second fails.
		perCallErrs: []error{nil, errors.New("image not found")},
	}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-abc123", Slug: slug}
	homeBySlugDigest := map[string]map[string]string{
		slug: {"digest1": "opencode-msb-home-myproject-digest1"},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneStaleCascade(context.Background(), client, entry, homeBySlugDigest, msbImagesBySlug, false, ui, report)

	assertReport(t, report, prunedCounts{vms: 1, volumes: 1, msbImages: 2, dockerImages: 1})
	// Verify MSB removals.
	if len(client.removedVolumes) != 1 {
		t.Errorf("removed volumes = %v, want [opencode-msb-home-myproject-digest1]", client.removedVolumes)
	}
	if len(client.removedImages) != 2 {
		t.Errorf("removed MSB images count = %d, want 2", len(client.removedImages))
	}
	// Docker: only the first image removed (second fails, doesn't count).
	if len(dockerMock.removedImages) != 1 {
		t.Errorf("removed docker images = %v, want 1", dockerMock.removedImages)
	}
	// There should be a verbose message about the failed docker image.
	if len(ui.VerboseCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
}

func TestPrune_DockerRemoveFails_PartialReport(t *testing.T) {
	oldTime := time.Now().Add(-2 * time.Hour)

	client := &MockMsbClient{
		Sandboxes: []SandboxHandle{
			// Stale VM for myproject -> cascade removes everything.
			&MockSandboxHandle{
				Name_:      "opencode-msb-vm-myproject-1mjusbm3wikhb0",
				Status_:    msb.SandboxStatusStopped,
				UpdatedAt_: oldTime,
			},
		},
		Volumes: []VolumeHandle{
			&MockVolumeHandle{Name_: "opencode-msb-home-myproject-1mjusbm3wikhb0-digest1"},
		},
		Images: []ImageHandle{
			&MockImageHandle{Reference_: "opencode-msb/runner-myproject-1mjusbm3wikhb0:digest1"},
			&MockImageHandle{Reference_: "opencode-msb/runner-myproject-1mjusbm3wikhb0:latest"},
		},
	}

	dockerMock := &mockDockerClient{
		// First succeeds, second fails.
		perCallErrs: []error{nil, errors.New("image not found")},
	}
	docker.WithDockerMock(t, dockerMock)
	ui := newMockUI()

	oldNewMsbClient := newMsbClient
	newMsbClient = func() MsbClient { return client }
	defer func() { newMsbClient = oldNewMsbClient }()

	report, err := Prune(context.Background(), 30*time.Minute, false, ui)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}

	// 1 stale VM is pruned.
	assertReport(t, report, prunedCounts{vms: 1, volumes: 1, msbImages: 2, dockerImages: 1})
	// There should be a verbose message about the failed docker image.
	if len(ui.VerboseCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
}
