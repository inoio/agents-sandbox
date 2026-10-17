package runtimemigration

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
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// interactiveUI returns an interactive mock UI whose Select returns key and
// records that a prompt happened.
func interactiveUI(key string, prompt *bool) *termio.Mock {
	return &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			*prompt = true
			return key, nil
		},
	}
}

// writeCorruptRootState writes an unparseable root state.yaml so ReadRootState
// fails, exercising the offer's best-effort error handling.
func writeCorruptRootState(t *testing.T) {
	t.Helper()
	path := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(path, []byte("migrations: [unterminated"), 0o600); err != nil {
		t.Fatalf("write corrupt root state: %v", err)
	}
}

// TestOfferStateReadErrorWarnsAndSkips pins the spec's anti-goal: a corrupt or
// unreadable launcher-global state file must not block the session. The offer
// is best-effort, so it warns and returns nil rather than surfacing the error.
func TestOfferStateReadErrorWarnsAndSkips(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeCorruptRootState(t)

	selectCalls := 0
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			selectCalls++
			return choiceCopy, nil
		},
	}
	client := &msb.MockMsbClient{}

	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer() error = %v, want nil (best-effort)", err)
	}
	if selectCalls != 0 {
		t.Errorf("Select called %d times, want 0", selectCalls)
	}
	if len(ui.WarnCalls) != 1 {
		t.Errorf("WarnCalls = %v, want exactly one warning", ui.WarnCalls)
	}
	if len(client.Volumes) != 0 || len(client.RemovedVolumes) != 0 {
		t.Error("Offer() touched volumes on a state read error")
	}
}

// TestOfferNecessaryVolumesErrorWarnsAndSkips covers the other best-effort
// branch: a failure deriving necessity (here, the home directory cannot be
// resolved) warns and skips instead of blocking the session.
func TestOfferNecessaryVolumesErrorWarnsAndSkips(t *testing.T) {
	configpaths.WithMockConfigPathsHomeError(t, errors.New("home unavailable"))

	var prompted bool
	ui := interactiveUI(choiceCopy, &prompted)
	client := &msb.MockMsbClient{}

	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer() error = %v, want nil (best-effort)", err)
	}
	if prompted {
		t.Error("Offer() prompted on a NecessaryVolumes error")
	}
	if len(ui.WarnCalls) != 1 {
		t.Errorf("WarnCalls = %v, want exactly one warning", ui.WarnCalls)
	}
}

func TestOfferDismissedDoesNotPrompt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")
	if err := state.WriteRootState(state.RootState{
		Migrations: map[string]state.Migration{MigrationName: {Status: state.MigrationDismissed}},
	}); err != nil {
		t.Fatalf("WriteRootState: %v", err)
	}

	var prompted bool
	ui := interactiveUI("c", &prompted)
	client := &msb.MockMsbClient{}

	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if prompted {
		t.Error("Offer() prompted although the migration was dismissed")
	}
	if len(client.CreatedSandboxes) != 0 {
		t.Error("Offer() copied volumes although the migration was dismissed")
	}
}

// TestOfferTerminalMigrationDoesNotPrompt pins that a terminal (completed)
// migration is never re-armed: even with a legacy volume present, the offer
// returns without prompting.
func TestOfferTerminalMigrationDoesNotPrompt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")
	if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCompleted},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var prompted bool
	ui := interactiveUI(choiceCopy, &prompted)
	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if prompted {
		t.Error("prompted after a terminal migration")
	}
}

// TestOfferFinalizesOpenCampaignWithNothingLeft pins the crash-recovery finalize:
// a campaign that copied everything but crashed before recording completion is
// closed out as completed on the next offer, without prompting.
func TestOfferFinalizesOpenCampaignWithNothingLeft(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "done"
	addVolumeDir(t, legacy, name)
	// Campaign copied everything, then crashed before writing completed.
	if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCopying, Copied: []string{name}},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ui := &termio.Mock{IsInteractiveResult: true}
	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	root, _ := state.ReadRootState()
	m, _ := root.Migration(MigrationName)
	if m.Status != state.MigrationCompleted {
		t.Fatalf("status = %q, want completed", m.Status)
	}
}

