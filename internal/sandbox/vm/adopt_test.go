package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/hostlock"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestAdoptLegacyIdentityFailsWhenALaterLegacySlugIsUnreadable pins the
// lock-set derivation failure: the pending state is under the first legacy slug,
// but a later legacy slug cannot be enumerated, so adoption aborts before
// destroying or moving anything.
func TestAdoptLegacyIdentityFailsWhenALaterLegacySlugIsUnreadable(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)
	const badSlug = "workspace-bad"
	testutil.WritePath(t, state.SlugDir(badSlug), "not a directory")

	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{adoptRepoSlug, badSlug}

	client := &msbtest.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), client, identity, adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected an unreadable later legacy slug to abort adoption")
	}
	if !strings.Contains(err.Error(), "derive adoption lock set") {
		t.Errorf("error = %v, want the lock-set derivation failure", err)
	}
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("error = %v, want the wrapped unreadable-slug cause preserved", err)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved despite the failed lock-set derivation")
	}
}

func TestAdoptLegacyIdentityHoldsKeyLocksAcrossTheMove(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)
	sibling := state.Key{Slug: adoptRepoSlug, Agent: "pi"}
	if err := seedHomeState(sibling, state.HomeState{HomeVolume: "vol-pi"}); err != nil {
		t.Fatal(err)
	}

	var lockedCalls int
	adoptKeysLockedHook = func() {
		lockedCalls++
		// The sibling's home lock is held: re-entering WithKeyLocks fails fast.
		if err := state.WithKeyLocks([]state.Key{sibling}, func() error { return nil }); err == nil ||
			!strings.Contains(err.Error(), "already held") {
			t.Errorf("sibling home lock not held during the move: %v", err)
		}
		// The checkout key's project-VM lock is held.
		assertProjectVMLockHeld(t, adoptCheckoutKey())
	}
	t.Cleanup(func() { adoptKeysLockedHook = nil })

	client := &msbtest.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if lockedCalls != 2 {
		t.Fatalf("locked-region hook invoked %d times, want 2 (before the destroy and after the move)", lockedCalls)
	}
	// Every lock is released once adoption returns.
	if err := state.WithKeyLocks([]state.Key{adoptCheckoutKey(), sibling}, func() error { return nil }); err != nil {
		t.Fatalf("adoption leaked a key lock: %v", err)
	}
}

func TestAdoptLegacyIdentityReleasesKeyLocksOnDestroyFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName(), DestroyErr: errors.New("destroy boom")}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected the destroy failure to abort adoption")
	}
	if err := state.WithKeyLocks(
		[]state.Key{adoptRepoKey(), adoptCheckoutKey()},
		func() error { return nil },
	); err != nil {
		t.Fatalf("a failed adoption leaked a key lock: %v", err)
	}
}

// TestAdoptLegacyIdentityPersistRecoveredVolumeFailureFailsClosed pins the
// marker write failing after the claim committed: adoption aborts and, crucially,
// leaves the legacy VM running so the volume is not lost.
func TestAdoptLegacyIdentityPersistRecoveredVolumeFailureFailsClosed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	// The checkout state directory is read-only, so the marker write cannot
	// create its temp file. The read still sees a missing file (the trigger).
	keyDir := state.KeyDir(adoptCheckoutKey())
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyDir, 0o555); err != nil {
		t.Skipf("cannot make the checkout state dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(keyDir, 0o700) })

	handle := legacyVMWithHome()
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected the marker write failure to abort adoption")
	}
	if handle.Destroyed {
		t.Error("the legacy VM must survive a failed marker write")
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("a failed marker write must not leave a checkout state file")
	}
	// The claim commits before the marker, so the volume is already owned and
	// recoverable even though the marker write failed.
	root, rootErr := state.ReadRootState()
	if rootErr != nil {
		t.Fatalf("ReadRootState: %v", rootErr)
	}
	if _, ok := root.AdoptedHomeVolume(adoptRepoSlug, adoptAgentName); !ok {
		t.Error("the claim must commit before the marker, leaving the volume owned on a marker failure")
	}
}

// TestAdoptLegacyIdentityClaimCommitFailureFailsClosed pins the claim commit
// failing: the root-state write is the first durable step of a VM-only adoption,
// so its failure aborts before the marker and before the destroy.
func TestAdoptLegacyIdentityClaimCommitFailureFailsClosed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	// Pre-create the root lock so a read-only state root still lets the
	// transaction acquire its lock and read (a missing root state file), leaving
	// only the final atomic write to fail. Pre-create the checkout state
	// directory too, so on the old ordering the marker write would succeed and the
	// assertion below would catch it.
	root := configpaths.Get().UserStateDir()
	lockPath := filepath.Join(root, "locks", "root.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state.KeyDir(adoptCheckoutKey()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Skipf("cannot make the state root read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	handle := legacyVMWithHome()
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err == nil {
		t.Fatal("expected the claim commit failure to abort adoption")
	}
	if !errors.As(err, new(*os.PathError)) {
		t.Errorf("error = %v, want it to preserve the filesystem cause", err)
	}
	if handle.Destroyed {
		t.Error("the legacy VM must survive a failed claim commit")
	}
	// The claim commits before the marker, so a failed commit must not leave the
	// recovery marker behind (which would let a second clone recover the volume).
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("a failed claim commit must not leave a recovery marker")
	}
}

// TestAdoptLegacyIdentityClaimVolumeMismatchFailsClosed pins the reconciled-
// claim guard: a claim this checkout owns but that names a different volume than
// the live legacy VM mounts cannot be trusted, so adoption refuses rather than
// destroying the VM and overwriting the record.
func TestAdoptLegacyIdentityClaimVolumeMismatchFailsClosed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if err := seedRootState(state.RootState{AdoptedHomeVolumes: map[string]state.AdoptedHomeVolume{
		"widget-aaa/opencode": {CheckoutSlug: adoptCheckoutSlug, Volume: "some-other-volume"},
	}}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	handle := legacyVMWithHome()
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err == nil {
		t.Fatal("expected a mismatched adoption claim to fail closed")
	}
	if !strings.Contains(err.Error(), "refusing to destroy it") {
		t.Errorf("error = %v, want the fail-closed refusal", err)
	}
	if handle.Destroyed {
		t.Error("the legacy VM must survive a mismatched adoption claim")
	}
}

// TestAdoptLegacyIdentityRestoresOtherOwnersMarkerBeforeDestroy pins the
// stranded-volume hole: checkout A claimed the shared legacy home but failed to
// record its recovery marker, so when checkout B adopts the same live VM it must
// materialize A's marker from the claim before destroying the VM (the volume's
// only other reference). B still starts with its own fresh volume.
func TestAdoptLegacyIdentityRestoresOtherOwnersMarkerBeforeDestroy(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if err := seedRootState(state.RootState{AdoptedHomeVolumes: map[string]state.AdoptedHomeVolume{
		"widget-aaa/opencode": {CheckoutSlug: adoptOtherOwnerSlug, Volume: adoptLegacyVolume},
	}}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	handle := legacyVMWithHome()
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if !handle.Destroyed {
		t.Error("the legacy VM must be destroyed by the checkout that does not own the volume")
	}

	// The owner's recovery reference is durable, so the volume survives even if
	// the owner never runs again.
	ownerState, err := state.ReadState(adoptOtherOwnerKey())
	if err != nil {
		t.Fatalf("ReadState(owner): %v", err)
	}
	if ownerState.RecoveredHomeVolume != adoptLegacyVolume {
		t.Errorf("owner RecoveredHomeVolume = %q, want %q", ownerState.RecoveredHomeVolume, adoptLegacyVolume)
	}
	// The adopting checkout must not share the owner's volume.
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("the non-owning checkout must not record a recovery marker")
	}
}

// TestAdoptLegacyIdentityOtherOwnerClaimVolumeMismatchFailsClosed pins the
// mismatch guard for a claim held by another checkout: the claim names a
// different volume than the live VM mounts, so destroying the VM could strand
// whatever it actually holds, and adoption refuses.
func TestAdoptLegacyIdentityOtherOwnerClaimVolumeMismatchFailsClosed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if err := seedRootState(state.RootState{AdoptedHomeVolumes: map[string]state.AdoptedHomeVolume{
		"widget-aaa/opencode": {CheckoutSlug: adoptOtherOwnerSlug, Volume: "some-other-volume"},
	}}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	handle := legacyVMWithHome()
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err == nil {
		t.Fatal("expected a mismatched other-owner claim to fail closed")
	}
	if !strings.Contains(err.Error(), "refusing to destroy it") {
		t.Errorf("error = %v, want the fail-closed refusal", err)
	}
	if handle.Destroyed {
		t.Error("the legacy VM must survive a mismatched other-owner claim")
	}
	if statePresent(t, adoptOtherOwnerKey()) {
		t.Error("a refused adoption must not write the owner's marker")
	}
}

