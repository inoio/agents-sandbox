package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestStopProjectVMGetSandboxError covers the non-not-found GetSandbox error
// branch in stopOrKillProjectVM.
func TestStopProjectVMGetSandboxError(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGetSandboxErr(errors.New("boom"))
	msb.WithMsbMock(t, client)

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping", client,
		func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err == nil {
		t.Fatal("expected error from GetSandbox")
	}
}

// TestStopProjectVMStopFnError covers the stopFn error branch and the remove
// error branch (remove fails after a successful stop).
func TestStopProjectVMStopFnError(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	}
	handle.StopErr = errors.New("stop failed")
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping", client,
		func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err == nil {
		t.Fatal("expected error from stopFn")
	}
}

func TestStopOrKillProjectVMDryRunNoRemove(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning})
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	err := stopOrKillProjectVM(context.Background(), false, true, &ui, testVMKey(), "stop", "Stopping",
		client, func(_ msb.SandboxHandle, _ context.Context) error { return nil })
	if err != nil {
		t.Fatalf("dry-run without remove: %v", err)
	}
	joined := joinStrings(ui.InfoCalls)
	if !contains(joined, "Would stop") {
		t.Errorf("expected a dry-run info message, got %v", ui.InfoCalls)
	}
	if strings.Contains(joined, "would remove") {
		t.Errorf("dry-run without remove should not mention removing state, got %v", ui.InfoCalls)
	}
}

func TestStopOrKillProjectVMRemoveSuccess(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	err := stopOrKillProjectVM(context.Background(), true, false, &ui, testVMKey(), "stop", "Stopping",
		client, func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err != nil {
		t.Fatalf("remove success: %v", err)
	}
	if !handle.DidRmv {
		t.Error("expected the sandbox handle to be removed")
	}
	if !contains(joinStrings(ui.VerboseCalls), "persisted state removed") {
		t.Errorf("expected a verbose message about removing state, got %v", ui.VerboseCalls)
	}
}

func TestStopOrKillProjectVMRemoveError(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:     "vm",
		Status_:   msbSdk.SandboxStatusRunning,
		RemoveErr: errors.New("remove failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	err := stopOrKillProjectVM(context.Background(), true, false, &ui, testVMKey(), "stop", "Stopping",
		client, func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err != nil {
		t.Fatalf("remove error should be non-fatal: %v", err)
	}
	if !contains(joinStrings(ui.WarnCalls), "failed to remove sandbox state") {
		t.Errorf("expected a warning about the failed remove, got %v", ui.WarnCalls)
	}
}

func TestStopOrKillProjectVMRejectsUnsafeStatuses(t *testing.T) {
	for _, status := range []msbSdk.SandboxStatus{
		msbSdk.SandboxStatusPaused,
		msbSdk.SandboxStatus("future-status"),
	} {
		t.Run(string(status), func(t *testing.T) {
			ui := termio.NewTestMock(t)
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(&msb.MockSandboxHandle{Name_: "vm", Status_: status})
			err := stopOrKillProjectVM(
				context.Background(),
				false,
				false,
				&ui,
				testVMKey(),
				"stop",
				"Stopping",
				client,
				func(msb.SandboxHandle, context.Context) error { return nil },
			)
			if err == nil {
				t.Fatalf("expected unsafe status %q to be rejected", status)
			}
		})
	}
}

// TestStopOrKillProjectVMInactiveWithoutRemoveLeavesState pins the unchanged
// inactive path: without remove, neither the action nor the removal runs.
func TestStopOrKillProjectVMInactiveWithoutRemoveLeavesState(t *testing.T) {
	for _, status := range inactiveStatuses {
		t.Run(string(status), func(t *testing.T) {
			ui := termio.NewTestMock(t)
			handle := inactiveHandle(status)
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(handle)

			err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping",
				client, failingStopFn(t))
			if err != nil {
				t.Fatalf("stop: %v", err)
			}
			if handle.DidRemove() {
				t.Error("removal must not run without remove")
			}
			if !strings.Contains(joinStrings(ui.InfoCalls), "already stopped") {
				t.Errorf("expected the already-stopped note, got %v", ui.InfoCalls)
			}
		})
	}
}

// TestStopOrKillProjectVMInactiveRemoveRemovesState pins the new behavior:
// remove on an inactive sandbox skips the action but still removes the state.
func TestStopOrKillProjectVMInactiveRemoveRemovesState(t *testing.T) {
	for _, status := range inactiveStatuses {
		t.Run(string(status), func(t *testing.T) {
			ui := termio.NewTestMock(t)
			handle := inactiveHandle(status)
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(handle)

			err := stopOrKillProjectVM(context.Background(), true, false, &ui, testVMKey(), "stop", "Stopping",
				client, failingStopFn(t))
			if err != nil {
				t.Fatalf("stop: %v", err)
			}
			if !handle.DidRemove() {
				t.Error("expected remove to delete state on an inactive sandbox")
			}
			if !strings.Contains(joinStrings(ui.VerboseCalls), "persisted state removed") {
				t.Errorf("expected a verbose removal note, got %v", ui.VerboseCalls)
			}
		})
	}
}

// TestStopOrKillProjectVMInactiveRemoveDryRunRemovesNothing pins that a dry run
// removes nothing even for an inactive sandbox, and says it would remove.
func TestStopOrKillProjectVMInactiveRemoveDryRunRemovesNothing(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := inactiveHandle(msbSdk.SandboxStatusStopped)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)

	err := stopOrKillProjectVM(context.Background(), true, true, &ui, testVMKey(), "stop", "Stopping",
		client, failingStopFn(t))
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if handle.DidRemove() {
		t.Error("a dry run must remove nothing")
	}
	if !strings.Contains(joinStrings(ui.InfoCalls), "dry-run: Would remove persisted state") {
		t.Errorf("expected a dry-run removal note, got %v", ui.InfoCalls)
	}
}