// TestOfferStartFreshIsNotLostToACompletedCampaign pins the lock-serialization
// ruling: a "start fresh" dismissal must not clobber a terminal status another
// session already recorded. The migration is pending when the prompt is shown,
// but a concurrent session completes it before the dismissal write; choosing
// fresh must re-read under the lock and leave the terminal completed status
// intact.
func TestOfferStartFreshIsNotLostToACompletedCampaign(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")

	// A concurrent session wins the race by completing the campaign after the
	// prompt is shown but before the dismissal write.
	var prompted bool
	ui := interactiveUI(choiceFresh, &prompted)
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		prompted = true
		root, err := state.ReadRootState()
		if err != nil {
			t.Fatalf("ReadRootState: %v", err)
		}
		if m, _ := root.Migration(MigrationName); m.Status != "" {
			t.Fatalf("precondition: status = %q, want pending", m.Status)
		}
		if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
			MigrationName: {Status: state.MigrationCompleted},
		}}); err != nil {
			t.Fatalf("concurrent completion: %v", err)
		}
		return choiceFresh, nil
	}

	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if !prompted {
		t.Fatal("Offer did not prompt")
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if m, _ := root.Migration(MigrationName); m.Status != state.MigrationCompleted {
		t.Fatalf("status = %q, want completed (start-fresh must not overwrite a finished campaign)", m.Status)
	}
}

// TestOfferStartFreshLockFailureSurfaces pins that a migration-lock acquisition
// failure on the start-fresh path surfaces as an Offer error rather than a
// silent no-op. A directory occupying the root-state lock path makes the lock
// open fail.
func TestOfferStartFreshLockFailureSurfaces(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")
	blockRootLock(t)

	var prompted bool
	ui := interactiveUI(choiceFresh, &prompted)
	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err == nil {
		t.Fatal("Offer() error = nil, want a lock-acquisition failure on start-fresh")
	}
}

func TestOfferNothingNecessaryDoesNotPrompt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	installLegacyHome(t)

	var prompted bool
	ui := interactiveUI("c", &prompted)
	client := &msb.MockMsbClient{}

	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if prompted {
		t.Error("Offer() prompted although nothing was necessary")
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("Offer() warned despite nothing necessary: %v", ui.WarnCalls)
	}
}

// TestOfferNonInteractiveBlocksBeforeFreshHome pins option D: a non-interactive
// session with a pending migration must not proceed, because resolving the home
// volume would create a fresh one and overwrite the project's recorded
// reference before the user can accept the copy. It returns the pending error
// and touches no volumes.
func TestOfferNonInteractiveBlocksBeforeFreshHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")

	ui := &termio.Mock{IsInteractiveResult: false}
	client := &msb.MockMsbClient{}

	err := Offer(context.Background(), client, ui)
	if !errors.Is(err, ErrMigrationPending) {
		t.Fatalf("Offer() error = %v, want ErrMigrationPending", err)
	}
	if !strings.Contains(err.Error(), "interactive") {
		t.Errorf("error %q does not mention running interactively", err.Error())
	}
	if len(client.RemovedVolumes) != 0 || len(client.Volumes) != 0 {
		t.Error("Offer() touched volumes in a non-interactive session")
	}
}

// TestOfferAssumeYesBehavesLikeNonInteractive pins Review Focus 3: --yes is
// treated as non-interactive for this offer, so no prompt is ever shown even
// though the user "assumed yes" globally.
func TestOfferAssumeYesBehavesLikeNonInteractive(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")

	var selectCalls int
	ui := &termio.Mock{
		IsInteractiveResult: false,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			selectCalls++
			return "c", nil
		},
	}
	ui.SetAssumeYes(true)
	client := &msb.MockMsbClient{}

	err := Offer(context.Background(), client, ui)
	if !errors.Is(err, ErrMigrationPending) {
		t.Fatalf("Offer() error = %v, want ErrMigrationPending under --yes", err)
	}
	if selectCalls != 0 {
		t.Errorf("Select called %d times under --yes, want 0", selectCalls)
	}
}