// TestAdoptLegacyIdentityOtherOwnerMarkerRestoreFailureFailsClosed pins the
// owner's marker restore failing: the VM's only other reference could not be
// made durable, so adoption must not destroy the VM and strand the volume.
func TestAdoptLegacyIdentityOtherOwnerMarkerRestoreFailureFailsClosed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if err := seedRootState(state.RootState{AdoptedHomeVolumes: map[string]state.AdoptedHomeVolume{
		"widget-aaa/opencode": {CheckoutSlug: adoptOtherOwnerSlug, Volume: adoptLegacyVolume},
	}}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	// A directory at the owner's state-file path makes the marker write fail with
	// a non-repairable read error.
	if err := os.MkdirAll(filepath.Join(state.KeyDir(adoptOtherOwnerKey()), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	handle := legacyVMWithHome()
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected the owner's marker restore failure to abort adoption")
	}
	if handle.Destroyed {
		t.Error("the legacy VM must survive a failed owner-marker restore")
	}
}

// TestAdoptLegacyIdentityRecheckErrorPropagates pins the locked re-check
// failing: the legacy state became unreadable while this run waited for the root
// lock, so adoption reports it instead of proceeding on unknown state.
func TestAdoptLegacyIdentityRecheckErrorPropagates(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	legacyStatePath := seedLegacyState(t)
	adoptLockHook = func() {
		if err := os.WriteFile(legacyStatePath, []byte("{ corrupt: yaml: ["), 0o600); err != nil {
			t.Errorf("corrupt legacy state: %v", err)
		}
	}
	t.Cleanup(func() { adoptLockHook = nil })

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err == nil {
		t.Fatal("expected the locked re-check to propagate the unreadable legacy state")
	}
	if !strings.Contains(err.Error(), "check for a pending legacy identity") {
		t.Errorf("error = %v, want the pending-identity check failure", err)
	}
	if !errors.Is(err, state.ErrStateUnparsable) {
		t.Errorf("error = %v, want it to preserve the unparsable-state cause", err)
	}
}

// TestAdoptLegacyIdentityLiveLegacyVMWithoutHomeMountSkipsTheClaim pins the guard
// on the VM-only arm: a legacy VM with no home mount has nothing to preserve, so
// adoption destroys it without claiming a volume or recording a recovery marker.
func TestAdoptLegacyIdentityLiveLegacyVMWithoutHomeMountSkipsTheClaim(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	handle := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName()}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), legacyVMNotFound(handle),
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if !handle.Destroyed {
		t.Error("a legacy VM with no home mount must still be destroyed")
	}
	if !got.Applied {
		t.Errorf("adoption = %+v, want it applied", got)
	}
	root, rootErr := state.ReadRootState()
	if rootErr != nil {
		t.Fatalf("ReadRootState: %v", rootErr)
	}
	if claim, ok := root.AdoptedHomeVolume(adoptRepoSlug, adoptAgentName); ok {
		t.Errorf("a legacy VM with no home mount must not claim a volume: %+v", claim)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("a legacy VM with no home mount must not record a recovery marker")
	}
}

// TestAdoptLegacyIdentityMovesStateAndDestroysVM pins the happy path: the
// legacy VM is destroyed and the legacy state directory merges onto the
// checkout slug, so the caller continues under the checkout identity.
func TestAdoptLegacyIdentityMovesStateAndDestroysVM(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName()}
	var requested string
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		requested = name
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if requested != adoptLegacyVMName() {
		t.Errorf("GetSandbox name = %q, want %q", requested, adoptLegacyVMName())
	}
	if !handle.Destroyed {
		t.Error("legacy VM was not destroyed")
	}
	var sawDestroyed bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "destroyed legacy project VM") {
			sawDestroyed = true
		}
	}
	if !sawDestroyed {
		t.Errorf("expected the destroyed-VM verbose note; got %v", ui.VerboseCalls)
	}
	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("no state file under the checkout slug after adoption")
	}
	if _, statErr := os.Stat(state.SlugDir(adoptRepoSlug)); !os.IsNotExist(statErr) {
		t.Errorf("legacy slug dir still present (stat error = %v)", statErr)
	}
	want := IdentityAdoption{Slug: adoptCheckoutSlug, Pending: true, Applied: true}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestAdoptLegacyIdentityNoticeNamesEveryPath pins the notice's content: it is
// the only place the user learns where their state went, so every moved or
// recreated path has to appear in the block.
func TestAdoptLegacyIdentityNoticeNamesEveryPath(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	notice := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{
		"Migrating this checkout to its own project identity",
		state.SlugDir(adoptRepoSlug),
		state.SlugDir(adoptCheckoutSlug),
		adoptLegacyVMName(),
		ProjectVMName(adoptCheckoutKey()),
		adoptLegacyVolume,
		naming.RunnerImageRef(adoptRepoSlug, adoptAgentName),
		naming.RunnerImageRef(adoptCheckoutSlug, adoptAgentName),
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice missing %q:\n%s", want, notice)
		}
	}
}

// TestAdoptLegacyIdentityNoticeWithoutHomeVolume pins the placeholder the notice
// renders when the legacy state records no home volume, so the row never shows
// an empty value.
func TestAdoptLegacyIdentityNoticeWithoutHomeVolume(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	if err := seedHomeState(adoptRepoKey(), state.HomeState{}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called during a dry run", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if notice := strings.Join(ui.WarnCalls, "\n"); !strings.Contains(notice, "(none recorded)") {
		t.Errorf("notice missing the empty home-volume placeholder:\n%s", notice)
	}
	if !got.Pending {
		t.Errorf("adoption = %+v, want a pending migration", got)
	}
}

// failIfAdoptionLocks fails the test if the adoption takes its root-state
// lock: a run with nothing to adopt must not wait behind another lock holder.
func failIfAdoptionLocks(t *testing.T) {
	t.Helper()
	adoptLockHook = func() { t.Error("took the adoption lock with nothing to adopt") }
	t.Cleanup(func() { adoptLockHook = nil })
}

// TestAdoptLegacyIdentityNoLegacyState pins a project that never had the
// legacy identity: no legacy state file and no surviving legacy VM, so nothing is
// locked, destroyed, moved or printed.
func TestAdoptLegacyIdentityNoLegacyState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	failIfAdoptionLocks(t)

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no legacy VM"}
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if got != (IdentityAdoption{Slug: adoptCheckoutSlug}) {
		t.Errorf("adoption = %+v, want the checkout slug with nothing pending", got)
	}
	if len(ui.WarnCalls) != 0 || len(ui.InfoCalls) != 0 || len(ui.VerboseCalls) != 0 ||
		len(ui.OutCalls) != 0 || len(ui.PrintCalls) != 0 {
		t.Errorf("expected no output, got warn=%v info=%v verbose=%v out=%v print=%v",
			ui.WarnCalls, ui.InfoCalls, ui.VerboseCalls, ui.OutCalls, ui.PrintCalls)
	}
}

// TestAdoptLegacyIdentityAlreadyAtCheckoutSlug pins the re-run case: a
// checkout-keyed state file makes the trigger false even while the legacy
// directory still exists, so the legacy state is deliberately left alone and
// nothing is locked.
func TestAdoptLegacyIdentityAlreadyAtCheckoutSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	failIfAdoptionLocks(t)
	legacyStateFile := seedLegacyState(t)
	if err := seedHomeState(adoptCheckoutKey(), state.HomeState{HomeVolume: "home-bbb"}); err != nil {
		t.Fatalf("seed checkout state: %v", err)
	}

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called for an already-adopted project", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if got != (IdentityAdoption{Slug: adoptCheckoutSlug}) {
		t.Errorf("adoption = %+v, want a no-op at the checkout slug", got)
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state file moved by a no-op adoption: %v", statErr)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("expected no notice, got %v", ui.WarnCalls)
	}
}

// TestAdoptLegacyIdentityUnparsableLegacyState verifies the fail-closed
// predicate: an unreadable legacy document aborts adoption before anything is
// destroyed or moved, because the caller cannot know what it would be adopting.
func TestAdoptLegacyIdentityUnparsableLegacyState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacyDir := filepath.Join(state.SlugDir(adoptRepoSlug), adoptAgentName)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy state dir: %v", err)
	}
	testutil.WriteFile(t, legacyDir, "state.yaml", `": [`)

	handle := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected an unparsable legacy state to fail adoption")
	} else if !errors.Is(err, state.ErrStateUnparsable) {
		t.Errorf("error = %v, want it to preserve the unparsable-state cause", err)
	}

	if handle.Destroyed {
		t.Error("legacy VM destroyed despite an unparsable legacy state")
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved despite an unparsable legacy state")
	}
	if _, statErr := os.Stat(filepath.Join(legacyDir, "state.yaml")); statErr != nil {
		t.Errorf("legacy state file removed: %v", statErr)
	}
}

