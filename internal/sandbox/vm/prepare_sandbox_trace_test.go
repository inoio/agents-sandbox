package vm

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	sandboximage "github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/lifecycle"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// vmOwnedStages returns the stages PrepareSandbox owns, in the order of
// specs/lifecycle.md: resolve-identity through setup-sandbox.
func vmOwnedStages(t *testing.T) []lifecycle.StageID {
	t.Helper()
	order := lifecycle.Order()
	first := slices.Index(order, lifecycle.ResolveIdentity)
	last := slices.Index(order, lifecycle.SetupSandbox)
	if first < 0 || last < first {
		t.Fatalf("lifecycle.Order() = %v lacks the vm-owned stage range", order)
	}
	return order[first : last+1]
}

// TestPrepareSandboxSkipVMRecordsStagesInOrder pins the recorded order of a
// --dry-run-vm prepare: every vm-owned stage is reached in lifecycle order, the
// VM ones included, so reordering, adding or removing a stage in
// PrepareSandbox's list changes the trace and fails here.
func TestPrepareSandboxSkipVMRecordsStagesInOrder(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clone := newOriginClone(t)
	installPrepareSandboxDockerMock(t)
	installPrepareSandboxDaemonMock(t)
	ui := termio.NewTestMock(t)
	t.Chdir(clone)
	installPrepareSandboxMSBMock(t)

	trace := &lifecycle.Trace{}
	session, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode", Trace: trace}, true, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox (skip VM): %v", err)
	}
	defer session.Cleanup()

	if got, want := trace.Stages(), vmOwnedStages(t); !slices.Equal(got, want) {
		t.Errorf("trace = %v, want %v", got, want)
	}
}

// TestPrepareSandboxTraceEndsAtTheFailingStage pins that a failing stage is
// the last one recorded: an unknown agent fails resolve-identity, so no later
// stage is reached.
func TestPrepareSandboxTraceEndsAtTheFailingStage(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Chdir(newOriginClone(t))
	ui := termio.NewTestMock(t)

	trace := &lifecycle.Trace{}
	if _, err := PrepareSandbox(
		context.Background(), options.RunOptions{Agent: "no-such-agent", Trace: trace}, true, &ui,
	); err == nil {
		t.Fatal("PrepareSandbox with an unknown agent: want an error")
	}
	if got, want := trace.Stages(), []lifecycle.StageID{lifecycle.ResolveIdentity}; !slices.Equal(got, want) {
		t.Errorf("trace = %v, want %v", got, want)
	}
}

// TestEnsureVMStageFailureSkipsFingerprints pins the ensure-vm error path: a
// failed per-project flock acquisition is returned with no sandbox, and the
// fingerprints are not persisted, since no VM was created.
func TestEnsureVMStageFailureSkipsFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	lockErr := errors.New("flock boom")
	origLock := acquireProjectVMLock
	acquireProjectVMLock = func(state.Key) (func(), error) { return nil, lockErr }
	t.Cleanup(func() { acquireProjectVMLock = origLock })
	k := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	ui := termio.NewTestMock(t)

	sb, _, err := ensureVMStage(
		context.Background(), sessionIdentity{key: k}, sandboximage.ImageInfo{}, preparedHome{},
		reconfigPlan{homeVolume: "home-vol"}, false, options.RunOptions{}, &ui,
	)
	if !errors.Is(err, lockErr) {
		t.Fatalf("ensureVMStage error = %v, want %v", err, lockErr)
	}
	if sb != nil {
		t.Errorf("ensureVMStage sandbox = %v, want nil on failure", sb)
	}
	if statePresent(t, k) {
		t.Error("a failed ensure-vm persisted state")
	}
}

// TestPrepareSandboxVMStageFailureStopsAtEnsureVM pins the ensure-vm error path
// through the ordered runner: a failing per-project flock acquisition fails
// ensure-vm, so the trace ends there — setup-sandbox is never reached — and the
// error propagates to the caller.
func TestPrepareSandboxVMStageFailureStopsAtEnsureVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clone := newOriginClone(t)
	installPrepareSandboxDockerMock(t)
	installPrepareSandboxDaemonMock(t)
	ui := termio.NewTestMock(t)
	t.Chdir(clone)
	installPrepareSandboxMSBMock(t)

	lockErr := errors.New("flock boom")
	origLock := acquireProjectVMLock
	acquireProjectVMLock = func(state.Key) (func(), error) { return nil, lockErr }
	t.Cleanup(func() { acquireProjectVMLock = origLock })

	trace := &lifecycle.Trace{}
	_, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode", Trace: trace}, false, &ui)
	if !errors.Is(err, lockErr) {
		t.Fatalf("PrepareSandbox error = %v, want %v", err, lockErr)
	}
	owned := vmOwnedStages(t)
	want := owned[:len(owned)-1] // resolve-identity .. ensure-vm, without setup-sandbox
	if got := trace.Stages(); !slices.Equal(got, want) {
		t.Errorf("trace = %v, want %v (ends at the failing ensure-vm)", got, want)
	}
}
