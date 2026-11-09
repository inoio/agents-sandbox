package pruning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestPruneStateArtifactsRemovesUnheldClaimsAndStaleLeases(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)

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
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)

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
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)

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
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)

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
	configpathstest.WithMockConfigPaths(t)
	msbtest.WithMsbMock(t, &msbtest.MockMsbClient{})
	dockertest.WithNoopDockerMock(t)
	testUI := termiotest.NewTestMock(t)

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

// TestPruneStateArtifactsReclaimsEmptyAgentAndClientDirs pins the bottom-up
// reclaim: an agent whose state file is already absent but whose clients/ dir
// held a stale lease must not leave an empty clients/ and agent shell behind, or
// the slug is rediscovered by project discovery on every sweep.
func TestPruneStateArtifactsReclaimsEmptyAgentAndClientDirs(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)

	const slug = "sweep-empty-shells"
	agentKey := state.Key{Slug: slug, Agent: "opencode"}
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

	report, err := PruneStateArtifacts(context.Background(), &testUI)
	if err != nil {
		t.Fatalf("PruneStateArtifacts: %v", err)
	}
	if report.LeasesRemoved != 1 {
		t.Errorf("LeasesRemoved = %d, want 1", report.LeasesRemoved)
	}
	if _, err := os.Stat(state.SlugDir(slug)); !os.IsNotExist(err) {
		t.Errorf("the emptied slug dir should be reclaimed, stat err = %v", err)
	}
	if report.DirsRemoved != 1 {
		t.Errorf("DirsRemoved = %d, want 1", report.DirsRemoved)
	}
}

// TestPruneStateArtifactsPropagatesRootReadError pins that an unreadable state
// root fails the sweep closed rather than reporting nothing removed.
func TestPruneStateArtifactsPropagatesRootReadError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	breakStateRoot(t)
	testUI := termiotest.NewTestMock(t)

	if _, err := PruneStateArtifacts(context.Background(), &testUI); err == nil {
		t.Fatal("PruneStateArtifacts should fail closed on an unreadable state root")
	}
}

// TestPruneStateArtifactsStopsOnCancelledContext pins that a cancelled context
// aborts the sweep before it touches the next slug.
func TestPruneStateArtifactsStopsOnCancelledContext(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)
	if err := os.MkdirAll(state.SlugDir("sweep-cancelled"), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PruneStateArtifacts(ctx, &testUI); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestPruneStateArtifactsPropagatesSweepError pins that a per-slug sweep failure
// propagates instead of being swallowed as a partial report.
func TestPruneStateArtifactsPropagatesSweepError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)
	dir := state.SlugDir("sweep-slugerr")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A claims path that is not a directory cannot be read as a claims dir.
	testutil.WritePath(t, filepath.Join(dir, "claims"), "")

	if _, err := PruneStateArtifacts(context.Background(), &testUI); err == nil {
		t.Fatal("PruneStateArtifacts should propagate the sweep error")
	}
}

// TestSweepSlugPropagatesChildErrors pins each fail-closed path of sweepSlug:
// a claims read error, a state-dir read error, a claims-dir removal error, and a
// slug-dir removal error.
func TestSweepSlugPropagatesChildErrors(t *testing.T) {
	t.Run("claims read error", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		const slug = "sweep-child-claims-read"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, filepath.Join(dir, "claims"), "")
		var report StateSweepReport
		if err := report.sweepSlug(slug); err == nil {
			t.Fatal("expected the claims read error")
		}
	})

	t.Run("state dir read error", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		const slug = "sweep-child-statedir-read"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o300); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		var report StateSweepReport
		if err := report.sweepSlug(slug); err == nil {
			t.Fatal("expected the state-dir read error")
		}
	})

	t.Run("claims dir removal error", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		const slug = "sweep-child-claims-rm"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(filepath.Join(dir, claimsDirName), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		var report StateSweepReport
		if err := report.sweepSlug(slug); err == nil {
			t.Fatal("expected the claims-dir removal error")
		}
	})

	t.Run("slug dir removal error", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		const slug = "sweep-child-slug-rm"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		var report StateSweepReport
		if err := report.sweepSlug(slug); err == nil {
			t.Fatal("expected the slug-dir removal error")
		}
	})
}

