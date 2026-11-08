package pruning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestPlanRemovalListsEveryArtifactForSlug pins that the plan is the union of
// everything the slug owns, discovered from the live runtime and the on-disk
// state, deduped and sorted: state keys (per-agent and legacy agent-less),
// sandboxes, home volumes, runner images, state files, claims and root-state
// adoption claims.
func TestPlanRemovalListsEveryArtifactForSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	const slug = "purge-plan-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	imageRef := naming.RunnerImageRef(slug, "opencode")
	projectVM := slug + "-opencode"
	taskVM := naming.TaskSandboxName(slug, "opencode", 1729000000000000000)

	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	legacyKey := state.Key{Slug: slug}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed agent state: %v", err)
	}
	if err := seedHomeState(legacyKey, state.HomeState{}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}
	claimsDir := filepath.Join(state.SlugDir(slug), "claims")
	if err := os.MkdirAll(claimsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(claimsDir, "notify.claim"), "")
	seedAdoptionClaim(t, slug, "opencode", slug, "legacy-volume")

	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: volumeName},
			// A foreign volume must never enter the plan.
			&msb.MockVolumeHandle{Name_: "user-data"},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: imageRef},
			// A foreign image must never enter the plan.
			&msb.MockImageHandle{Reference_: "docker.io/library/alpine:latest"},
		},
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{Name_: projectVM, Status_: msb.SandboxStatusStopped},
			&msb.MockSandboxHandle{Name_: taskVM, Status_: msb.SandboxStatusStopped},
			// A foreign sandbox must never enter the plan.
			&msb.MockSandboxHandle{Name_: "builder-vm", Status_: msb.SandboxStatusRunning},
		},
	})

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}

	if !slices.Equal(plan.Slugs, []string{slug}) {
		t.Errorf("Slugs = %v, want [%s]", plan.Slugs, slug)
	}
	wantKeys := []state.Key{legacyKey, agentKey}
	if !slices.Equal(plan.Keys, wantKeys) {
		t.Errorf("Keys = %v, want %v", plan.Keys, wantKeys)
	}
	wantSandboxes := []string{projectVM, taskVM}
	if !slices.Equal(plan.Sandboxes, wantSandboxes) {
		t.Errorf("Sandboxes = %v, want %v", plan.Sandboxes, wantSandboxes)
	}
	if !slices.Equal(plan.Volumes, []string{volumeName}) {
		t.Errorf("Volumes = %v, want [%s]", plan.Volumes, volumeName)
	}
	if !slices.Equal(plan.Images, []string{imageRef}) {
		t.Errorf("Images = %v, want [%s]", plan.Images, imageRef)
	}
	wantStateFiles := []string{
		filepath.Join(state.KeyDir(legacyKey), "state.yaml"),
		filepath.Join(state.KeyDir(agentKey), "state.yaml"),
	}
	slices.Sort(wantStateFiles)
	if !slices.Equal(plan.StateFiles, wantStateFiles) {
		t.Errorf("StateFiles = %v, want %v", plan.StateFiles, wantStateFiles)
	}
	if !slices.Equal(plan.Claims, []string{slug + "/notify"}) {
		t.Errorf("Claims = %v, want [%s/notify]", plan.Claims, slug)
	}
	if !slices.Equal(plan.AdoptionClaims, []string{slug + "/opencode"}) {
		t.Errorf("AdoptionClaims = %v, want [%s/opencode]", plan.AdoptionClaims, slug)
	}
}

// TestExecuteRemovesVolumeThenState pins the safety-critical ordering: the home
// volume is removed before its state.yaml, so the pointer is dropped only after
// the volume is gone, and the slug dir is reclaimed once everything is removed.
func TestExecuteRemovesVolumeThenState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-order-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	stateFile := filepath.Join(state.KeyDir(agentKey), "state.yaml")

	var statePresentAtVolumeRemoval bool
	vm := &msb.MockSandboxHandle{Name_: slug + "-opencode", Status_: msb.SandboxStatusStopped}
	mock := &msb.MockMsbClient{
		Volumes:   []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		Sandboxes: []msb.SandboxHandle{vm},
		RemoveVolumeFn: func(context.Context, string) error {
			_, statErr := os.Stat(stateFile)
			statePresentAtVolumeRemoval = statErr == nil
			return nil
		},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !slices.Contains(mock.RemovedVolumes, volumeName) {
		t.Errorf("RemovedVolumes = %v, want to contain %s", mock.RemovedVolumes, volumeName)
	}
	if !statePresentAtVolumeRemoval {
		t.Error("state.yaml must survive until after its volume is removed")
	}
	if _, err := state.ReadState(agentKey); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("state should be gone after purge, err = %v", err)
	}
	if !vm.DidRemove() {
		t.Error("the sandbox should have been removed")
	}
	if _, err := os.Stat(state.SlugDir(slug)); !os.IsNotExist(err) {
		t.Errorf("empty slug dir should be removed, stat err = %v", err)
	}
}