// TestAdoptLegacyIdentityApplyFalseIsReadOnly pins the dry run: the notice is
// printed under the "would" headline, nothing is touched, and the caller is told
// to keep resolving against the legacy slug.
func TestAdoptLegacyIdentityApplyFalseIsReadOnly(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called during a dry run", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	notice := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{
		"Would migrate this checkout to its own project identity",
		state.SlugDir(adoptCheckoutSlug),
		adoptLegacyVolume,
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("dry-run notice missing %q:\n%s", want, notice)
		}
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved during a dry run")
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state removed during a dry run: %v", statErr)
	}
	want := IdentityAdoption{Slug: adoptRepoSlug, Pending: true, Applied: false}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestAdoptLegacyIdentityLegacyVMAlreadyGone verifies that an absent legacy VM
// does not block adoption: the state move is the part that matters.
func TestAdoptLegacyIdentityLegacyVMAlreadyGone(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("state not moved although the legacy VM was already gone")
	}
	if got.Applied != true {
		t.Errorf("adoption = %+v, want it applied", got)
	}
	var sawAbsentLegacyVM bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "legacy project VM") && strings.Contains(call, "is already gone") {
			sawAbsentLegacyVM = true
		}
	}
	if !sawAbsentLegacyVM {
		t.Errorf("expected the absent-legacy-VM verbose note; got %v", ui.VerboseCalls)
	}
}

// TestAdoptLegacyIdentityLegacyVMReplacedFailsClosed verifies the
// identity-aware destroy: the name now belongs to a sandbox this run did not
// create, so the VM is left alone and the state is not moved.
func TestAdoptLegacyIdentityLegacyVMReplacedFailsClosed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msbtest.MockSandboxHandle{
		Name_:      "widget-aaa--renamed-sandbox",
		DestroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"},
	}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err == nil {
		t.Fatal("expected a replaced legacy VM to fail adoption")
	}
	if !msb.IsReplaced(err) {
		t.Errorf("error = %v, want it to wrap ErrSandboxReplaced", err)
	}
	if !strings.Contains(err.Error(), "rerun to finish migrating this checkout") {
		t.Errorf("error = %v, want the adoption action clause from destroyProjectVMHandle", err)
	}
	if !strings.Contains(err.Error(), handle.Name()) {
		t.Errorf("error = %v, want it to name the resolved handle %q", err, handle.Name())
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved despite a replaced legacy VM")
	}
	if !statePresent(t, adoptRepoKey()) {
		t.Error("legacy state removed despite a replaced legacy VM")
	}
}

// TestAdoptLegacyIdentityStrandedStateFileFailsLoudly pins the mandatory
// post-move check. A dangling symlink at the destination agent directory makes
// the merge refuse to overwrite it and report a skip, so the legacy state file
// never arrives. Continuing would run under the checkout identity with no state
// — a fresh home volume and the agent's auth silently gone — so adoption must
// fail loudly, naming what is half-done and what to clear to finish it.
func TestAdoptLegacyIdentityStrandedStateFileFailsLoudly(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)

	checkoutSlugDir := state.SlugDir(adoptCheckoutSlug)
	if err := os.MkdirAll(checkoutSlugDir, 0o700); err != nil {
		t.Fatalf("create checkout state dir: %v", err)
	}
	if err := os.Symlink(
		filepath.Join(t.TempDir(), "nowhere"),
		filepath.Join(checkoutSlugDir, adoptAgentName),
	); err != nil {
		t.Fatalf("create blocking symlink: %v", err)
	}

	handle := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err == nil {
		t.Fatal("expected a stranded state file to fail adoption")
	}
	for _, want := range []string{
		"did not complete",
		legacyStateFile,
		"skipped [" + adoptAgentName + "]",
		adoptLegacyVMName() + " is already gone",
		checkoutSlugDir,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("error = %v, want it to preserve the missing-state cause", err)
	}
	var sawSkipWarning bool
	for _, call := range ui.WarnCalls {
		if strings.Contains(call, "left "+adoptAgentName+" in the legacy state directory") {
			sawSkipWarning = true
		}
	}
	if !sawSkipWarning {
		t.Errorf("expected the skipped-entry warning; got %v", ui.WarnCalls)
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state file gone after a loud failure: %v", statErr)
	}
}

// TestAdoptLegacyIdentityUnparsableCheckoutStateSkipsMigration pins the
// skip-don't-block predicate on the checkout side: a corrupt checkout-keyed
// document must not abort the run before the recovery path (RecoverHomeState) can
// repair it, so adoption reports nothing pending and leaves the legacy VM and
// state untouched.
func TestAdoptLegacyIdentityUnparsableCheckoutStateSkipsMigration(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)
	checkoutDir := state.KeyDir(adoptCheckoutKey())
	if err := os.MkdirAll(checkoutDir, 0o700); err != nil {
		t.Fatalf("create checkout state dir: %v", err)
	}
	testutil.WriteFile(t, checkoutDir, "state.yaml", `": [`)

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called despite an unreadable checkout state", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termiotest.NewTestMock(t)

	adoption, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("a corrupt checkout state must not abort the run: %v", err)
	}
	if adoption.Pending || adoption.Applied {
		t.Errorf("adoption = %+v, want nothing pending", adoption)
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state file gone after a skipped migration: %v", statErr)
	}
}

// TestAdoptLegacyIdentityNonRepoCorruptStateDoesNotBlock pins the empty-legacy
// case: outside a repository there is no legacy identity to adopt, so a corrupt
// checkout state must not block the run's own recovery path.
func TestAdoptLegacyIdentityNonRepoCorruptStateDoesNotBlock(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	// Both slugs equal for a non-repo directory, so legacySlugs is empty.
	identity := git.Identity{RepoSlug: "workspace-ccc", CheckoutSlug: "workspace-ccc"}
	key := state.Key{Slug: "workspace-ccc", Agent: adoptAgentName}
	if err := os.MkdirAll(state.KeyDir(key), 0o700); err != nil {
		t.Fatalf("create checkout state dir: %v", err)
	}
	testutil.WriteFile(t, state.KeyDir(key), "state.yaml", `": [`)

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called with no legacy identity", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termiotest.NewTestMock(t)

	adoption, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		identity,
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("a corrupt state outside a repo must not block the run: %v", err)
	}
	if adoption.Pending || adoption.Applied {
		t.Errorf("adoption = %+v, want nothing pending", adoption)
	}
}

// TestAdoptLegacyIdentityStateMoveFailureFailsLoudly pins the state-move
// failure: the trigger sees no checkout state file through a dangling
// destination symlink, but the merge then cannot create the destination, so
// adoption fails loudly instead of continuing with no state.
func TestAdoptLegacyIdentityStateMoveFailureFailsLoudly(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)
	if err := os.Symlink(
		filepath.Join(t.TempDir(), "nowhere"),
		state.SlugDir(adoptCheckoutSlug),
	); err != nil {
		t.Fatalf("create dangling destination symlink: %v", err)
	}

	handle := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err == nil {
		t.Fatal("expected a failed state move to fail adoption")
	}
	if !strings.Contains(err.Error(), "move project state to") {
		t.Errorf("error = %v, want the state-move failure", err)
	}
	if !errors.As(err, new(*os.PathError)) {
		t.Errorf("error = %v, want it to preserve the filesystem cause", err)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state present after a failed move")
	}
}

// TestAdoptLegacyIdentityGetSandboxErrorAborts pins a non-not-found failure
// resolving the legacy VM: adoption must abort rather than move state and leave
// an unknown VM running against the shared home volume.
func TestAdoptLegacyIdentityGetSandboxErrorAborts(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, errLegacyVMBoom
	}}
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err == nil {
		t.Fatal("expected a GetSandbox failure to abort adoption")
	}
	if !strings.Contains(err.Error(), "check legacy project VM") {
		t.Errorf("error = %v, want the legacy-VM check failure", err)
	}
	if !errors.Is(err, errLegacyVMBoom) {
		t.Errorf("error = %v, want it to wrap the GetSandbox cause", err)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved despite a failed legacy-VM check")
	}
}

// TestAdoptLegacyIdentityLegacyVMAlreadyGoneOnDestroy pins the identity-safe
// teardown's already-gone outcome: the name vanished between the check and the
// destroy, so the move continues and the caller is told.
func TestAdoptLegacyIdentityLegacyVMAlreadyGoneOnDestroy(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msbtest.MockSandboxHandle{
		Name_:      adoptLegacyVMName(),
		DestroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"},
	}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("state not moved although the legacy VM vanished before the destroy")
	}
	if !got.Applied {
		t.Errorf("adoption = %+v, want it applied", got)
	}
	var sawNote bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "was already gone; continuing") {
			sawNote = true
		}
	}
	if !sawNote {
		t.Errorf("expected the already-gone verbose note; got %v", ui.VerboseCalls)
	}
}