// TestSweepClaimsPropagatesErrors pins the claim-token read failure and the
// unheld-claim removal failure.
func TestSweepClaimsPropagatesErrors(t *testing.T) {
	t.Run("token read error", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		const slug = "sweep-claims-read"
		dir := state.SlugDir(slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, filepath.Join(dir, claimsDirName), "")
		var report StateSweepReport
		if err := report.sweepClaims(slug); err == nil {
			t.Fatal("expected the claims read error")
		}
	})

	t.Run("removal error", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		const slug = "sweep-claims-rm"
		claimsDir := filepath.Join(state.SlugDir(slug), claimsDirName)
		if err := os.MkdirAll(claimsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, filepath.Join(claimsDir, "free.claim"), "")
		if err := os.Chmod(claimsDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(claimsDir, 0o700) })
		var report StateSweepReport
		if err := report.sweepClaims(slug); err == nil {
			t.Fatal("expected the claim removal error")
		}
	})
}

// TestSweepStatesAndLeasesPropagatesKeyStateError pins that a corrupt per-agent
// state fails the slug's sweep closed.
func TestSweepStatesAndLeasesPropagatesKeyStateError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "sweep-corrupt-agent"
	key := state.Key{Slug: slug, Agent: "opencode"}
	if err := os.MkdirAll(state.KeyDir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(state.KeyDir(key), "state.yaml"), "schema: [not-valid")

	var report StateSweepReport
	if err := report.sweepStatesAndLeases(slug); err == nil {
		t.Fatal("expected the corrupt per-agent state error")
	}
}

// TestSweepStatesAndLeasesMissingDirIsNoOp pins that a slug dir that is already
// gone is absence, not an error.
func TestSweepStatesAndLeasesMissingDirIsNoOp(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	var report StateSweepReport
	if err := report.sweepStatesAndLeases("sweep-states-missing"); err != nil {
		t.Fatalf("sweepStatesAndLeases on a missing dir = %v, want nil", err)
	}
}

// TestSweepKeyStatePropagatesErrors pins the two fail-closed paths of
// sweepKeyState: an unreadable state file and a removal that fails.
func TestSweepKeyStatePropagatesErrors(t *testing.T) {
	t.Run("read error", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		key := state.Key{Slug: "sweep-key-corrupt", Agent: "opencode"}
		if err := os.MkdirAll(state.KeyDir(key), 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.WritePath(t, filepath.Join(state.KeyDir(key), "state.yaml"), "schema: [not-valid")
		var report StateSweepReport
		if err := report.sweepKeyState(key); err == nil {
			t.Fatal("expected the state read error")
		}
	})

	t.Run("removal error", func(t *testing.T) {
		configpathstest.WithMockConfigPaths(t)
		const slug = "sweep-key-rm"
		key := state.Key{Slug: slug, Agent: "opencode"}
		if err := seedHomeState(key, state.HomeState{}); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		if err := os.Chmod(state.SlugDir(slug), 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(state.SlugDir(slug), 0o700) })
		var report StateSweepReport
		if err := report.sweepKeyState(key); err == nil {
			t.Fatal("expected the state removal error")
		}
	})
}

// TestRemoveEmptyClaimsDirPropagatesError pins that a claims dir that cannot be
// removed for a reason other than absence or non-emptiness is surfaced.
func TestRemoveEmptyClaimsDirPropagatesError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	const slug = "sweep-claimsdir-rm"
	dir := state.SlugDir(slug)
	if err := os.MkdirAll(filepath.Join(dir, claimsDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := removeEmptyClaimsDir(slug); err == nil {
		t.Fatal("expected the claims-dir removal error")
	}
}

// TestPruneStateArtifactsCountsLeasesAcrossAgents pins that the lease count
// accumulates over every agent of a slug: two agents each holding a stale lease
// report two, not the last agent's one.
func TestPruneStateArtifactsCountsLeasesAcrossAgents(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	testUI := termiotest.NewTestMock(t)
	const slug = "sweep-leases-two-agents"
	aged := time.Now().Add(-time.Hour)
	for _, agent := range []string{"opencode", "pi"} {
		clientsDir := filepath.Join(state.KeyDir(state.Key{Slug: slug, Agent: agent}), "clients")
		if err := os.MkdirAll(clientsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		lease := filepath.Join(clientsDir, "22222-4000000000.lock")
		testutil.WritePath(t, lease, "stale")
		if err := os.Chtimes(lease, aged, aged); err != nil {
			t.Fatal(err)
		}
	}

	report, err := PruneStateArtifacts(context.Background(), &testUI)
	if err != nil {
		t.Fatalf("PruneStateArtifacts: %v", err)
	}
	if report.LeasesRemoved != 2 {
		t.Errorf("LeasesRemoved = %d, want 2 (one per agent)", report.LeasesRemoved)
	}
}