func TestOfferChooseCopyCopiesVolumes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "legacy"
	addVolumeDir(t, legacy, name)
	writeFileWithMode(t, legacy+"/volumes/"+name+"/settings.json", "{}", 0o600)

	target := t.TempDir()
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, volumeName string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: volumeName, Path_: target}, nil
		},
	}

	var prompted bool
	ui := interactiveUI("c", &prompted)

	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if !prompted {
		t.Fatal("Offer() did not prompt in an interactive session with a necessary volume")
	}
	if len(client.RemovedVolumes) != 0 {
		t.Errorf("RemovedVolumes = %v, want none (copy succeeded)", client.RemovedVolumes)
	}
	assertFileWithMode(t, target+"/settings.json", "{}", 0o600)
}

// TestOfferCopyErrorAborts verifies that a campaign-scoped copy failure aborts
// the session rather than continuing to a fresh home, which would overwrite the
// recorded reference and orphan the old data. Here the state root is unwritable,
// so persisting the copying status fails and the error propagates through Offer.
// A volume-scoped copy failure, by contrast, is excluded inside CopyVolumes and
// does not abort (see TestCopyVolumesExcludesVolumeScopedFailureAndCompletes).
func TestOfferCopyErrorAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")
	blockRootState(t)

	var prompted bool
	ui := interactiveUI(choiceCopy, &prompted)

	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err == nil {
		t.Fatal("Offer() error = nil, want a campaign-scoped copy failure to abort the session")
	}
}

func TestOfferChooseStartFreshPersistsDismissal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")

	var prompted bool
	ui := interactiveUI("s", &prompted)
	client := &msb.MockMsbClient{}

	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer() error = %v", err)
	}

	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if m, ok := root.Migration(MigrationName); !ok || m.Status != state.MigrationDismissed {
		t.Error("start-fresh did not persist a dismissed runtime_home migration")
	}
}

// TestOfferChooseQuitAborts pins that quitting the prompt aborts the session
// without a decision: no copy, no dismissal, and the migration stays pending.
func TestOfferChooseQuitAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")

	var prompted bool
	ui := interactiveUI(choiceQuit, &prompted)
	client := &msb.MockMsbClient{}

	if err := Offer(context.Background(), client, ui); !errors.Is(err, ErrMigrationPending) {
		t.Fatalf("Offer() error = %v, want ErrMigrationPending on quit", err)
	}

	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if m, ok := root.Migration(MigrationName); ok && m.Status == state.MigrationDismissed {
		t.Error("quit dismissed the migration; want it still pending")
	}
}

// TestOfferSelectErrorAborts verifies that a failing prompt (e.g. the terminal
// closed) aborts the session rather than proceeding to a fresh home.
func TestOfferSelectErrorAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", context.Canceled
		},
	}
	client := &msb.MockMsbClient{}

	if err := Offer(context.Background(), client, ui); !errors.Is(err, ErrMigrationPending) {
		t.Fatalf("Offer() error = %v, want ErrMigrationPending on prompt failure", err)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if m, ok := root.Migration(MigrationName); ok && m.Status == state.MigrationDismissed {
		t.Error("a failed prompt dismissed the migration; want it still pending")
	}
	if len(client.RemovedVolumes) != 0 || len(client.Volumes) != 0 {
		t.Error("a failed prompt touched volumes")
	}
}

// makeLegacyVolumesUnlistable replaces <legacy>/volumes with a self-referential
// symlink, so listing it fails with ELOOP (a non-tolerated error in both the
// pending and copying listings).
func makeLegacyVolumesUnlistable(t *testing.T, legacy string) {
	t.Helper()
	volumes := filepath.Join(legacy, "volumes")
	if err := os.RemoveAll(volumes); err != nil {
		t.Fatalf("remove volumes dir: %v", err)
	}
	if err := os.Symlink("volumes", volumes); err != nil {
		t.Fatalf("symlink volumes: %v", err)
	}
}

// seedPartialDestination seeds an open copying campaign whose single legacy
// target has a present, unrecorded destination: an interrupted copy's partial.
func seedPartialDestination(t *testing.T, legacy, name string) {
	t.Helper()
	addVolumeDir(t, legacy, name)
	if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCopying},
	}}); err != nil {
		t.Fatalf("seed copying migration: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(runtime.PrivateHome(), "volumes", name), 0o750); err != nil {
		t.Fatalf("seed partial destination: %v", err)
	}
}