// TestStopOrKillProjectVMInactiveRemoveFailureWarns pins the non-fatal warning
// on the inactive path, mirroring the active path.
func TestStopOrKillProjectVMInactiveRemoveFailureWarns(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := inactiveHandle(msbSdk.SandboxStatusStopped)
	handle.RemoveErr = errors.New("remove failed")
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)

	err := stopOrKillProjectVM(context.Background(), true, false, &ui, testVMKey(), "stop", "Stopping",
		client, failingStopFn(t))
	if err != nil {
		t.Fatalf("removal failure must be non-fatal: %v", err)
	}
	if !strings.Contains(joinStrings(ui.WarnCalls), "failed to remove sandbox state") {
		t.Errorf("expected a warning about the failed removal, got %v", ui.WarnCalls)
	}
}

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

// TestStopAndKillProjectVMUnknownAgent covers the unknown-agent error branch
// in both StopProjectVM and KillProjectVM.
func TestStopAndKillProjectVMUnknownAgent(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	if err := StopProjectVM(
		context.Background(),
		configpaths.Get(),
		client,
		false,
		false,
		&ui,
		"no-such-agent",
	); err == nil {
		t.Fatal("expected error from StopProjectVM with an unknown agent")
	}
	if err := KillProjectVM(
		context.Background(),
		configpaths.Get(),
		client,
		false,
		false,
		&ui,
		"no-such-agent",
	); err == nil {
		t.Fatal("expected error from KillProjectVM with an unknown agent")
	}
}