// TestAdoptLegacyIdentityDestroysEveryMovedAgentsVM pins the multi-agent case:
// moving the slug directory carries every agent's state, so every moved agent's
// legacy VM must be destroyed — not only the one the command named — or the
// left-behind sibling keeps writing the shared home volume under its old name.
func TestAdoptLegacyIdentityDestroysEveryMovedAgentsVM(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)
	piKey := state.Key{Slug: adoptRepoSlug, Agent: "pi"}
	if err := seedHomeState(piKey, state.HomeState{HomeVolume: "home-pi"}); err != nil {
		t.Fatalf("seed pi state: %v", err)
	}

	handles := map[string]*msbtest.MockSandboxHandle{}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		handle := &msbtest.MockSandboxHandle{Name_: name}
		handles[name] = handle
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	for _, name := range []string{ProjectVMName(adoptRepoKey()), ProjectVMName(piKey)} {
		handle, ok := handles[name]
		if !ok {
			t.Errorf("GetSandbox never called for %q", name)
			continue
		}
		if !handle.Destroyed {
			t.Errorf("legacy VM %q was not destroyed", name)
		}
	}
	if !statePresent(t, state.Key{Slug: adoptCheckoutSlug, Agent: "pi"}) {
		t.Error("pi state was not moved to the checkout slug")
	}
	if !got.Applied {
		t.Errorf("adoption = %+v, want it applied", got)
	}
}

// TestAdoptLegacyIdentityLosingTheLockRaceContinues pins two runs racing for the
// shared legacy identity: the loser re-checks the trigger under the root lock,
// sees the state already moved, and continues under the checkout identity instead
// of tripping the incomplete-migration failure on a source that is already gone.
func TestAdoptLegacyIdentityLosingTheLockRaceContinues(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)

	adoptLockHook = func() {
		if err := seedHomeState(adoptCheckoutKey(), state.HomeState{HomeVolume: "home-bbb"}); err != nil {
			t.Errorf("seed checkout state inside the lock hook: %v", err)
		}
	}
	t.Cleanup(func() { adoptLockHook = nil })

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called although another adopter won the lock", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	want := IdentityAdoption{Slug: adoptCheckoutSlug}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestAdoptLegacyIdentityAdoptsBareRootLegacySlug pins the bare root's second
// legacy identity: state under the pre-split path-derived slug is moved onto the
// shared checkout slug and its legacy VM is destroyed, even though the
// origin-derived repo slug has no state at all.
func TestAdoptLegacyIdentityAdoptsBareRootLegacySlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const legacySlug = "workspace-aaa"

	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{legacySlug}
	legacyKey := state.Key{Slug: legacySlug, Agent: adoptAgentName}
	if err := seedHomeState(legacyKey, state.HomeState{HomeVolume: "home-legacy"}); err != nil {
		t.Fatalf("seed bare-root legacy state: %v", err)
	}

	handles := map[string]*msbtest.MockSandboxHandle{}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		handle := &msbtest.MockSandboxHandle{Name_: name}
		handles[name] = handle
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		identity,
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("bare-root legacy state was not moved to the checkout slug")
	}
	if handle := handles[ProjectVMName(legacyKey)]; handle == nil || !handle.Destroyed {
		t.Errorf("bare-root legacy VM was not destroyed (handles=%v)", handles)
	}
	want := IdentityAdoption{Slug: adoptCheckoutSlug, Pending: true, Applied: true}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestAdoptLegacyIdentityRecheckCorruptStateSkips pins the re-check under the
// lock: if the checkout state becomes unreadable while this run waited for the
// lock, adoption skips rather than proceeding on a document it cannot read, and
// leaves the legacy VM untouched for the caller's recovery path.
func TestAdoptLegacyIdentityRecheckCorruptStateSkips(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	seedLegacyState(t)

	adoptLockHook = func() {
		dir := state.KeyDir(adoptCheckoutKey())
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Errorf("mkdir checkout state dir: %v", err)
			return
		}
		testutil.WriteFile(t, dir, "state.yaml", `": [`)
	}
	t.Cleanup(func() { adoptLockHook = nil })

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called despite an unreadable re-check", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termiotest.NewTestMock(t)

	adoption, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("a corrupt re-check must not abort the run: %v", err)
	}
	if adoption.Pending || adoption.Applied {
		t.Errorf("adoption = %+v, want nothing pending", adoption)
	}
}

// TestAdoptLegacyIdentityConfigMoveFailurePropagates pins the caller's
// propagation: a failed config move aborts the adoption after the state move.
func TestAdoptLegacyIdentityConfigMoveFailurePropagates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	configpathstest.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "x: 1\n")
	if err := seedHomeState(
		state.Key{Slug: "workspace-aaa", Agent: adoptAgentName},
		state.HomeState{HomeVolume: "home-legacy"},
	); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	renameConfigDir = func(_, _ string) error { return errRenameBoom }
	t.Cleanup(func() { renameConfigDir = os.Rename })

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{"workspace-aaa"}
	ui := termiotest.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), client, identity, adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected the config-move failure to abort adoption")
	}
	if !strings.Contains(err.Error(), "move config dir") {
		t.Errorf("error = %v, want the propagated config-move failure", err)
	}
	if !errors.Is(err, errRenameBoom) {
		t.Errorf("error = %v, want it to wrap the rename cause", err)
	}
}

// TestAdoptLegacyIdentityDryRunReturnsFoundLegacySlug pins the dry-run slug: a
// bare root's pending state can live under its path-derived legacy slug, so a dry
// run must report that slug (not the repo slug) for the caller to resolve against.
func TestAdoptLegacyIdentityDryRunReturnsFoundLegacySlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const legacySlug = "workspace-aaa"

	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{legacySlug}
	if err := seedHomeState(
		state.Key{Slug: legacySlug, Agent: adoptAgentName},
		state.HomeState{HomeVolume: "home-legacy"},
	); err != nil {
		t.Fatalf("seed bare-root legacy state: %v", err)
	}

	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no VM for " + name}
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		identity,
		adoptAgentName,
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if got.Slug != legacySlug {
		t.Errorf("dry-run Slug = %q, want the legacy slug %q that holds the state", got.Slug, legacySlug)
	}
	if !got.Pending || got.Applied {
		t.Errorf("adoption = %+v, want pending and not applied", got)
	}
	notice := strings.Join(ui.WarnCalls, "\n")
	if !strings.Contains(notice, state.SlugDir(legacySlug)) {
		t.Errorf("notice missing the extra legacy state row for %s:\n%s", legacySlug, notice)
	}
	legacyConfigDir := configpaths.Get().UserSlugConfigDir(legacySlug)
	if !strings.Contains(notice, "config dir") || !strings.Contains(notice, legacyConfigDir) {
		t.Errorf("notice missing the legacy config-dir row %q:\n%s", legacyConfigDir, notice)
	}
}

// TestAdoptLegacyIdentityDestroyFailureLeavesStateForRetry pins resumability: a
// bare root can have state under both the origin slug and its path slug, and if
// one source's VM teardown fails, the migration must abort with every legacy state
// file still in place so the next run resumes and adopts the remaining source
// rather than stranding it.
func TestAdoptLegacyIdentityDestroyFailureLeavesStateForRetry(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const legacySlug = "workspace-aaa"
	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{legacySlug}

	// The requested agent's pending state is under the origin slug; a sibling
	// agent's state sits under the bare-root legacy slug, whose VM teardown fails.
	if err := seedHomeState(adoptRepoKey(), state.HomeState{HomeVolume: adoptLegacyVolume}); err != nil {
		t.Fatalf("seed origin state: %v", err)
	}
	piLegacyKey := state.Key{Slug: legacySlug, Agent: "pi"}
	if err := seedHomeState(piLegacyKey, state.HomeState{HomeVolume: "home-pi"}); err != nil {
		t.Fatalf("seed bare-root legacy state: %v", err)
	}

	failingClient := &msbtest.MockMsbClient{
		GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
			if strings.Contains(name, legacySlug) {
				return nil, errors.New("teardown boom")
			}
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
		},
	}
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), failingClient,
		identity,
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected the sibling VM teardown failure to abort the migration")
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("a destroy failure must not have moved state: the checkout state file exists")
	}
	if !statePresent(t, adoptRepoKey()) || !statePresent(t, piLegacyKey) {
		t.Error("a destroy failure must leave every legacy state file in place for retry")
	}

	// Retry with every VM gone: both sources are adopted now.
	okClient := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	retryUI := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		okClient,
		identity,
		adoptAgentName,
		true,
		&retryUI,
	)
	if err != nil {
		t.Fatalf("retry AdoptLegacyIdentity: %v", err)
	}
	if !got.Applied {
		t.Errorf("retry = %+v, want it applied", got)
	}
	if !statePresent(t, adoptCheckoutKey()) || !statePresent(t, state.Key{Slug: adoptCheckoutSlug, Agent: "pi"}) {
		t.Error("retry did not adopt every legacy source")
	}
}