// TestOfferStartFreshRemovesUnfinishedDestination pins that choosing "start
// fresh" after an interrupted copy cannot adopt the half-copied home: the
// unfinished destination is removed before the dismissal is recorded, so the
// home resolver creates a genuinely fresh volume.
func TestOfferStartFreshRemovesUnfinishedDestination(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "partial"
	seedPartialDestination(t, legacy, name)

	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, volumeName string) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: volumeName}, nil
		},
	}
	var prompted bool
	ui := interactiveUI(choiceFresh, &prompted)

	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if !prompted {
		t.Fatal("Offer did not prompt")
	}
	if !slices.Contains(client.RemovedVolumes, name) {
		t.Errorf("RemovedVolumes = %v, want the unfinished destination %q removed", client.RemovedVolumes, name)
	}
	root, _ := state.ReadRootState()
	if m, _ := root.Migration(MigrationName); m.Status != state.MigrationDismissed {
		t.Fatalf("status = %q, want dismissed", m.Status)
	}
}

// TestOfferStartFreshRemovalFailureAborts pins that a failed cleanup blocks the
// dismissal: recording "start fresh" while a usable partial remains would let the
// home resolver adopt the half-copied home under a permanent opt-out.
func TestOfferStartFreshRemovalFailureAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "partial"
	seedPartialDestination(t, legacy, name)

	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, volumeName string) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: volumeName}, nil
		},
		RemoveVolumeFn: func(_ context.Context, _ string) error { return errors.New("remove boom") },
	}
	var prompted bool
	ui := interactiveUI(choiceFresh, &prompted)

	if err := Offer(context.Background(), client, ui); err == nil {
		t.Fatal("Offer() error = nil, want an abort when the partial cannot be removed")
	}
	root, _ := state.ReadRootState()
	if m, _ := root.Migration(MigrationName); m.Status == state.MigrationDismissed {
		t.Fatal("migration was dismissed despite the failed partial cleanup")
	}
}

// TestFinalizeCompletedCampaignTerminalIsNoOp pins that closing out a campaign
// never clobbers a terminal write another session already made.
func TestFinalizeCompletedCampaignTerminalIsNoOp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCompleted},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ui := termio.NewTestMock(t)
	if err := finalizeCompletedCampaign(&ui); err != nil {
		t.Fatalf("finalizeCompletedCampaign: %v", err)
	}
	root, _ := state.ReadRootState()
	if m, _ := root.Migration(MigrationName); m.Status != state.MigrationCompleted {
		t.Fatalf("status = %q, want completed", m.Status)
	}
}

func TestFinalizeCompletedCampaignStateReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeCorruptRootState(t)
	ui := termio.NewTestMock(t)
	if err := finalizeCompletedCampaign(&ui); err == nil {
		t.Fatal("finalizeCompletedCampaign() error = nil, want a state read error")
	}
}

func TestDismissMigrationStateReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeCorruptRootState(t)
	if err := dismissMigration(context.Background(), &msb.MockMsbClient{}); err == nil {
		t.Fatal("dismissMigration() error = nil, want a state read error")
	}
}

// TestRemoveUnclearDestinationsRemovesOnlyUnclear pins the cleanup's
// selectivity: it removes a present, unaccounted target (a partial) but leaves a
// copied target, a user-kept target, and a private volume that is not a legacy
// target.
func TestRemoveUnclearDestinationsRemovesOnlyUnclear(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	partial := naming.HomePrefix + "partial"
	copied := naming.HomePrefix + "copied"
	kept := naming.HomePrefix + "kept"
	addVolumeDir(t, legacy, partial)
	addVolumeDir(t, legacy, copied)
	addVolumeDir(t, legacy, kept)

	privateHome := runtime.PrivateHome()
	for _, name := range []string{partial, copied, kept, naming.HomePrefix + "unrelated"} {
		if err := os.MkdirAll(filepath.Join(privateHome, "volumes", name), 0o750); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}
	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	m := state.Migration{Status: state.MigrationCopying, Copied: []string{copied}, Kept: []string{kept}}
	if err := removeUnclearDestinations(context.Background(), client, m); err != nil {
		t.Fatalf("removeUnclearDestinations: %v", err)
	}
	assertSameSet(t, client.RemovedVolumes, []string{partial})
}