// TestExecuteFailsClosedOnCorruptState pins that an unreadable or corrupt state
// stops the purge before anything is deleted: the state is never discarded and
// no volume, sandbox or image is removed.
func TestExecuteFailsClosedOnCorruptState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-corrupt-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	stateFile := filepath.Join(state.KeyDir(agentKey), "state.yaml")

	vm := &msb.MockSandboxHandle{Name_: slug + "-opencode", Status_: msb.SandboxStatusStopped}
	mock := &msb.MockMsbClient{
		Volumes:   []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		Sandboxes: []msb.SandboxHandle{vm},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	// Corrupt the state between planning and execution: the fail-closed read
	// happens at execute time, before any deletion.
	if err := os.WriteFile(stateFile, []byte("schema: [not-valid"), 0o600); err != nil {
		t.Fatalf("corrupt state: %v", err)
	}
	if err := plan.Execute(context.Background(), true, false, &testUI); err == nil {
		t.Fatal("Execute should fail closed on a corrupt state")
	}

	if _, statErr := os.Stat(stateFile); statErr != nil {
		t.Errorf("corrupt state must survive, stat err = %v", statErr)
	}
	if len(mock.RemovedVolumes) != 0 {
		t.Errorf("no volume may be removed, got %v", mock.RemovedVolumes)
	}
	if vm.DidRemove() {
		t.Error("no sandbox may be removed")
	}
}

