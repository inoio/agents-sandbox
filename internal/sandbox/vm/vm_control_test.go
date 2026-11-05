package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// TestStopOrKillProjectVM pins how stop and kill act on the project VM by its
// status: a running VM is stopped or killed (and its persisted state removed on
// request), an inactive one is left as it is and only removed on request, and
// a paused or unknown one is refused. A dry run only reports what it would do;
// a failed removal is a warning, a failed stop or kill keeps its cause.
func TestStopOrKillProjectVM(t *testing.T) {
	lookupFailed := errors.New("lookup failed")
	actionFailed := errors.New("action failed")
	cases := []struct {
		name      string
		handle    *msb.MockSandboxHandle // nil: no project VM
		lookupErr error
		action    string
		remove    bool
		dryRun    bool
		actionErr error

		wantErr     func(error) bool
		wantActed   bool
		wantRemoved bool
		wantInfo    string
		wantNoInfo  string
		wantWarn    string
		wantVerbose string
	}{
		{name: "no project VM", action: "stop", wantInfo: "no project VM found: testproj-opencode"},
		{
			name:      "lookup fails",
			lookupErr: lookupFailed,
			action:    "stop",
			wantErr:   func(err error) bool { return errors.Is(err, lookupFailed) },
		},
		{
			name:      "running VM is stopped under its resolved name",
			handle:    &msb.MockSandboxHandle{Name_: "resolved-name", Status_: msbSdk.SandboxStatusRunning},
			action:    "stop",
			wantActed: true,
			wantInfo:  "stopped project VM: resolved-name",
		},
		{
			name:      "running VM is killed",
			handle:    &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
			action:    "kill",
			wantActed: true,
			wantInfo:  "killed project VM: vm",
		},
		{
			name:        "running VM is stopped and removed",
			handle:      &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
			action:      "stop",
			remove:      true,
			wantActed:   true,
			wantRemoved: true,
			wantVerbose: "persisted state removed: vm",
		},
		{
			name: "failed removal after a stop warns",
			handle: &msb.MockSandboxHandle{
				Name_: "vm", Status_: msbSdk.SandboxStatusRunning, RemoveErr: errors.New("remove failed"),
			},
			action:    "stop",
			remove:    true,
			wantActed: true,
			wantWarn:  "failed to remove sandbox state: remove failed",
		},
		{
			name:      "failed stop keeps its cause and removes nothing",
			handle:    &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
			action:    "stop",
			remove:    true,
			actionErr: actionFailed,
			wantErr:   func(err error) bool { return errors.Is(err, actionFailed) },
			wantActed: true,
		},
		{
			name:        "inactive VM is only removed",
			handle:      inactiveHandle(msbSdk.SandboxStatusStopped),
			action:      "stop",
			remove:      true,
			wantRemoved: true,
			wantInfo:    "project VM already stopped: vm",
		},
		{
			name: "failed removal of an inactive VM warns",
			handle: &msb.MockSandboxHandle{
				Name_:     "vm",
				Status_:   msbSdk.SandboxStatusStopped,
				RemoveErr: errors.New("remove failed"),
			},
			action:   "kill",
			remove:   true,
			wantInfo: "project VM already killed: vm",
			wantWarn: "failed to remove sandbox state: remove failed",
		},
		{
			name:     "dry run on an inactive VM removes nothing",
			handle:   inactiveHandle(msbSdk.SandboxStatusStopped),
			action:   "stop",
			remove:   true,
			dryRun:   true,
			wantInfo: "dry-run: Would remove persisted state of stopped project VM: vm",
		},
		{
			name:       "dry run stop",
			handle:     &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
			action:     "stop",
			dryRun:     true,
			wantInfo:   "dry-run: Would stop project VM: vm",
			wantNoInfo: "would remove",
		},
		{
			name:     "dry run kill with removal",
			handle:   &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
			action:   "kill",
			remove:   true,
			dryRun:   true,
			wantInfo: "dry-run: Would kill project VM: vm (also would remove persisted state)",
		},
		{
			name:    "paused VM is refused",
			handle:  &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusPaused},
			action:  "stop",
			wantErr: func(err error) bool { return strings.Contains(err.Error(), `cannot stop paused sandbox "vm"`) },
		},
		{
			name:   "unknown status is refused",
			handle: &msb.MockSandboxHandle{Name_: "vm", Status_: "future-status"},
			action: "kill",
			wantErr: func(err error) bool {
				return strings.Contains(err.Error(), `cannot kill sandbox "vm" with unexpected status "future-status"`)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			client := &msb.MockMsbClient{}
			switch {
			case tc.lookupErr != nil:
				client.SetGetSandboxErr(tc.lookupErr)
			case tc.handle != nil:
				client.SetGotSandbox(tc.handle)
			default:
				client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
					return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
				}
			}
			acted := false
			act := func(msb.SandboxHandle, context.Context) error {
				acted = true
				return tc.actionErr
			}

			err := stopOrKillProjectVM(context.Background(), tc.remove, tc.dryRun, &ui, testVMKey(),
				tc.action, "Acting on", client, act)

			if tc.wantErr != nil {
				if err == nil || !tc.wantErr(err) {
					t.Fatalf("error = %v, want the expected failure", err)
				}
			} else if err != nil {
				t.Fatalf("stopOrKillProjectVM: %v", err)
			}
			if acted != tc.wantActed {
				t.Errorf("acted = %v, want %v", acted, tc.wantActed)
			}
			if tc.wantActed && tc.actionErr == nil && (ui.SpinnerStops != 1 || len(ui.SpinnerStopErrors) != 0) {
				t.Errorf("spinner stops %d, errors %v; want one clean stop", ui.SpinnerStops, ui.SpinnerStopErrors)
			}
			if tc.actionErr != nil && len(ui.SpinnerStopErrors) != 1 {
				t.Errorf("spinner stop errors = %v, want the action's error", ui.SpinnerStopErrors)
			}
			if tc.handle != nil && tc.handle.DidRemove() != tc.wantRemoved {
				t.Errorf("removed = %v, want %v", tc.handle.DidRemove(), tc.wantRemoved)
			}
			for _, want := range []struct {
				calls []string
				text  string
			}{{ui.InfoCalls, tc.wantInfo}, {ui.WarnCalls, tc.wantWarn}, {ui.VerboseCalls, tc.wantVerbose}} {
				if want.text != "" &&
					!slices.ContainsFunc(want.calls, func(c string) bool { return strings.Contains(c, want.text) }) {
					t.Errorf("messages %v, want %q", want.calls, want.text)
				}
			}
			if tc.wantNoInfo != "" && strings.Contains(strings.Join(ui.InfoCalls, "\n"), tc.wantNoInfo) {
				t.Errorf("info %v must not mention %q", ui.InfoCalls, tc.wantNoInfo)
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
			if !strings.Contains(strings.Join(ui.InfoCalls, "\n"), "already stopped") {
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
			if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "persisted state removed") {
				t.Errorf("expected a verbose removal note, got %v", ui.VerboseCalls)
			}
		})
	}
}