func TestRemoveUnclearDestinationsLegacyHomeError(t *testing.T) {
	configpaths.WithMockConfigPathsHomeError(t, errors.New("home unavailable"))
	if err := removeUnclearDestinations(
		context.Background(), &msb.MockMsbClient{}, state.Migration{Status: state.MigrationCopying},
	); err == nil {
		t.Fatal("removeUnclearDestinations() error = nil, want legacy home resolution error")
	}
}

func TestRemoveUnclearDestinationsLegacyListingError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	makeLegacyVolumesUnlistable(t, legacy)
	if err := removeUnclearDestinations(
		context.Background(), &msb.MockMsbClient{}, state.Migration{Status: state.MigrationCopying},
	); err == nil {
		t.Fatal("removeUnclearDestinations() error = nil, want legacy listing error")
	}
}

func TestRemoveUnclearDestinationsPrivateListingError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"target")

	volumes := filepath.Join(runtime.PrivateHome(), "volumes")
	if err := os.MkdirAll(filepath.Dir(volumes), 0o750); err != nil {
		t.Fatalf("mkdir private home: %v", err)
	}
	if err := os.Symlink("volumes", volumes); err != nil {
		t.Fatalf("symlink private volumes: %v", err)
	}
	if err := removeUnclearDestinations(
		context.Background(), &msb.MockMsbClient{}, state.Migration{Status: state.MigrationCopying},
	); err == nil {
		t.Fatal("removeUnclearDestinations() error = nil, want private listing error")
	}
}

// TestOfferPendingListingErrorWarnsAndSkips pins the other side of finding 3: in
// the pending state an unlistable legacy home is best-effort, so the offer warns
// and skips rather than blocking. A regular file at volumes/ makes the strict
// campaign listing fail (ENOTDIR) while the pending tolerant listing treats it as
// empty, exercising the failure at the present-target listing.
func TestOfferPendingListingErrorWarnsAndSkips(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	volumes := filepath.Join(legacy, "volumes")
	if err := os.RemoveAll(volumes); err != nil {
		t.Fatalf("remove volumes dir: %v", err)
	}
	if err := os.WriteFile(volumes, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file at volumes path: %v", err)
	}

	var prompted bool
	ui := interactiveUI(choiceCopy, &prompted)
	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err != nil {
		t.Fatalf("Offer() error = %v, want nil (best-effort in pending)", err)
	}
	if prompted {
		t.Error("Offer() prompted on a pending listing failure")
	}
	if len(ui.WarnCalls) != 1 {
		t.Errorf("WarnCalls = %v, want exactly one warning", ui.WarnCalls)
	}
}

// TestOfferCopyingListingErrorBlocks pins finding 3: while a campaign is open, an
// unlistable legacy volumes directory is campaign-scoped, so the offer returns
// the error and blocks the session rather than letting it reuse the unfinished
// destination. The pending state keeps its best-effort warn-and-skip (see
// TestOfferNecessaryVolumesErrorWarnsAndSkips).
func TestOfferCopyingListingErrorBlocks(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	makeLegacyVolumesUnlistable(t, legacy)
	if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCopying},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var prompted bool
	ui := interactiveUI(choiceCopy, &prompted)
	if err := Offer(context.Background(), &msb.MockMsbClient{}, ui); err == nil {
		t.Fatal("Offer() error = nil, want the campaign listing failure to block the session")
	}
	if prompted {
		t.Error("Offer() prompted despite the campaign listing failure")
	}
}

// TestOfferChoicesAreCopyFreshQuit pins that "decide later" is gone: the only
// outcomes are copy, start fresh, or quit.
func TestOfferChoicesAreCopyFreshQuit(t *testing.T) {
	choices := offerChoices(state.Migration{}, 0)
	got := make([]string, len(choices))
	for i, c := range choices {
		got[i] = c.Key
	}
	assertSameSet(t, got, []string{choiceCopy, choiceFresh, choiceQuit})
}

func choiceLabels(choices []termio.Choice) []string {
	labels := make([]string, len(choices))
	for i, c := range choices {
		labels[i] = c.Label
	}
	return labels
}

