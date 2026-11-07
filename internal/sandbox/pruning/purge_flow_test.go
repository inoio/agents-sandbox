package pruning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
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
	configpaths.WithMockConfigPaths(t)
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
	seedAdoptionClaim(t, slug, "opencode", "checkout-slug", "legacy-volume")

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
			&msb.MockSandboxHandle{Name_: projectVM, Status_: msbSdk.SandboxStatusStopped},
			&msb.MockSandboxHandle{Name_: taskVM, Status_: msbSdk.SandboxStatusStopped},
			// A foreign sandbox must never enter the plan.
			&msb.MockSandboxHandle{Name_: "builder-vm", Status_: msbSdk.SandboxStatusRunning},
		},
	})

	plan, err := PlanRemoval(context.Background(), []string{slug}, "")
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
	configpaths.WithMockConfigPaths(t)
	const slug = "purge-order-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	stateFile := filepath.Join(state.KeyDir(agentKey), "state.yaml")

	var statePresentAtVolumeRemoval bool
	vm := &msb.MockSandboxHandle{Name_: slug + "-opencode", Status_: msbSdk.SandboxStatusStopped}
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

	plan, err := PlanRemoval(context.Background(), []string{slug}, "")
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
	configpaths.WithMockConfigPaths(t)
	const slug = "purge-corrupt-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	stateFile := filepath.Join(state.KeyDir(agentKey), "state.yaml")
	if err := os.WriteFile(stateFile, []byte("schema: [not-valid"), 0o600); err != nil {
		t.Fatalf("corrupt state: %v", err)
	}

	vm := &msb.MockSandboxHandle{Name_: slug + "-opencode", Status_: msbSdk.SandboxStatusStopped}
	mock := &msb.MockMsbClient{
		Volumes:   []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		Sandboxes: []msb.SandboxHandle{vm},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "")
	if err != nil {
		t.Fatalf("PlanRemoval: %v", err)
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
	configpaths.WithMockConfigPaths(t)
	const slug = "purge-running-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	vm := &msb.MockSandboxHandle{Name_: slug + "-opencode", Status_: msbSdk.SandboxStatusRunning}
	mock := &msb.MockMsbClient{
		Volumes:   []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		Sandboxes: []msb.SandboxHandle{vm},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "")
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
	configpaths.WithMockConfigPaths(t)
	const slug = "purge-dry-1mjusbm3wikhb0"
	volumeName := naming.HomeVolumeName(slug, "opencode", "20260101T000000")
	imageRef := naming.RunnerImageRef(slug, "opencode")
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{HomeVolume: volumeName}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	stateFile := filepath.Join(state.KeyDir(agentKey), "state.yaml")
	seedAdoptionClaim(t, slug, "opencode", "checkout-slug", "legacy-volume")

	vm := &msb.MockSandboxHandle{Name_: slug + "-opencode", Status_: msbSdk.SandboxStatusStopped}
	mock := &msb.MockMsbClient{
		Volumes:   []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: volumeName}},
		Images:    []msb.ImageHandle{&msb.MockImageHandle{Reference_: imageRef}},
		Sandboxes: []msb.SandboxHandle{vm},
	}
	msb.WithMsbMock(t, mock)
	testUI := termio.NewTestMock(t)

	plan, err := PlanRemoval(context.Background(), []string{slug}, "")
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
	configpaths.WithMockConfigPaths(t)
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

	plan, err := PlanRemoval(context.Background(), []string{slug}, "")
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
