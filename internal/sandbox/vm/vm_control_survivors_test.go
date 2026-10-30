package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// errVMSentinel is the cause a failing stop/kill function returns, so a test can
// prove the wrapping relationship survives (%w, not %v).
var errVMSentinel = errors.New("vm stop boom")

// TestStopOrKillProjectVMReportsResolvedHandleName pins that the user-facing
// message names the handle's resolved name (which can differ from the requested
// one) and that a successful call stops its spinner.
func TestStopOrKillProjectVMReportsResolvedHandleName(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{Name_: "resolved-sandbox-name", Status_: msbSdk.SandboxStatusRunning}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping",
		client, func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	joined := joinStrings(ui.InfoCalls)
	if !strings.Contains(joined, "resolved-sandbox-name") {
		t.Errorf("expected the resolved handle name in the message, got %v", ui.InfoCalls)
	}
	if !strings.Contains(joined, "stopped project VM") {
		t.Errorf("expected the past-tense stop message, got %v", ui.InfoCalls)
	}
	if ui.SpinnerStops != 1 {
		t.Errorf("spinner stops = %d, want one", ui.SpinnerStops)
	}
}

// TestStopOrKillProjectVMDryRunKillWithRemove pins the kill action word and the
// remove note on a dry run: the action word distinguishes kill from stop, and the
// remove note names the persisted state the caller also asked to remove.
func TestStopOrKillProjectVMDryRunKillWithRemove(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning})
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	err := stopOrKillProjectVM(context.Background(), true, true, &ui, testVMKey(), "kill", "Force-killing",
		client, func(msb.SandboxHandle, context.Context) error { return nil })
	if err != nil {
		t.Fatalf("kill dry-run with remove: %v", err)
	}
	joined := joinStrings(ui.InfoCalls)
	if !strings.Contains(joined, "Would kill") {
		t.Errorf("expected the kill action word, got %v", ui.InfoCalls)
	}
	if !strings.Contains(joined, "also would remove persisted state") {
		t.Errorf("expected the remove note on a dry run, got %v", ui.InfoCalls)
	}
}

// TestStopOrKillProjectVMStopErrorWrapsCauseAndStopsSpinner pins the failure
// path: the stop function's cause is preserved and the spinner is stopped with
// the error rather than left running.
func TestStopOrKillProjectVMStopErrorWrapsCauseAndStopsSpinner(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning})
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping", client,
		func(msb.SandboxHandle, context.Context) error { return errVMSentinel })
	if !errors.Is(err, errVMSentinel) {
		t.Errorf("error = %v, want it to wrap the stop cause", err)
	}
	if len(ui.SpinnerStopErrors) != 1 {
		t.Errorf("spinner stop errors = %v, want one", ui.SpinnerStopErrors)
	}
}

// TestStopProjectVMWrapsStopError drives StopProjectVM end to end so its own
// return of the teardown result is exercised: a failing handle stop propagates.
func TestStopProjectVMWrapsStopError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:   "vm",
		Status_: msbSdk.SandboxStatusRunning,
		StopErr: errVMSentinel,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	t.Chdir(testutil.InitRepo(t))

	err := StopProjectVM(context.Background(), false, false, &ui, "opencode")
	if err == nil {
		t.Fatal("expected the stop failure to propagate out of StopProjectVM")
	}
	if !errors.Is(err, errVMSentinel) {
		t.Errorf("error = %v, want it to wrap the stop cause", err)
	}
}

// TestKillProjectVMWrapsKillError is the kill counterpart: KillProjectVM returns
// its teardown result, with the kill cause preserved.
func TestKillProjectVMWrapsKillError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:   "vm",
		Status_: msbSdk.SandboxStatusRunning,
		KillErr: errVMSentinel,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	t.Chdir(testutil.InitRepo(t))

	err := KillProjectVM(context.Background(), false, false, &ui, "opencode")
	if err == nil {
		t.Fatal("expected the kill failure to propagate out of KillProjectVM")
	}
	if !errors.Is(err, errVMSentinel) {
		t.Errorf("error = %v, want it to wrap the kill cause", err)
	}
}

// TestStopKillProjectVMDryRunLeavesLegacyState pins the apply flag on the
// adoption call: a dry run passes apply=false, so a pending legacy migration is
// left in place rather than applied as a side effect of a read-only stop/kill.
func TestStopKillProjectVMDryRunLeavesLegacyState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Chdir(newOriginClone(t))
	identity := mustIdentity(t, ".")
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("fixture must have distinct repo and checkout slugs")
	}

	legacyKey := state.Key{Slug: identity.RepoSlug, Agent: "opencode"}
	if err := seedHomeState(legacyKey, state.HomeState{HomeVolume: "home-legacy"}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}
	msb.WithMsbMock(t, client)

	for _, action := range []struct {
		name string
		run  func(context.Context, bool, bool, termio.UI, string) error
	}{
		{"stop", StopProjectVM},
		{"kill", KillProjectVM},
	} {
		t.Run(action.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			if err := action.run(context.Background(), false, true, &ui, "opencode"); err != nil {
				t.Fatalf("%s dry run: %v", action.name, err)
			}
			if !statePresent(t, legacyKey) {
				t.Error("a dry run must not apply the pending legacy migration")
			}
		})
	}
}