// TestAdoptLegacyIdentityAdoptsLiveLegacyVMWithoutState pins the lost-state
// case (issue #219): no legacy state file, but a live legacy project VM. The VM
// is itself a legacy identity, so adoption destroys it and records its mounted
// home volume durably in the checkout state before the destroy, for home
// resolution to reuse — instead of treating the checkout as brand new and
// minting a fresh volume.
func TestAdoptLegacyIdentityAdoptsLiveLegacyVMWithoutState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	const legacyVolume = "agents-sandbox-home-widget-aaa-opencode-9"
	handle := &msbtest.MockSandboxHandle{
		Name_: adoptLegacyVMName(),
		Mounts: map[string]msb.MountConfig{
			mounts.VMHomeDir: msb.Mount.Named(legacyVolume, msb.MountOptions{}),
		},
	}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return handle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if !handle.Destroyed {
		t.Error("live legacy VM was not destroyed")
	}
	want := IdentityAdoption{Slug: adoptCheckoutSlug, Pending: true, Applied: true}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
	// The VM was the only record of its home volume and is now gone, so adoption
	// must have persisted the volume first.
	st, readErr := state.ReadState(adoptCheckoutKey())
	if readErr != nil {
		t.Fatalf("adoption did not record the recovered volume: %v", readErr)
	}
	if st.RecoveredHomeVolume != legacyVolume {
		t.Errorf("RecoveredHomeVolume = %q, want %q", st.RecoveredHomeVolume, legacyVolume)
	}
}

// TestAdoptLegacyIdentityPersistsVolumeBeforeDestroyFails pins the ordering the
// #244 review required: the recovered volume is durable before the legacy VM is
// destroyed, so a destroy failure (or a caller that discards the result) cannot
// lose it — and the marker keeps the trigger live so the teardown is retried.
func TestAdoptLegacyIdentityPersistsVolumeBeforeDestroyFails(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	const legacyVolume = "agents-sandbox-home-widget-aaa-opencode-9"
	handle := &msbtest.MockSandboxHandle{
		Name_:      adoptLegacyVMName(),
		DestroyErr: errors.New("destroy boom"),
		Mounts: map[string]msb.MountConfig{
			mounts.VMHomeDir: msb.Mount.Named(legacyVolume, msb.MountOptions{}),
		},
	}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return handle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected the destroy failure to abort adoption")
	}
	st, readErr := state.ReadState(adoptCheckoutKey())
	if readErr != nil || st.RecoveredHomeVolume != legacyVolume {
		t.Fatalf("volume not persisted before the destroy: %+v (err %v)", st, readErr)
	}

	// Retry with a working destroy: the marker keeps the trigger live, so the
	// leftover legacy VM is torn down and the marker survives for recovery.
	handle.DestroyErr = nil
	retryUI := termiotest.NewTestMock(t)
	retry, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&retryUI,
	)
	if err != nil {
		t.Fatalf("retry AdoptLegacyIdentity: %v", err)
	}
	if !handle.Destroyed {
		t.Error("retry did not destroy the leftover legacy VM")
	}
	if !retry.Pending || !retry.Applied {
		t.Errorf("retry adoption = %+v, want pending and applied", retry)
	}
	st, readErr = state.ReadState(adoptCheckoutKey())
	if readErr != nil || st.RecoveredHomeVolume != legacyVolume {
		t.Errorf("recovered volume lost on retry: %+v (err %v)", st, readErr)
	}
}

// TestAdoptLegacyIdentityDryRunLiveLegacyVMIsReadOnly pins the dry run over a
// live legacy VM: the notice names the VM's home volume, nothing is destroyed,
// nothing is recorded, and the caller keeps the legacy slug to resolve against.
func TestAdoptLegacyIdentityDryRunLiveLegacyVMIsReadOnly(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	const legacyVolume = "agents-sandbox-home-widget-aaa-opencode-9"
	handle := &msbtest.MockSandboxHandle{
		Name_: adoptLegacyVMName(),
		Mounts: map[string]msb.MountConfig{
			mounts.VMHomeDir: msb.Mount.Named(legacyVolume, msb.MountOptions{}),
		},
	}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		client,
		adoptIdentity(),
		adoptAgentName,
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if handle.Destroyed {
		t.Error("legacy VM destroyed during a dry run")
	}
	notice := strings.Join(ui.WarnCalls, "\n")
	if !strings.Contains(notice, "Would migrate") || !strings.Contains(notice, legacyVolume) {
		t.Errorf("dry-run notice missing the would-headline or the volume:\n%s", notice)
	}
	want := IdentityAdoption{Slug: adoptRepoSlug, Pending: true}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("a dry run must not record a checkout state")
	}
}

// TestAdoptLegacyIdentityLiveLegacyVMMountsErrorFailsClosed pins the fail-closed
// rule: a live legacy VM whose mounts cannot be read might hold the user's only
// home volume, so adoption aborts rather than destroying it blind.
func TestAdoptLegacyIdentityLiveLegacyVMMountsErrorFailsClosed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	handle := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName(), MountsErr: errors.New("mounts boom")}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termiotest.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(context.Background(), configpaths.Get(), client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected an unreadable legacy VM mount to fail adoption")
	}
	if handle.Destroyed {
		t.Error("legacy VM destroyed despite an unreadable mount")
	}
}

// TestAdoptLegacyIdentityClaimsSharedVolumeOnce pins the two-clone race the
// #244 review found: clone A records a marker for a shared legacy VM's home
// volume but its destroy fails, so clone B can still adopt the surviving VM.
// Only one checkout may own the volume: B must not record a marker, and the
// claim must name A.
func TestAdoptLegacyIdentityClaimsSharedVolumeOnce(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	const legacyVolume = "agents-sandbox-home-widget-aaa-opencode-5"
	identityA := git.Identity{RepoSlug: adoptRepoSlug, CheckoutSlug: "checkout-aaa"}
	identityB := git.Identity{RepoSlug: adoptRepoSlug, CheckoutSlug: "checkout-bbb"}
	checkoutA := state.Key{Slug: "checkout-aaa", Agent: adoptAgentName}
	checkoutB := state.Key{Slug: "checkout-bbb", Agent: adoptAgentName}

	legacyMount := map[string]msb.MountConfig{
		mounts.VMHomeDir: msb.Mount.Named(legacyVolume, msb.MountOptions{}),
	}
	// Clone A claims the volume and records the marker, then its destroy fails.
	failing := &msbtest.MockSandboxHandle{
		Name_:      adoptLegacyVMName(),
		DestroyErr: errors.New("destroy boom"),
		Mounts:     legacyMount,
	}
	clientA := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return failing, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	uiA := termiotest.NewTestMock(t)
	if _, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		clientA,
		identityA,
		adoptAgentName,
		true,
		&uiA,
	); err == nil {
		t.Fatal("expected clone A's destroy failure to abort adoption")
	}
	if stA, err := state.ReadState(checkoutA); err != nil || stA.RecoveredHomeVolume != legacyVolume {
		t.Fatalf("clone A marker = %+v (err %v), want %q", stA, err, legacyVolume)
	}

	// Clone B adopts the surviving legacy VM successfully; it must not record the
	// same volume, which clone A owns.
	succeeding := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName(), Mounts: legacyMount}
	clientB := &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return succeeding, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	uiB := termiotest.NewTestMock(t)
	if _, err := AdoptLegacyIdentity(
		context.Background(),
		configpaths.Get(),
		clientB,
		identityB,
		adoptAgentName,
		true,
		&uiB,
	); err != nil {
		t.Fatalf("clone B adoption: %v", err)
	}
	if !succeeding.Destroyed {
		t.Error("clone B did not tear down the leftover legacy VM")
	}
	if stB, readErr := state.ReadState(checkoutB); readErr == nil && stB.RecoveredHomeVolume != "" {
		t.Errorf("clone B recorded the shared volume: %+v", stB)
	}

	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	claim, ok := root.AdoptedHomeVolume(adoptRepoSlug, adoptAgentName)
	if !ok || claim.CheckoutSlug != identityA.CheckoutSlug || claim.Volume != legacyVolume {
		t.Errorf("claim = %+v (present %v), want %q owning %q", claim, ok, identityA.CheckoutSlug, legacyVolume)
	}
}