// TestExecuteRefusesRunningSandboxWithoutForce pins the guard: a running sandbox
// aborts the purge with a hint naming --force and deletes nothing; with --force
// the sandbox is stopped first, then removed.
func TestExecuteRefusesRunningSandboxWithoutForce(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-running-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	vm := &msb.MockSandboxHandle{Name_: slug + "-opencode", Status_: msb.SandboxStatusRunning}
	mock := &msb.MockMsbClient{
		Volumes:   []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		Sandboxes: []msb.SandboxHandle{vm},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if !plan.HasRunningSandbox() {
		t.Error("HasRunningSandbox = false, want true for a running VM")
	}

	err = plan.Execute(context.Background(), false, false, &testUI)
	if err == nil {
		t.Fatal("Execute should refuse a running sandbox without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %q, want it to name --force", err)
	}
	if vm.DidStop || vm.DidRemove() {
		t.Error("nothing may be stopped or removed without --force")
	}
	if len(mock.RemovedVolumes) != 0 {
		t.Errorf("no volume may be removed, got %v", mock.RemovedVolumes)
	}

	if err := plan.Execute(context.Background(), true, false, &testUI); err != nil {
		t.Fatalf("Execute with --force: %v", err)
	}
	if !vm.DidStop {
		t.Error("--force should stop the running sandbox first")
	}
	if !vm.DidRemove() {
		t.Error("--force should remove the sandbox")
	}
	if !slices.Contains(mock.RemovedVolumes, volumeName) {
		t.Errorf("RemovedVolumes = %v, want to contain %s", mock.RemovedVolumes, volumeName)
	}
}

// TestExecuteDryRunDeletesNothing pins that --dry-run performs no deletion at
// all: no sandbox, volume, image, state file or root-state claim is touched.
func TestExecuteDryRunDeletesNothing(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-dry-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	imageRef := naming.RunnerImageRef(slug, "opencode")
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	stateFile := filepath.Join(state.KeyDir(agentKey), "state.yaml")
	seedAdoptionClaim(t, slug, "opencode", "checkout-slug", "legacy-volume")

	vm := &msb.MockSandboxHandle{Name_: slug + "-opencode", Status_: msb.SandboxStatusStopped}
	mock := &msb.MockMsbClient{
		Volumes:   []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		Images:    []msb.ImageHandle{&msb.MockImageHandle{Reference_: imageRef}},
		Sandboxes: []msb.SandboxHandle{vm},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if err := plan.Execute(context.Background(), true, true, &testUI); err != nil {
		t.Fatalf("Execute dry-run: %v", err)
	}

	if _, statErr := os.Stat(stateFile); statErr != nil {
		t.Errorf("state file must survive a dry-run, stat err = %v", statErr)
	}
	if vm.DidStop || vm.DidRemove() {
		t.Error("sandbox must survive a dry-run")
	}
	if len(mock.RemovedVolumes) != 0 {
		t.Errorf("no volume may be removed in a dry-run, got %v", mock.RemovedVolumes)
	}
	if len(mock.RemovedImages) != 0 {
		t.Errorf("no image may be removed in a dry-run, got %v", mock.RemovedImages)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume(slug, "opencode"); !ok {
		t.Error("adoption claim must survive a dry-run")
	}
}

// TestExecuteKeepsStateWhenVolumeRemovalFails pins the pointer-preservation rule:
// a home volume that fails to remove keeps its state file (so the volume is
// still discoverable), the error is returned, and the purge still completes the
// other removals.
func TestExecuteKeepsStateWhenVolumeRemovalFails(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-volfail-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	stateFile := filepath.Join(state.KeyDir(agentKey), "state.yaml")

	mock := &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		RemoveVolumeFn: func(context.Context, string) error {
			return errors.New("volume busy")
		},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err == nil {
		t.Fatal("Execute should surface the failed volume removal")
	}
	if _, statErr := os.Stat(stateFile); statErr != nil {
		t.Errorf("state file must survive a failed volume removal, stat err = %v", statErr)
	}
	if _, statErr := state.ReadState(agentKey); statErr != nil {
		t.Errorf("the state pointer must survive, read err = %v", statErr)
	}
}

// TestPlanRemovalScopesToAgent pins that a non-empty agent scopes every deletion
// set, not only the key map: with two agents' volumes, images and sandboxes for
// one slug, only the targeted agent's artifacts are planned.
func TestPlanRemovalScopesToAgent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	const slug = "purge-agent-1mjusbm3wikhb0"
	opencodeVolume := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	piVolume := naming.HomeVolumeName(slug, "pi", "20260101T000000")
	opencodeImage := naming.RunnerImageRef(slug, "opencode")
	piImage := naming.RunnerImageRef(slug, "pi")
	opencodeVM := slug + "-opencode"
	piVM := slug + "-pi"

	for _, key := range []state.Key{{Slug: slug, Agent: "opencode"}, {Slug: slug, Agent: "pi"}} {
		if err := seedHomeState(key, state.HomeState{}); err != nil {
			t.Fatalf("seed %s state: %v", key.Agent, err)
		}
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{
		Volumes: []msb.VolumeHandle{
			&msb.MockVolumeHandle{Name_: opencodeVolume},
			&msb.MockVolumeHandle{Name_: piVolume},
		},
		Images: []msb.ImageHandle{
			&msb.MockImageHandle{Reference_: opencodeImage},
			&msb.MockImageHandle{Reference_: piImage},
		},
		Sandboxes: []msb.SandboxHandle{
			&msb.MockSandboxHandle{Name_: opencodeVM, Status_: msb.SandboxStatusStopped},
			&msb.MockSandboxHandle{Name_: piVM, Status_: msb.SandboxStatusStopped},
		},
	})

	plan, err := PlanRemoval(context.Background(), []string{slug}, "opencode", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if want := []state.Key{{Slug: slug, Agent: "opencode"}}; !slices.Equal(plan.Keys, want) {
		t.Errorf("Keys = %v, want %v", plan.Keys, want)
	}
	if !slices.Equal(plan.Volumes, []string{opencodeVolume}) {
		t.Errorf("Volumes = %v, want only %s", plan.Volumes, opencodeVolume)
	}
	if !slices.Equal(plan.Images, []string{opencodeImage}) {
		t.Errorf("Images = %v, want only %s", plan.Images, opencodeImage)
	}
	if !slices.Equal(plan.Sandboxes, []string{opencodeVM}) {
		t.Errorf("Sandboxes = %v, want only %s", plan.Sandboxes, opencodeVM)
	}
}

// TestExecuteKeepsStateForUnplannedVolume pins the stronger pointer rule: a
// state that records a home volume the plan never enumerated (the runtime did
// not list it) keeps its state file, so deleting the state can never orphan a
// volume that still exists.
func TestExecuteKeepsStateForUnplannedVolume(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-unplanned-1mjusbm3wikhb0"
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	recorded := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: recorded}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	stateFile := filepath.Join(state.KeyDir(agentKey), "state.yaml")
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if len(plan.Volumes) != 0 {
		t.Fatalf("plan unexpectedly lists volumes: %v", plan.Volumes)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, statErr := os.Stat(stateFile); statErr != nil {
		t.Errorf("state recording an unplanned volume must survive, stat err = %v", statErr)
	}
}

// TestExecuteRemovesPlannedImage pins the success branch of the image step: the
// planned runner image is removed in a real purge.
func TestExecuteRemovesPlannedImage(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-image-1mjusbm3wikhb0"
	ref := naming.RunnerImageRef(slug, "opencode")
	mock := &msb.MockMsbClient{
		Images: []msb.ImageHandle{&msb.MockImageHandle{Reference_: ref}},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []msb.MockRemoveImageCall{{Ref: ref, Force: true}}
	if !slices.Equal(mock.RemovedImages, want) {
		t.Errorf("RemovedImages = %v, want %v", mock.RemovedImages, want)
	}
}

// TestExecuteImageRemovalFailureWarnsAndContinues pins the failure branch of the
// image step: a failed removal warns, is joined into the returned error, and the
// purge still removes the rest.
func TestExecuteImageRemovalFailureWarnsAndContinues(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-imagefail-1mjusbm3wikhb0"
	ref := naming.RunnerImageRef(slug, "opencode")
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	mock := &msb.MockMsbClient{
		Images:  []msb.ImageHandle{&msb.MockImageHandle{Reference_: ref}},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		ImageRemoveFn: func(context.Context, string, bool) error {
			return errors.New("image in use")
		},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err == nil {
		t.Fatal("Execute should surface the failed image removal")
	}
	if len(testUI.WarnCalls) == 0 {
		t.Error("a failed image removal should warn")
	}
	if !slices.Contains(mock.RemovedVolumes, volumeName) {
		t.Errorf("RemovedVolumes = %v, want to contain %s (the purge continues)", mock.RemovedVolumes, volumeName)
	}
}

// TestExecuteRemovesAdoptionClaim pins the root-state cleanup branch: a purge
// drops the adoption claims its checkout owns.
func TestExecuteRemovesAdoptionClaim(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-claim-1mjusbm3wikhb0"
	if _, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		txn.State().ClaimAdoptedHomeVolume(slug, "pi", slug, "legacy-volume")
		return txn.Commit()
	}); err != nil {
		t.Fatalf("seed adoption claim: %v", err)
	}
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume(slug, "pi"); ok {
		t.Errorf("adoption claim survived the purge: %+v", root.AdoptedHomeVolumes)
	}
}

// TestExecuteSweepsClaimsAndLeases pins the step-1 branches: an unheld claim and
// a stale client lease are removed, a held claim is kept.
func TestExecuteSweepsClaimsAndLeases(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "purge-sweep-1mjusbm3wikhb0"
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	claimsDir := filepath.Join(state.SlugDir(slug), "claims")
	if err := os.MkdirAll(claimsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(claimsDir, "free.claim"), "")
	releaseClaim, claimed, err := state.AcquireClaim(slug, "held")
	if err != nil || !claimed {
		t.Fatalf("AcquireClaim: claimed=%v err=%v", claimed, err)
	}
	defer releaseClaim()

	clientsDir := filepath.Join(state.KeyDir(agentKey), "clients")
	if err := os.MkdirAll(clientsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	staleLease := filepath.Join(clientsDir, "22222-4000000000.lock")
	testutil.WritePath(t, staleLease, "stale")
	aged := time.Now().Add(-time.Hour)
	if err := os.Chtimes(staleLease, aged, aged); err != nil {
		t.Fatal(err)
	}

	msb.WithMsbMock(t, &msb.MockMsbClient{})
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(claimsDir, "free.claim")); !os.IsNotExist(statErr) {
		t.Errorf("free claim should be gone, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(claimsDir, "held.claim")); statErr != nil {
		t.Errorf("held claim should remain, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(staleLease); !os.IsNotExist(statErr) {
		t.Errorf("stale lease should be gone, stat err = %v", statErr)
	}
}

// TestPlanRemovalKeepsVolumeOwnedByAnotherCheckout pins the P1 guard: purging a
// legacy slug whose home volume a different checkout adopted must not delete the
// volume. The volume name still embeds the legacy slug, so the name-based rule
// alone would plan it; the checkout's state reference makes it foreign.
func TestPlanRemovalKeepsVolumeOwnedByAnotherCheckout(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	const (
		legacy   = "purge-legacy-1mjusbm3wikhb0"
		checkout = "purge-ownercheckout-2nkvtdn4xjlic1"
	)
	volumeName := naming.HomeVolumeName(legacy, "opencode", "20260101T000000")
	if err := seedHomeState(
		state.Key{Slug: checkout, Agent: "opencode"},
		state.HomeState{HomeVolume: volumeName},
	); err != nil {
		t.Fatalf("seed checkout state: %v", err)
	}
	mock := &msb.MockMsbClient{Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}}}
	msb.WithMsbMock(t, mock)

	plan, err := PlanRemoval(context.Background(), []string{legacy}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if len(plan.Volumes) != 0 {
		t.Fatalf("Volumes = %v, want none (owned by another checkout)", plan.Volumes)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(mock.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none (the checkout's saved home must survive)", mock.RemovedVolumes)
	}
	if len(testUI.WarnCalls) == 0 {
		t.Error("a kept volume should warn")
	}
}

// TestPlanRemovalReclaimsVolumeReferencedByScopedCheckout pins the other half of
// the P1 fix: purging the checkout reclaims the legacy-named volume its state
// points at, and drops the state in the same run (the pointer never dangles).
func TestPlanRemovalReclaimsVolumeReferencedByScopedCheckout(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	const (
		legacy   = "purge-legacyref-1mjusbm3wikhb0"
		checkout = "purge-scopedcheckout-2nkvtdn4xjlic1"
	)
	volumeName := naming.HomeVolumeName(legacy, "opencode", "20260101T000000")
	agentKey := state.Key{Slug: checkout, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed checkout state: %v", err)
	}
	mock := &msb.MockMsbClient{Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}}}
	msb.WithMsbMock(t, mock)

	plan, err := PlanRemoval(context.Background(), []string{checkout}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if !slices.Equal(plan.Volumes, []string{volumeName}) {
		t.Fatalf("Volumes = %v, want [%s]", plan.Volumes, volumeName)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !slices.Contains(mock.RemovedVolumes, volumeName) {
		t.Errorf("RemovedVolumes = %v, want to contain %s", mock.RemovedVolumes, volumeName)
	}
	if _, err := state.ReadState(agentKey); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("the checkout state should be gone with its volume, err = %v", err)
	}
}

// TestPlanRemovalKeepsAdoptionClaimOwnedByAnotherCheckout pins that purging a
// legacy slug does not delete a live checkout's adoption claim: the claim is the
// checkout's recovery marker, not the legacy identity's to remove.
func TestPlanRemovalKeepsAdoptionClaimOwnedByAnotherCheckout(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	const legacy = "purge-claimkeep-1mjusbm3wikhb0"
	seedAdoptionClaim(t, legacy, "opencode", "other-checkout-slug", "legacy-volume")
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	plan, err := PlanRemoval(context.Background(), []string{legacy}, "", &testUI)
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
	}
	if len(plan.AdoptionClaims) != 0 {
		t.Errorf("AdoptionClaims = %v, want none (owned by another checkout)", plan.AdoptionClaims)
	}
	if err := plan.Execute(context.Background(), false, false, &testUI); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if _, ok := root.AdoptedHomeVolume(legacy, "opencode"); !ok {
		t.Error("the live checkout's adoption claim must survive the legacy purge")
	}
}
