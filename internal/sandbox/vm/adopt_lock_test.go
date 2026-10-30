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
	"github.com/inoio/agents-sandbox/internal/hostlock"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestLegacyAgentsToMigrateIncludesCommandAgentOnce pins that the command's own
// agent is not duplicated when it already has legacy state: the early return
// keeps the enumerated names rather than appending the agent a second time.
func TestLegacyAgentsToMigrateIncludesCommandAgentOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
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
	configpaths.WithMockConfigPaths(t)
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

// TestAdoptionLockKeysCoversSiblingAndCheckoutKeys pins the lock set to what the
// directory move actually touches: every agent with legacy state, under both the
// legacy and the checkout slug.
func TestAdoptionLockKeysCoversSiblingAndCheckoutKeys(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
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

// TestAdoptionLockKeysErrorsOnUnreadableSlug covers the enumeration failure
// propagation: an unreadable legacy slug must abort the lock-set derivation
// rather than lock an incomplete set.
func TestAdoptionLockKeysErrorsOnUnreadableSlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WritePath(t, state.SlugDir(adoptRepoSlug), "not a directory")

	if _, err := adoptionLockKeys([]string{adoptRepoSlug}, adoptCheckoutSlug, adoptAgentName); err == nil {
		t.Fatal("expected an unreadable slug to fail the lock-set derivation")
	} else if !strings.Contains(err.Error(), "list legacy state") {
		t.Errorf("error = %v, want the enumeration failure", err)
	}
}

// TestAdoptLegacyIdentityFailsWhenALaterLegacySlugIsUnreadable covers the
// lock-set derivation failure: the pending state is under the first legacy slug,
// but a later legacy slug cannot be enumerated, so adoption aborts before
// destroying or moving anything.
func TestAdoptLegacyIdentityFailsWhenALaterLegacySlugIsUnreadable(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)
	const badSlug = "workspace-bad"
	testutil.WritePath(t, state.SlugDir(badSlug), "not a directory")

	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{adoptRepoSlug, badSlug}

	client := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), client, identity, adoptAgentName, true, &ui)
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

func TestAdoptLegacyIdentityHoldsKeyLocksAcrossTheMove(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
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

	client := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		client,
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
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName(), DestroyErr: errors.New("destroy boom")}
	client := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		client,
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
