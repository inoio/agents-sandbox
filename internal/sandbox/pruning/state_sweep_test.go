package pruning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestPruneStateArtifactsRemovesUnheldClaimsAndStaleLeases(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)

	const slug = "sweep-claims-and-leases"
	claimsDir := filepath.Join(state.SlugDir(slug), "claims")
	if err := os.MkdirAll(claimsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A free claim file: reclaimed.
	testutil.WritePath(t, filepath.Join(claimsDir, "free.claim"), "")
	// A held claim file: kept.
	releaseClaim, claimed, err := state.AcquireClaim(slug, "held")
	if err != nil || !claimed {
		t.Fatalf("AcquireClaim: claimed=%v err=%v", claimed, err)
	}
	defer releaseClaim()

	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	clientsDir := filepath.Join(state.SlugDir(slug), "opencode", "clients")
	if err := os.MkdirAll(clientsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A live lease: kept.
	releaseLease, err := state.AcquireClientLease(agentKey)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer releaseLease()
	// A stale unlocked lease: a crashed client's leftover, reclaimed.
	staleLease := filepath.Join(clientsDir, "22222-4000000000.lock")
	testutil.WritePath(t, staleLease, "stale")
	aged := time.Now().Add(-time.Hour)
	if err := os.Chtimes(staleLease, aged, aged); err != nil {
		t.Fatal(err)
	}

	report, err := PruneStateArtifacts(context.Background(), &testUI)
	if err != nil {
		t.Fatalf("PruneStateArtifacts: %v", err)
	}
	if report.ClaimsRemoved != 1 {
		t.Errorf("ClaimsRemoved = %d, want 1", report.ClaimsRemoved)
	}
	if report.LeasesRemoved != 1 {
		t.Errorf("LeasesRemoved = %d, want 1", report.LeasesRemoved)
	}
	if _, err := os.Stat(filepath.Join(claimsDir, "free.claim")); !os.IsNotExist(err) {
		t.Errorf("free claim should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(claimsDir, "held.claim")); err != nil {
		t.Errorf("held claim should remain, stat err = %v", err)
	}
	if _, err := os.Stat(staleLease); !os.IsNotExist(err) {
		t.Errorf("stale lease should be gone, stat err = %v", err)
	}
	// The slug dir survives: the held claim and the live lease keep it.
	if _, err := os.Stat(state.SlugDir(slug)); err != nil {
		t.Errorf("slug dir should remain, stat err = %v", err)
	}
}

func TestPruneStateArtifactsRemovesStateWithNoVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)

	const slug = "sweep-no-volume"
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{ImageDigest: "agent-digest"}); err != nil {
		t.Fatalf("seed agent state: %v", err)
	}
	// Legacy agent-less state file directly under the slug dir.
	if err := seedHomeState(state.Key{Slug: slug}, state.HomeState{ImageDigest: "legacy-digest"}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	report, err := PruneStateArtifacts(context.Background(), &testUI)
	if err != nil {
		t.Fatalf("PruneStateArtifacts: %v", err)
	}
	if report.StatesRemoved != 2 {
		t.Errorf("StatesRemoved = %d, want 2 (agent + legacy state)", report.StatesRemoved)
	}
	if _, err := state.ReadState(agentKey); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("agent state should be gone, err = %v", err)
	}
	if _, err := state.ReadState(state.Key{Slug: slug}); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("legacy state should be gone, err = %v", err)
	}
	// With nothing left under it, the slug dir itself is reclaimed.
	if _, err := os.Stat(state.SlugDir(slug)); !os.IsNotExist(err) {
		t.Errorf("empty slug dir should be removed, stat err = %v", err)
	}
	if report.DirsRemoved != 1 {
		t.Errorf("DirsRemoved = %d, want 1", report.DirsRemoved)
	}
}

func TestPruneStateArtifactsKeepsStateRecordingALiveVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)

	const slug = "sweep-live-volume"
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(
		agentKey,
		state.NewHomeState("agents-sandbox-home-"+slug+"-20260806T143022", "digest"),
	); err != nil {
		t.Fatalf("seed agent state: %v", err)
	}
	// A pending adoption also records a volume: kept.
	if err := seedHomeState(
		state.Key{Slug: slug},
		state.HomeState{RecoveredHomeVolume: "agents-sandbox-home-" + slug + "-legacy"},
	); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	report, err := PruneStateArtifacts(context.Background(), &testUI)
	if err != nil {
		t.Fatalf("PruneStateArtifacts: %v", err)
	}
	if report.StatesRemoved != 0 {
		t.Errorf("StatesRemoved = %d, want 0 (safe tier never removes a volume-recording state)", report.StatesRemoved)
	}
	if report.StaleStatesKept != 2 {
		t.Errorf("StaleStatesKept = %d, want 2", report.StaleStatesKept)
	}
	if _, err := state.ReadState(agentKey); err != nil {
		t.Errorf("agent state should remain, err = %v", err)
	}
	if _, err := state.ReadState(state.Key{Slug: slug}); err != nil {
		t.Errorf("legacy state should remain, err = %v", err)
	}
	if report.DirsRemoved != 0 {
		t.Errorf("DirsRemoved = %d, want 0", report.DirsRemoved)
	}
}

func TestPruneStateArtifactsLeavesLocksTree(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)

	// The locks tree is never touched by the sweep.
	locksDir := filepath.Join(configpaths.Get().UserStateDir(), "locks")
	if err := os.MkdirAll(locksDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lockFile := filepath.Join(locksDir, "sweep-locks-opencode.lock")
	testutil.WritePath(t, lockFile, "")

	const slug = "sweep-locks"
	if err := seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, state.HomeState{}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	report, err := PruneStateArtifacts(context.Background(), &testUI)
	if err != nil {
		t.Fatalf("PruneStateArtifacts: %v", err)
	}
	if report.StatesRemoved != 1 {
		t.Errorf("StatesRemoved = %d, want 1 (the sweep ran)", report.StatesRemoved)
	}
	if _, err := os.Stat(lockFile); err != nil {
		t.Errorf("lock file should survive the sweep, stat err = %v", err)
	}
	if _, err := os.Stat(locksDir); err != nil {
		t.Errorf("locks dir should survive the sweep, stat err = %v", err)
	}
}

// TestPruneDryRunSkipsStateReclamation pins the dry-run gate on the state
// phase: --dry-run makes no changes (specs/dry-run-preview.md), so a state
// file the sweep would remove must survive a dry-run Prune and the report must
// carry no state removals. The state records no volume, so dropping the gate
// would remove it and fail both assertions.
func TestPruneDryRunSkipsStateReclamation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	docker.WithNoopDockerMock(t)
	testUI := termio.NewTestMock(t)

	const slug = "sweep-dry-run"
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(agentKey, state.HomeState{ImageDigest: "digest"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	report, err := Prune(context.Background(), docker.Get(), PruneOptions{
		Age:    time.Hour,
		DryRun: true,
	}, &testUI)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := state.ReadState(agentKey); err != nil {
		t.Errorf("a dry-run must not remove a state file, read err = %v", err)
	}
	if report.State != (StateSweepReport{}) {
		t.Errorf("dry-run State report = %+v, want zero (nothing removed)", report.State)
	}
}