// TestOfferChoicesWording pins the label rule: "Start fresh" only when the
// migration is pending and there is nothing unclear to clean up; otherwise the
// opt-out is worded "Dismiss".
func TestOfferChoicesWording(t *testing.T) {
	t.Run("pending no unclear is start fresh", func(t *testing.T) {
		labels := choiceLabels(offerChoices(state.Migration{}, 0))
		if !slices.Contains(labels, "Start fresh") || slices.Contains(labels, "Dismiss") {
			t.Errorf("labels = %v, want Start fresh, not Dismiss", labels)
		}
	})
	t.Run("pending with unclear is dismiss", func(t *testing.T) {
		labels := choiceLabels(offerChoices(state.Migration{}, 2))
		if !slices.Contains(labels, "Dismiss") || slices.Contains(labels, "Start fresh") {
			t.Errorf("labels = %v, want Dismiss, not Start fresh", labels)
		}
	})
	t.Run("copying is dismiss", func(t *testing.T) {
		labels := choiceLabels(offerChoices(state.Migration{Status: state.MigrationCopying}, 0))
		if !slices.Contains(labels, "Dismiss") || slices.Contains(labels, "Start fresh") {
			t.Errorf("labels = %v, want Dismiss, not Start fresh", labels)
		}
	})
}

// TestOfferChoicesCopyingResumeWording pins that while a campaign is open the
// copy option is a resume that discloses the re-copy: unlike the pending
// ambiguous state (which records present volumes as kept), an open campaign's
// present-unaccounted volume is an interrupted partial that CopyVolumes discards
// and re-copies. The label must not promise to leave it untouched.
func TestOfferChoicesCopyingResumeWording(t *testing.T) {
	choices := offerChoices(state.Migration{Status: state.MigrationCopying}, 1)
	byKey := map[string]termio.Choice{}
	for _, c := range choices {
		byKey[c.Key] = c
	}
	copyChoice := byKey[choiceCopy]
	if copyChoice.Label != "Resume the copy" {
		t.Errorf("copy label = %q, want %q", copyChoice.Label, "Resume the copy")
	}
	if !strings.Contains(copyChoice.Description, "re-copy") {
		t.Errorf("copy description = %q, want it to disclose re-copying", copyChoice.Description)
	}
	if strings.Contains(copyChoice.Description, "left untouched") {
		t.Errorf("copy description = %q, must not promise untouched volumes while copying", copyChoice.Description)
	}
}

// TestOfferPromptIncludesUnclearNotice pins the ambiguous-state instructions: the
// prompt explains the unclear volumes and the `volume remove --force` cleanup
// path, and omits them when there is nothing unclear.
func TestOfferPromptIncludesUnclearNotice(t *testing.T) {
	plain := offerPrompt(state.Migration{}, 1, 0)
	if strings.Contains(plain, "unclear") {
		t.Errorf("plain prompt mentions unclear volumes: %q", plain)
	}
	pending := offerPrompt(state.Migration{}, 1, 2)
	for _, want := range []string{"unclear", "volume remove --force", "Quit"} {
		if !strings.Contains(pending, want) {
			t.Errorf("pending prompt %q missing %q", pending, want)
		}
	}
}

// TestOfferPromptCopyingResumeNotice pins that in the copying state the notice
// describes re-copying the interrupted volumes rather than the pending
// ambiguous-state "leave them untouched / volume remove --force" instructions.
func TestOfferPromptCopyingResumeNotice(t *testing.T) {
	resume := offerPrompt(state.Migration{Status: state.MigrationCopying}, 1, 2)
	for _, want := range []string{"interrupted", "re-cop"} {
		if !strings.Contains(resume, want) {
			t.Errorf("resume prompt %q missing %q", resume, want)
		}
	}
	for _, unwanted := range []string{"unclear", "left untouched", "volume remove --force"} {
		if strings.Contains(resume, unwanted) {
			t.Errorf("resume prompt %q must not contain %q", resume, unwanted)
		}
	}
}