// TestLegacySlugsFiltersSources pins the ordered, de-duplicated source list
// adoption walks: the checkout slug is never a source, an empty repo slug is
// skipped without ending the scan, and a duplicate later in the legacy list is
// dropped.
func TestLegacySlugsFiltersSources(t *testing.T) {
	tests := []struct {
		name     string
		identity git.Identity
		want     []string
	}{
		{
			name:     "repo slug is the only source",
			identity: git.Identity{RepoSlug: "widget-aaa", CheckoutSlug: "widget-bbb"},
			want:     []string{"widget-aaa"},
		},
		{
			name:     "checkout slug is never a source",
			identity: git.Identity{RepoSlug: "widget-bbb", CheckoutSlug: "widget-bbb"},
			want:     nil,
		},
		{
			name:     "empty repo slug is skipped without ending the scan",
			identity: git.Identity{RepoSlug: "", CheckoutSlug: "widget-bbb", LegacyRepoSlugs: []string{"widget-aaa"}},
			want:     []string{"widget-aaa"},
		},
		{
			name: "duplicate and checkout entries in the legacy list are dropped",
			identity: git.Identity{
				RepoSlug:        "widget-aaa",
				CheckoutSlug:    "widget-bbb",
				LegacyRepoSlugs: []string{"widget-aaa", "widget-bbb", "widget-ccc"},
			},
			want: []string{"widget-aaa", "widget-ccc"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := legacySlugs(tc.identity); !slices.Equal(got, tc.want) {
				t.Errorf("legacySlugs(%+v) = %v, want %v", tc.identity, got, tc.want)
			}
		})
	}
}

// TestAdoptionLockKeysCoversSiblingAndCheckoutKeys pins the lock set to what the
// directory move actually touches: every agent with legacy state, under both the
// legacy and the checkout slug.
func TestAdoptionLockKeysCoversSiblingAndCheckoutKeys(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	if err := seedHomeState(
		state.Key{Slug: adoptRepoSlug, Agent: "pi"},
		state.HomeState{HomeVolume: "vol-pi"},
	); err != nil {
		t.Fatal(err)
	}

	keys, err := adoptionLockKeys([]string{adoptRepoSlug}, adoptCheckoutSlug, adoptAgentName)
	if err != nil {
		t.Fatalf("adoptionLockKeys: %v", err)
	}
	want := map[state.Key]bool{
		{Slug: adoptRepoSlug, Agent: adoptAgentName}:     true,
		{Slug: adoptRepoSlug, Agent: "pi"}:               true,
		{Slug: adoptCheckoutSlug, Agent: adoptAgentName}: true,
		{Slug: adoptCheckoutSlug, Agent: "pi"}:           true,
	}
	if len(keys) != len(want) {
		t.Fatalf("adoptionLockKeys = %v, want the %d keys %v", keys, len(want), want)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected lock key %+v", k)
		}
	}
}

// TestAdoptionLockKeysErrorsOnUnreadableSlug pins the enumeration failure
// propagation: an unreadable legacy slug must abort the lock-set derivation
// rather than lock an incomplete set.
func TestAdoptionLockKeysErrorsOnUnreadableSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testutil.WritePath(t, state.SlugDir(adoptRepoSlug), "not a directory")

	if _, err := adoptionLockKeys([]string{adoptRepoSlug}, adoptCheckoutSlug, adoptAgentName); err == nil {
		t.Fatal("expected an unreadable slug to fail the lock-set derivation")
	} else if !strings.Contains(err.Error(), "list legacy state") {
		t.Errorf("error = %v, want the enumeration failure", err)
	}
}

// TestPersistRecoveredHomeVolumeEmptyIsNoOp pins the guard for a VM with no
// home mount: there is nothing to preserve, so no state is written.
func TestPersistRecoveredHomeVolumeEmptyIsNoOp(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if err := persistRecoveredHomeVolume(adoptCheckoutKey(), ""); err != nil {
		t.Fatalf("persistRecoveredHomeVolume: %v", err)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("an empty recovered volume must not write a state file")
	}
}

// TestPersistRecoveredHomeVolumeReadError pins the write failure's wrapping: a
// directory at the state file path makes the recovery read fail with a
// non-not-found error, so the recorded volume is reported rather than lost.
func TestPersistRecoveredHomeVolumeReadError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	k := adoptCheckoutKey()
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	err := persistRecoveredHomeVolume(k, adoptLegacyVolume)
	if err == nil {
		t.Fatal("expected a read error to fail the recovered-volume write")
	}
	if !strings.Contains(err.Error(), "record the recovered home volume") {
		t.Errorf("error = %v, want it to name the recovered-volume write", err)
	}
	if !errors.As(err, new(*os.PathError)) {
		t.Errorf("error = %v, want it to preserve the filesystem cause", err)
	}
}

// TestClaimLegacyHomeVolumeReportsNotOwnedOnMismatch pins the claim's fail-closed
// outcome: a claim naming a different volume than the live VM mounts is returned
// with owned=false alongside its error.
func TestClaimLegacyHomeVolumeReportsNotOwnedOnMismatch(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if err := seedRootState(state.RootState{AdoptedHomeVolumes: map[string]state.AdoptedHomeVolume{
		"widget-aaa/opencode": {CheckoutSlug: adoptCheckoutSlug, Volume: "some-other-volume"},
	}}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	source := legacyPendingSource{Slug: adoptRepoSlug, HomeVolume: adoptLegacyVolume, FromVM: true}
	_, own, err := withRootTxn(t, func(txn *state.RootTxn) (state.AdoptedHomeVolume, bool, error) {
		return claimLegacyHomeVolume(txn, source, adoptCheckoutKey())
	})
	if err == nil {
		t.Fatal("expected a mismatched claim to fail closed")
	}
	if own {
		t.Error("a mismatched claim must report not-owned alongside its error")
	}
}

// TestClaimLegacyHomeVolumeNamesTheCheckoutAndVolume pins that a fresh claim
// returns the ownership record the caller records: this checkout and the
// observed live volume.
func TestClaimLegacyHomeVolumeNamesTheCheckoutAndVolume(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	source := legacyPendingSource{Slug: adoptRepoSlug, HomeVolume: adoptLegacyVolume, FromVM: true}
	got, own, err := withRootTxn(t, func(txn *state.RootTxn) (state.AdoptedHomeVolume, bool, error) {
		return claimLegacyHomeVolume(txn, source, adoptCheckoutKey())
	})
	if err != nil || !own {
		t.Fatalf("claimLegacyHomeVolume = (own %v, err %v), want an owned claim", own, err)
	}
	want := state.AdoptedHomeVolume{CheckoutSlug: adoptCheckoutSlug, Volume: adoptLegacyVolume}
	if got != want {
		t.Errorf("claim = %+v, want %+v", got, want)
	}
}

// TestLegacyAdoptionPendingUnreadableStateReportsNotPending pins the error
// contract for an unreadable legacy state: it returns a switching error with
// pending=false, so a caller that checks the error first never mistakes a
// failure for a pending migration.
func TestLegacyAdoptionPendingUnreadableStateReportsNotPending(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	legacyKey := state.Key{Slug: adoptRepoSlug, Agent: adoptAgentName}
	if err := os.MkdirAll(filepath.Join(state.KeyDir(legacyKey), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	pending, err := legacyPendingWithError(t, &msbtest.MockMsbClient{})
	if err == nil {
		t.Fatal("expected an unreadable legacy state to fail")
	}
	if pending {
		t.Error("an unreadable legacy state must report not pending")
	}
}

// TestLegacyAdoptionPendingUnreadableMountWrapsCause pins both the pending=false
// contract and the cause-preserving wrap for an unreadable legacy VM mount.
func TestLegacyAdoptionPendingUnreadableMountWrapsCause(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	handle := &msbtest.MockSandboxHandle{Name_: adoptLegacyVMName(), MountsErr: errLegacyVMBoom}
	client := &msbtest.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	pending, err := legacyPendingWithError(t, client)
	if err == nil {
		t.Fatal("expected an unreadable legacy VM mount to fail")
	}
	if pending {
		t.Error("an unreadable legacy VM mount must report not pending")
	}
	if !errors.Is(err, errLegacyVMBoom) {
		t.Errorf("error = %v, want it to wrap the legacy-VM cause", err)
	}
}

// TestDestroyLegacyVMsErrorsOnUnreadableSlug pins the enumeration failure: an
// unreadable legacy slug directory aborts the teardown rather than moving state
// with an unknown legacy VM possibly left running.
func TestDestroyLegacyVMsErrorsOnUnreadableSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testutil.WritePath(t, state.SlugDir(adoptRepoSlug), "not a directory")

	ui := termiotest.NewTestMock(t)
	err := destroyLegacyVMs(context.Background(), &msbtest.MockMsbClient{}, adoptRepoSlug, adoptAgentName, &ui)
	if err == nil {
		t.Fatal("expected an unreadable slug to fail the teardown")
	}
	if !strings.Contains(err.Error(), "list legacy state") {
		t.Errorf("error = %v, want the enumeration failure", err)
	}
	if !errors.As(err, new(*os.PathError)) {
		t.Errorf("error = %v, want it to preserve the filesystem cause", err)
	}
}

// TestLegacyAgentsToMigrateIncludesCommandAgentOnce pins that the command's own
// agent is not duplicated when it already has legacy state: the early return
// keeps the enumerated names rather than appending the agent a second time.
func TestLegacyAgentsToMigrateIncludesCommandAgentOnce(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	if err := seedHomeState(adoptRepoKey(), state.HomeState{HomeVolume: adoptLegacyVolume}); err != nil {
		t.Fatal(err)
	}

	agents, err := legacyAgentsToMigrate(adoptRepoSlug, adoptAgentName)
	if err != nil {
		t.Fatalf("legacyAgentsToMigrate: %v", err)
	}
	if !slices.Equal(agents, []string{adoptAgentName}) {
		t.Fatalf("legacyAgentsToMigrate() = %v, want exactly [%s]", agents, adoptAgentName)
	}
}

// TestLegacyAgentsToMigrateAppendsMissingCommandAgent pins the other arm: an
// agent with no legacy state (e.g. a VM whose state file was lost, #219) is
// appended to the enumerated names.
func TestLegacyAgentsToMigrateAppendsMissingCommandAgent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	sibling := state.Key{Slug: adoptRepoSlug, Agent: "pi"}
	if err := seedHomeState(sibling, state.HomeState{HomeVolume: "vol-pi"}); err != nil {
		t.Fatal(err)
	}

	agents, err := legacyAgentsToMigrate(adoptRepoSlug, adoptAgentName)
	if err != nil {
		t.Fatalf("legacyAgentsToMigrate: %v", err)
	}
	if want := []string{"pi", adoptAgentName}; !slices.Equal(agents, want) {
		t.Fatalf("legacyAgentsToMigrate() = %v, want %v", agents, want)
	}
}

// TestMigrateLegacyConfigDirsMovesBareRootConfig pins the config half of the
// bare-root adoption: the pre-split per-slug config directory is renamed onto the
// repo slug the launcher reads now, so the user keeps their settings.
func TestMigrateLegacyConfigDirsMovesBareRootConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpathstest.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	to := paths.UserSlugConfigDir("agents-sandbox-bbb")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "x: 1\n")

	ui := termiotest.NewTestMock(t)
	identity := git.Identity{RepoSlug: "agents-sandbox-bbb", LegacyRepoSlugs: []string{"workspace-aaa"}}

	if err := migrateLegacyConfigDirs(configpaths.Get(), identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(to, "config.yaml")); err != nil {
		t.Errorf("config not moved to the repo slug dir: %v", err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Errorf("legacy config dir still present (stat err = %v)", err)
	}
	var sawMoved bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "moved legacy config dir") {
			sawMoved = true
		}
	}
	if !sawMoved {
		t.Errorf("expected the moved-config-dir verbose note; got %v", ui.VerboseCalls)
	}
}