// stopAndKillCommands are the two exported commands over stopOrKillProjectVM.
var stopAndKillCommands = []struct {
	name string
	run  func(context.Context, configpaths.ConfigPaths, msb.Client, bool, bool, termio.UI, string) error
}{
	{"stop", StopProjectVM},
	{"kill", KillProjectVM},
}

// TestStopAndKillProjectVMRunTheirAction pins that StopProjectVM stops and
// KillProjectVM kills the current checkout's project VM through the client the
// command resolved (never the process global), returning the action's failure
// with its cause.
func TestStopAndKillProjectVMRunTheirAction(t *testing.T) {
	for _, command := range stopAndKillCommands {
		t.Run(command.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			t.Chdir(testutil.InitRepo(t))
			handle := &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning}
			if command.name == "stop" {
				handle.StopErr = errVMSentinel
			} else {
				handle.KillErr = errVMSentinel
			}
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(handle)
			resolutions := msb.WithCountingMsbMock(t, client)
			ui := termio.NewTestMock(t)

			err := command.run(context.Background(), configpaths.Get(), client, false, false, &ui, "opencode")
			if !errors.Is(err, errVMSentinel) {
				t.Errorf("error = %v, want the %s failure %v", err, command.name, errVMSentinel)
			}
			if handle.DidStop != (command.name == "stop") || handle.DidKill != (command.name == "kill") {
				t.Errorf("stopped %v, killed %v; want only %s", handle.DidStop, handle.DidKill, command.name)
			}
			if *resolutions != 0 {
				t.Errorf("msb.Get resolved %d times, want 0: the command takes its client", *resolutions)
			}
		})
	}
}

// TestStopAndKillProjectVMRejectAnUnknownAgent pins that both commands refuse
// an agent name that is not registered.
func TestStopAndKillProjectVMRejectAnUnknownAgent(t *testing.T) {
	for _, command := range stopAndKillCommands {
		t.Run(command.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			err := command.run(
				context.Background(),
				configpaths.Get(),
				&msb.MockMsbClient{},
				false,
				false,
				&ui,
				"no-such-agent",
			)
			if err == nil || !strings.Contains(err.Error(), `unknown agent "no-such-agent"`) {
				t.Fatalf("error = %v, want the unknown agent refused", err)
			}
		})
	}
}

// TestStopKillProjectVMErrorsWhenIdentityUnresolvable pins #354 on the stop/kill
// paths: a cwd whose git dir cannot be resolved aborts before the legacy
// identity adoption could act on it.
func TestStopKillProjectVMErrorsWhenIdentityUnresolvable(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))

	for _, action := range stopAndKillCommands {
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

	for _, action := range stopAndKillCommands {
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

	for _, action := range stopAndKillCommands {
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