// TestStopKillProjectVMErrorsWhenIdentityUnresolvable pins #354 on the stop/kill
// paths: a cwd whose git dir cannot be resolved aborts before the legacy
// identity adoption could act on it.
func TestStopKillProjectVMErrorsWhenIdentityUnresolvable(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))

	for _, action := range []struct {
		name string
		run  func(context.Context, configpaths.ConfigPaths, msb.Client, bool, bool, termio.UI, string) error
	}{
		{"stop", StopProjectVM},
		{"kill", KillProjectVM},
	} {
		t.Run(action.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			if err := action.run(
				context.Background(), configpaths.Get(), &msb.MockMsbClient{}, false, false, &ui, "opencode",
			); err == nil {
				t.Fatal("succeeded with an unresolvable identity, want an error")
			}
		})
	}
}

// TestStopKillProjectVMAdoptionFailureFailsClosed pins the fail-closed adoption
// wiring on stop/kill: an unreadable legacy state aborts before the VM lookup,
// so a first post-upgrade stop cannot act on a half-understood identity.
func TestStopKillProjectVMAdoptionFailureFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Chdir(newOriginClone(t))
	identity := mustIdentity(t, ".")
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("fixture must have distinct repo and checkout slugs")
	}

	legacyDir := state.KeyDir(state.Key{Slug: identity.RepoSlug, Agent: "opencode"})
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy state dir: %v", err)
	}
	testutil.WriteFile(t, legacyDir, "state.yaml", `": [`)

	msb.WithMsbMock(t, &msb.MockMsbClient{})

	for _, action := range []struct {
		name string
		run  func(context.Context, configpaths.ConfigPaths, msb.Client, bool, bool, termio.UI, string) error
	}{
		{"stop", StopProjectVM},
		{"kill", KillProjectVM},
	} {
		t.Run(action.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			err := action.run(
				context.Background(), configpaths.Get(), msb.Get(), false, false, &ui, "opencode",
			)
			if err == nil || !strings.Contains(err.Error(), "check for a pending legacy identity") {
				t.Fatalf("error = %v, want the fail-closed predicate error", err)
			}
		})
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

	err := StopProjectVM(context.Background(), configpaths.Get(), client, false, false, &ui, "opencode")
	if err == nil {
		t.Fatal("expected the stop failure to propagate out of StopProjectVM")
	}
	if !errors.Is(err, errVMSentinel) {
		t.Errorf("error = %v, want it to wrap the stop cause", err)
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
		run  func(context.Context, configpaths.ConfigPaths, msb.Client, bool, bool, termio.UI, string) error
	}{
		{"stop", StopProjectVM},
		{"kill", KillProjectVM},
	} {
		t.Run(action.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			if err := action.run(
				context.Background(), configpaths.Get(), client, false, true, &ui, "opencode",
			); err != nil {
				t.Fatalf("%s dry run: %v", action.name, err)
			}
			if !statePresent(t, legacyKey) {
				t.Error("a dry run must not apply the pending legacy migration")
			}
		})
	}
}

func TestStopProjectVMUsesClient(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	resolutions := msb.WithCountingMsbMock(t, client)

	// CheckoutSlug depends on the current directory, so use a temp repo.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), configpaths.Get(), client, false, false, ui, "opencode"); err != nil {
		t.Fatalf("StopProjectVM failed: %v", err)
	}
	if *resolutions != 0 {
		t.Errorf("msb.Get resolved %d times, want 0: StopProjectVM takes its client", *resolutions)
	}
}

func TestStopProjectVMAlreadyStopped(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
	})
	msb.WithMsbMock(t, client)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), configpaths.Get(), client, false, false, ui, "opencode"); err != nil {
		t.Fatalf("StopProjectVM on already-stopped VM failed: %v", err)
	}
	if !strings.Contains(joinStrings(ui.InfoCalls), "already stopped") {
		t.Errorf("expected the already-stopped note, got %v", ui.InfoCalls)
	}
}

func TestStopProjectVMDryRunRemove(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	msb.WithMsbMock(t, client)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), configpaths.Get(), client, true, true, ui, "opencode"); err != nil {
		t.Fatalf("StopProjectVM dry-run with remove failed: %v", err)
	}
}