// TestMigrateLegacyConfigDirsKeepsExistingTarget pins the never-overwrite rule:
// when the repo-slug config dir already exists, the legacy copy is left in place
// rather than clobbering the config the user is using. The legacy dir holds a
// launcher config file that the repo slug now shadows, so the user gets a
// warning rather than only a verbose note.
func TestMigrateLegacyConfigDirsKeepsExistingTarget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpathstest.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	to := paths.UserSlugConfigDir("agents-sandbox-bbb")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatalf("mkdir target config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "legacy\n")
	testutil.WriteFile(t, to, "config.yaml", "existing\n")

	ui := termiotest.NewTestMock(t)
	identity := git.Identity{RepoSlug: "agents-sandbox-bbb", LegacyRepoSlugs: []string{"workspace-aaa"}}

	if err := migrateLegacyConfigDirs(configpaths.Get(), identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(to, "config.yaml"))
	if err != nil {
		t.Fatalf("read target config: %v", err)
	}
	if string(body) != "existing\n" {
		t.Errorf("target config = %q, want it untouched", string(body))
	}
	if _, err := os.Stat(filepath.Join(from, "config.yaml")); err != nil {
		t.Errorf("legacy config removed although the target existed: %v", err)
	}
	var sawWarning bool
	for _, call := range ui.WarnCalls {
		if strings.Contains(call, "no longer read") {
			sawWarning = true
		}
	}
	if !sawWarning {
		t.Errorf("expected a warning that the legacy launcher config is no longer read; got %v", ui.WarnCalls)
	}
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "kept existing config dir") {
			t.Errorf("a kept legacy config file must warn, not only note verbosely: %v", ui.VerboseCalls)
		}
	}
}

// TestMigrateLegacyConfigDirsKeepsTargetWithoutConfig pins the quiet branch:
// when the target exists and the legacy dir holds no launcher config file (here
// only a secret file, which the repo slug now reads), the legacy dir is left in
// place with a verbose note and no warning.
func TestMigrateLegacyConfigDirsKeepsTargetWithoutConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpathstest.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	to := paths.UserSlugConfigDir("agents-sandbox-bbb")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatalf("mkdir target config dir: %v", err)
	}
	testutil.WriteFile(t, from, "env.secret.yaml", "TOKEN: legacy\n")

	ui := termiotest.NewTestMock(t)
	identity := git.Identity{RepoSlug: "agents-sandbox-bbb", LegacyRepoSlugs: []string{"workspace-aaa"}}

	if err := migrateLegacyConfigDirs(configpaths.Get(), identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(from, "env.secret.yaml")); err != nil {
		t.Errorf("legacy secret file removed although the target existed: %v", err)
	}
	var sawKept bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "kept existing config dir") {
			sawKept = true
		}
	}
	if !sawKept {
		t.Errorf("expected the kept-config-dir verbose note; got %v", ui.VerboseCalls)
	}
	for _, call := range ui.WarnCalls {
		if strings.Contains(call, "no longer read") {
			t.Errorf("a legacy dir without a launcher config must not warn; got %v", ui.WarnCalls)
		}
	}
}

// TestMigrateLegacyConfigDirsSkipsRepoSlugAndMissingSource pins that a legacy
// entry equal to the repo slug needs no move and a legacy slug with no config
// directory has nothing to move: both are skipped silently.
func TestMigrateLegacyConfigDirsSkipsRepoSlugAndMissingSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpathstest.WithRealConfigPaths(t)
	paths := configpaths.Get()

	// The repo-slug config dir exists, so an entry equal to the repo slug is
	// only a no-op if it is skipped outright: without the skip, from==to and the
	// code reports a spurious "kept existing" note.
	if err := os.MkdirAll(paths.UserSlugConfigDir("agents-sandbox-bbb"), 0o700); err != nil {
		t.Fatalf("mkdir repo-slug config dir: %v", err)
	}

	ui := termiotest.NewTestMock(t)
	identity := git.Identity{
		RepoSlug:        "agents-sandbox-bbb",
		LegacyRepoSlugs: []string{"agents-sandbox-bbb", "workspace-absent"},
	}
	if err := migrateLegacyConfigDirs(configpaths.Get(), identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	if len(ui.VerboseCalls) != 0 {
		t.Errorf("a repo-slug or missing legacy entry must be silent; got %v", ui.VerboseCalls)
	}
}

// TestMigrateLegacyConfigDirsContinuesPastSkippedEntries pins loop order: a
// legacy entry equal to the repo slug and one with no config directory are
// skipped, but the scan continues, so a later legacy identity's config
// directory is still moved onto the repo slug.
func TestMigrateLegacyConfigDirsContinuesPastSkippedEntries(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpathstest.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "x: 1\n")

	ui := termiotest.NewTestMock(t)
	identity := git.Identity{
		RepoSlug:        "agents-sandbox-bbb",
		LegacyRepoSlugs: []string{"agents-sandbox-bbb", "workspace-absent", "workspace-aaa"},
	}
	if err := migrateLegacyConfigDirs(configpaths.Get(), identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.UserSlugConfigDir("agents-sandbox-bbb"), "config.yaml")); err != nil {
		t.Errorf("a later legacy config dir was not moved past the skipped entries: %v", err)
	}
}

