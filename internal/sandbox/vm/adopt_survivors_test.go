package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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

// TestClaimLegacyHomeVolumeReportsNotOwnedOnMismatch pins the claim's fail-closed
// outcome: a claim naming a different volume than the live VM mounts is returned
// with owned=false alongside its error.
func TestClaimLegacyHomeVolumeReportsNotOwnedOnMismatch(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

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
	configpaths.WithMockConfigPaths(t)

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
	configpaths.WithMockConfigPaths(t)

	legacyKey := state.Key{Slug: adoptRepoSlug, Agent: adoptAgentName}
	if err := os.MkdirAll(filepath.Join(state.KeyDir(legacyKey), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	pending, err := legacyPendingWithError(t, &msb.MockMsbClient{})
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
	configpaths.WithMockConfigPaths(t)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName(), MountsErr: errLegacyVMBoom}
	client := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
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

// TestAdoptLegacyIdentityLiveLegacyVMWithoutHomeMountSkipsTheClaim pins the guard
// on the VM-only arm: a legacy VM with no home mount has nothing to preserve, so
// adoption destroys it without claiming a volume or recording a recovery marker.
func TestAdoptLegacyIdentityLiveLegacyVMWithoutHomeMountSkipsTheClaim(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(
		context.Background(),
		legacyVMNotFound(handle),
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