func TestStopProjectVMNotFound(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	}
	msb.WithMsbMock(t, client)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), configpaths.Get(), client, false, false, ui, "opencode"); err != nil {
		t.Fatalf("StopProjectVM on missing VM failed: %v", err)
	}
	if !strings.Contains(joinStrings(ui.InfoCalls), "no project VM found") {
		t.Errorf("expected the missing-VM note, got %v", ui.InfoCalls)
	}
}

// TestKillProjectVMDryRunRemove covers the kill dry-run branch and the
// actionWord selection.
func TestKillProjectVMDryRunRemove(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	msb.WithMsbMock(t, client)

	if err := KillProjectVM(context.Background(), configpaths.Get(), client, true, true, &ui, "opencode"); err != nil {
		t.Fatalf("KillProjectVM dry-run: %v", err)
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

	err := KillProjectVM(context.Background(), configpaths.Get(), client, false, false, &ui, "opencode")
	if err == nil {
		t.Fatal("expected the kill failure to propagate out of KillProjectVM")
	}
	if !errors.Is(err, errVMSentinel) {
		t.Errorf("error = %v, want it to wrap the kill cause", err)
	}
}

func TestKillProjectVMUsesClient(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	resolutions := msb.WithCountingMsbMock(t, client)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := KillProjectVM(context.Background(), configpaths.Get(), client, false, false, ui, "opencode"); err != nil {
		t.Fatalf("KillProjectVM failed: %v", err)
	}
	if *resolutions != 0 {
		t.Errorf("msb.Get resolved %d times, want 0: KillProjectVM takes its client", *resolutions)
	}
}

// mustIdentity resolves a fixture cwd to its project identity, failing the test
// on the now-fallible ResolveIdentity: fixtures always point at real paths.
func mustIdentity(t testing.TB, cwd string) git.Identity {
	t.Helper()
	identity, err := git.ResolveIdentity(cwd)
	require.NoError(t, err)
	return identity
}

// unresolvableIdentityDir returns a directory whose .git file points at a git
// directory that does not exist, so git.ResolveIdentity fails for it while the
// directory itself still resolves.
func unresolvableIdentityDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".git"),
		[]byte("gitdir: "+filepath.Join(t.TempDir(), "gone.git")+"\n"),
		0o644,
	))
	return dir
}

// cloneOrigin is the shared origin URL for the identity fixtures, so two clones
// map to one repo slug while keeping distinct checkout slugs.
const cloneOrigin = "https://example.com/acme/widget.git"

// newOriginClone builds a minimal origin-holding repo layout (a .git dir whose
// config names the shared origin), the same fixture shape used by internal/git's
// identity tests. It returns the symlink-resolved path so assertions match what
// ResolveIdentity derives.
func newOriginClone(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	config := "[remote \"origin\"]\n\turl = " + cloneOrigin + "\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	return root
}

// inactiveStatuses are the statuses IsSandboxInactive treats as having no live
// runtime, so stop/kill must skip the action and remove only persisted state.
var inactiveStatuses = []msbSdk.SandboxStatus{
	msbSdk.SandboxStatusCreated,
	msbSdk.SandboxStatusStopped,
	msbSdk.SandboxStatusCrashed,
}

// failingStopFn records that the stop/kill action must not run on an inactive
// sandbox; it fails the test if invoked.
func failingStopFn(t *testing.T) func(msb.SandboxHandle, context.Context) error {
	t.Helper()
	return func(msb.SandboxHandle, context.Context) error {
		t.Error("stop/kill action must not run on an inactive sandbox")
		return nil
	}
}

// inactiveHandle builds a mock handle in the given inactive status.
func inactiveHandle(status msbSdk.SandboxStatus) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{Name_: "vm", Status_: status}
}

// errVMSentinel is the cause a failing stop/kill function returns, so a test can
// prove the wrapping relationship survives (%w, not %v).
var errVMSentinel = errors.New("vm stop boom")