// TestFinalizeMigrationWarnsExcluded pins that finalization warns about the
// volumes the campaign could not copy, then records completed.
func TestFinalizeMigrationWarnsExcluded(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	m := state.Migration{Status: state.MigrationCopying, Excluded: []string{naming.HomePrefix + "bad"}}
	if _, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		return finalizeMigration(tx, m, &ui)
	}); err != nil {
		t.Fatalf("finalizeMigration: %v", err)
	}
	if len(ui.WarnCalls) != 1 {
		t.Fatalf("WarnCalls = %v, want one warning about the uncopied volumes", ui.WarnCalls)
	}
	root, _ := state.ReadRootState()
	if got, _ := root.Migration(MigrationName); got.Status != state.MigrationCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
}

// TestOfferAmbiguousStateCopyKeepsExisting pins option 1 in the ambiguous state:
// a present private volume of unclear status is recorded as Kept and left
// untouched while only the missing volumes are copied.
func TestOfferAmbiguousStateCopyKeepsExisting(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	present := naming.HomePrefix + "present"
	missing := naming.HomePrefix + "missing"
	addVolumeDir(t, legacy, present)
	addVolumeDir(t, legacy, missing)
	if err := os.MkdirAll(filepath.Join(runtime.PrivateHome(), "volumes", present), 0o750); err != nil {
		t.Fatalf("seed present private volume: %v", err)
	}

	target := t.TempDir()
	var created []string
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, n string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			created = append(created, n)
			return &msb.MockVolumeHandle{Name_: n, Path_: target}, nil
		},
	}
	var prompted bool
	ui := interactiveUI(choiceCopy, &prompted)
	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if !prompted {
		t.Fatal("Offer did not prompt in the ambiguous state")
	}
	if len(created) != 1 || created[0] != missing {
		t.Errorf("created = %v, want only %q", created, missing)
	}
	if slices.Contains(client.RemovedVolumes, present) {
		t.Errorf("RemovedVolumes = %v, want present %q kept", client.RemovedVolumes, present)
	}
	root, _ := state.ReadRootState()
	if got, _ := root.Migration(MigrationName); got.Status != state.MigrationCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
}

// TestOfferCopyingResumeRecopiesPartial pins the behavior behind the resume
// wording: while a campaign is open, accepting "Resume the copy" discards an
// interrupted partial destination and re-copies it, reaching completed.
func TestOfferCopyingResumeRecopiesPartial(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "partial"
	seedPartialDestination(t, legacy, name)

	target := t.TempDir()
	var created []string
	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, volumeName string) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: volumeName}, nil
		},
		CreateVolumeFn: func(_ context.Context, n string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			created = append(created, n)
			return &msb.MockVolumeHandle{Name_: n, Path_: target}, nil
		},
	}
	var prompted bool
	ui := interactiveUI(choiceCopy, &prompted)

	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if !prompted {
		t.Fatal("Offer did not prompt while copying")
	}
	if !slices.Contains(client.RemovedVolumes, name) {
		t.Errorf("RemovedVolumes = %v, want the interrupted partial %q discarded", client.RemovedVolumes, name)
	}
	if len(created) != 1 || created[0] != name {
		t.Errorf("created = %v, want the partial %q re-copied", created, name)
	}
	root, _ := state.ReadRootState()
	if got, _ := root.Migration(MigrationName); got.Status != state.MigrationCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
}

// TestOfferAmbiguousStateDismissRemovesUnclear pins option 2 in the ambiguous
// state: "Dismiss" removes the unclear private volume before recording the
// permanent opt-out, so the home resolver never adopts it.
func TestOfferAmbiguousStateDismissRemovesUnclear(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "unclear"
	addVolumeDir(t, legacy, name)
	if err := os.MkdirAll(filepath.Join(runtime.PrivateHome(), "volumes", name), 0o750); err != nil {
		t.Fatalf("seed unclear private volume: %v", err)
	}
	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, volumeName string) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: volumeName}, nil
		},
	}
	var prompted bool
	ui := interactiveUI(choiceFresh, &prompted)
	if err := Offer(context.Background(), client, ui); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if !slices.Contains(client.RemovedVolumes, name) {
		t.Errorf("RemovedVolumes = %v, want unclear %q removed", client.RemovedVolumes, name)
	}
	root, _ := state.ReadRootState()
	if got, _ := root.Migration(MigrationName); got.Status != state.MigrationDismissed {
		t.Fatalf("status = %q, want dismissed", got.Status)
	}
}