// TestMigrateLegacyConfigDirsReportsEveryKeptTarget pins that an existing target
// leaves every legacy directory in place, one report per legacy identity: the
// scan reports each rather than stopping at the first. The target holds no
// per-slug input, so the launcher still reads each legacy launcher config and
// the report must say so, not claim the config is stranded.
func TestMigrateLegacyConfigDirsReportsEveryKeptTarget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpathstest.WithRealConfigPaths(t)
	paths := configpaths.Get()

	if err := os.MkdirAll(paths.UserSlugConfigDir("agents-sandbox-bbb"), 0o700); err != nil {
		t.Fatalf("mkdir target config dir: %v", err)
	}
	for _, slug := range []string{"workspace-aaa", "workspace-ccc"} {
		from := paths.UserSlugConfigDir(slug)
		if err := os.MkdirAll(from, 0o700); err != nil {
			t.Fatalf("mkdir legacy config dir %s: %v", slug, err)
		}
		testutil.WriteFile(t, from, "config.yaml", "x: 1\n")
	}

	ui := termiotest.NewTestMock(t)
	identity := git.Identity{
		RepoSlug:        "agents-sandbox-bbb",
		LegacyRepoSlugs: []string{"workspace-aaa", "workspace-ccc"},
	}
	if err := migrateLegacyConfigDirs(configpaths.Get(), identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	var stillRead, stranded int
	for _, call := range ui.WarnCalls {
		if strings.Contains(call, "still read") {
			stillRead++
		}
		if strings.Contains(call, "no longer read") {
			stranded++
		}
	}
	if stillRead != 2 {
		t.Errorf("still-read reports = %d, want one per legacy identity; got %v", stillRead, ui.WarnCalls)
	}
	if stranded != 0 {
		t.Errorf(
			"an empty target leaves the legacy config active, so no stranded report is right; got %v",
			ui.WarnCalls,
		)
	}
}

// TestMigrateLegacyConfigDirsReportsRenameFailure pins the rename failure: a
// failed config move is reported, not silently swallowed.
func TestMigrateLegacyConfigDirsReportsRenameFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpathstest.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "x: 1\n")

	renameConfigDir = func(_, _ string) error { return errRenameBoom }
	t.Cleanup(func() { renameConfigDir = os.Rename })

	ui := termiotest.NewTestMock(t)
	identity := git.Identity{RepoSlug: "agents-sandbox-bbb", LegacyRepoSlugs: []string{"workspace-aaa"}}
	err := migrateLegacyConfigDirs(configpaths.Get(), identity, &ui)
	if err == nil {
		t.Fatal("expected a rename failure to be reported")
	}
	if !strings.Contains(err.Error(), "move config dir") {
		t.Errorf("error = %v, want the config-move failure", err)
	}
	if !errors.Is(err, errRenameBoom) {
		t.Errorf("error = %v, want it to wrap the rename cause", err)
	}
}

// TestAdoptionNoticeSkipsLegacySlugThatIsTheCheckoutSlug: a bare root whose old
// path slug already is its checkout slug (#433) moves no state from it, so the
// notice lists no "X -> X" state row, but still names the config dir move. A
// second legacy slug that is not the checkout slug still gets its own row, so the
// skip applies to the matching entry only rather than aborting the listing.
func TestAdoptionNoticeSkipsLegacySlugThatIsTheCheckoutSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const otherLegacySlug = "widget-legacy"
	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{adoptCheckoutSlug, otherLegacySlug}

	notice := adoptionNotice(configpaths.Get(), identity, adoptAgentName, adoptLegacyVolume, true)

	selfMove := state.SlugDir(adoptCheckoutSlug) + "  ->  " + state.SlugDir(adoptCheckoutSlug)
	if strings.Contains(notice, selfMove) {
		t.Errorf("notice lists a state move onto itself:\n%s", notice)
	}
	otherMove := state.SlugDir(otherLegacySlug) + "  ->  " + state.SlugDir(adoptCheckoutSlug)
	if !strings.Contains(notice, otherMove) {
		t.Errorf("notice missing the state move %q for a later legacy slug:\n%s", otherMove, notice)
	}
	configMove := configpaths.Get().UserSlugConfigDir(adoptCheckoutSlug) + "  ->  " +
		configpaths.Get().UserSlugConfigDir(adoptRepoSlug)
	if !strings.Contains(notice, configMove) {
		t.Errorf("notice missing the config dir move %q:\n%s", configMove, notice)
	}
}

// assertProjectVMLockHeld probes the on-disk project-VM lock: a held lock makes a
// fresh non-blocking flock contend.
func assertProjectVMLockHeld(t *testing.T, k state.Key) {
	t.Helper()
	path := filepath.Join(state.ProjectVMLockDir(k), state.ProjectVMLockName)
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open project-VM lock %s: %v", path, err)
	}
	defer f.Close()
	if err := hostlock.ExclusiveNonBlocking(f); err == nil {
		t.Errorf("project-VM lock for %+v must be held during the adoption move", k)
	} else if !hostlock.IsContention(err) {
		t.Errorf("probe for %+v failed for a non-contention reason: %v", k, err)
	}
}

// legacyVMNotFound returns a client that serves one live legacy VM (the handle)
// for the legacy project VM name and "sandbox not found" for everything else, the
// shape a lost-state adoption starts from.
func legacyVMNotFound(handle msb.SandboxHandle) *msbtest.MockMsbClient {
	return &msbtest.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return handle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
}

func legacyVMWithHome() *msbtest.MockSandboxHandle {
	return &msbtest.MockSandboxHandle{
		Name_: adoptLegacyVMName(),
		Mounts: map[string]msb.MountConfig{
			mounts.VMHomeDir: msb.Mount.Named(adoptLegacyVolume, msb.MountOptions{}),
		},
	}
}

// adoptOtherOwnerSlug is a third checkout that already owns a shared legacy
// identity's home volume but never wrote its recovery marker (its marker write
// failed after the claim committed), the state a later adopter destroying the
// legacy VM must not strand.
const adoptOtherOwnerSlug = "widget-ccc"

func adoptOtherOwnerKey() state.Key {
	return state.Key{Slug: adoptOtherOwnerSlug, Agent: adoptAgentName}
}

// errRenameBoom is the sentinel the rename seam returns, so a test can prove the
// config-move error wraps its cause.
var errRenameBoom = errors.New("rename boom")

// errLegacyVMBoom is a sentinel wrapped by the legacy-VM resolution helpers, so a
// test can prove the wrapping relationship survives (%w, not %v).
var errLegacyVMBoom = errors.New("legacy VM boom")

// withRootTxn runs fn inside one blocking root-state transaction and returns the
// three values of a claimLegacyHomeVolume-shaped call, so a test can exercise a
// RootTxn-taking helper directly.
func withRootTxn(
	t *testing.T,
	fn func(txn *state.RootTxn) (state.AdoptedHomeVolume, bool, error),
) (state.AdoptedHomeVolume, bool, error) {
	t.Helper()
	var (
		owner state.AdoptedHomeVolume
		own   bool
		fnErr error
	)
	if _, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		owner, own, fnErr = fn(txn)
		return nil
	}); err != nil {
		t.Fatalf("WithRootState: %v", err)
	}
	return owner, own, fnErr
}

func legacyPendingWithError(t *testing.T, client msb.Client) (bool, error) {
	t.Helper()
	_, pending, err := legacyAdoptionPending(
		context.Background(),
		client,
		[]string{adoptRepoSlug},
		adoptCheckoutKey(),
	)
	return pending, err
}

// The adoption tests use two fixed slugs so every path in the notice and every
// assertion is spelled out literally: widget-aaa is the legacy origin-derived
// identity, widget-bbb the checkout-scoped one.
const (
	adoptRepoSlug     = "widget-aaa"
	adoptCheckoutSlug = "widget-bbb"
	adoptAgentName    = "opencode"
	// adoptLegacyVolume is the home-volume name recorded in the legacy state,
	// embedding the legacy repo slug because the name rides along on migration.
	adoptLegacyVolume = "agents-sandbox-home-widget-aaa-opencode-1"
)

func adoptIdentity() git.Identity {
	return git.Identity{
		RepoSlug:     adoptRepoSlug,
		CheckoutSlug: adoptCheckoutSlug,
		BindRoot:     "/repo",
		CheckoutRoot: "/repo",
	}
}

func adoptRepoKey() state.Key { return state.Key{Slug: adoptRepoSlug, Agent: adoptAgentName} }

func adoptCheckoutKey() state.Key { return state.Key{Slug: adoptCheckoutSlug, Agent: adoptAgentName} }

func adoptLegacyVMName() string { return ProjectVMName(adoptRepoKey()) }

// seedLegacyState writes the legacy origin-keyed state file that triggers
// adoption, and returns the path it wrote so a test can assert it survives.
func seedLegacyState(t *testing.T) string {
	t.Helper()
	if err := seedHomeState(adoptRepoKey(), state.HomeState{HomeVolume: adoptLegacyVolume}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}
	return filepath.Join(state.KeyDir(adoptRepoKey()), "state.yaml")
}

// seedRootState writes rs as the launcher-global RootState through the root
// transaction. Tests only.
func seedRootState(rs state.RootState) error {
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		*tx.State() = rs
		return tx.Commit()
	})
	return err
}
