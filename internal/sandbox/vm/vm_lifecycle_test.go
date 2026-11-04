package vm

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestLifecycleSpinnerIsFinishedOnPreLockResolution pins that every pre-lock
// transition branch finishes its "Checking project VM" spinner — a successful
// path stops it, a failing one stops it with the error — so a resolution never
// leaves the spinner running behind whatever is printed next.
//
// The post-lock re-check passes a nil spinner, so this covers the half of the
// shared transition whose spinner is non-nil.
func TestLifecycleSpinnerIsFinishedOnPreLockResolution(t *testing.T) {
	cases := []struct {
		name           string
		handle         func() msb.SandboxHandle
		wantErr        bool
		wantStops      int
		wantStopErrors int
	}{
		{
			name: "decide error",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatus("unknown-status")}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "wait error",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   "vm",
					Status_: msbSdk.SandboxStatusStarting,
					RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
						return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusPaused}, nil
					},
				}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "wait success",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   "vm",
					Status_: msbSdk.SandboxStatusStarting,
					RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
						return &msb.MockSandboxHandle{
							Status_:   msbSdk.SandboxStatusRunning,
							ConnectSb: &msb.MockSandbox{Name_: "vm"},
						}, nil
					},
				}
			},
			wantStops: 1,
		},
		{
			name: "start success",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   "vm",
					Status_: msbSdk.SandboxStatusStopped,
					StartSb: &msb.MockSandbox{Name_: "vm"},
				}
			},
			wantStops: 1,
		},
		{
			name: "start error",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:    "vm",
					Status_:  msbSdk.SandboxStatusStopped,
					StartErr: errors.New("start failed"),
				}
			},
			wantErr: true,
			// The spinner is stopped before the start is attempted (it would
			// otherwise animate through it) and then stopped again with the
			// error; the real spinner's second finish is a no-op, the mock counts
			// both.
			wantStops:      1,
			wantStopErrors: 1,
		},
		{
			name: "connect success",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:     "vm",
					Status_:   msbSdk.SandboxStatusRunning,
					ConnectSb: &msb.MockSandbox{Name_: "vm"},
				}
			},
			wantStops: 1,
		},
		{
			name: "connect retry refresh error",
			handle: func() msb.SandboxHandle {
				return &retryConnectHandle{
					SandboxHandle: &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
					refreshErr:    errors.New("refresh failed"),
				}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "connect retry unavailable status",
			handle: func() msb.SandboxHandle {
				return &retryConnectHandle{
					SandboxHandle: &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
					refresh:       &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusPaused},
				}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "connect retry resume error",
			handle: func() msb.SandboxHandle {
				// No RefreshFn, so the retry re-reads the same running status and
				// the second connect fails the same way.
				return &msb.MockSandboxHandle{
					Name_:      "vm",
					Status_:    msbSdk.SandboxStatusRunning,
					ConnectErr: errors.New("connect failed"),
				}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "connect retry reconnect",
			handle: func() msb.SandboxHandle {
				return &retryConnectHandle{
					SandboxHandle: &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning},
					refresh: &msb.MockSandboxHandle{
						Status_:   msbSdk.SandboxStatusRunning,
						ConnectSb: &msb.MockSandbox{Name_: "vm"},
					},
				}
			},
			wantStops: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			ui := termio.NewTestMock(t)
			client := &msb.MockMsbClient{}
			client.SetGotSandbox(tc.handle())
			msb.WithMsbMock(t, client)

			_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
				Client:     client,
				Options:    options.RunOptions{Agent: "opencode"},
				Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
				HomeVolume: "vol",
				RepoPath:   "/workspace",
				Key:        testVMKey(),
				RepoSlug:   testVMKey().Slug,
				SkipVM:     false,
				UI:         &ui,
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("EnsureProjectVM error = %v, wantErr %v", err, tc.wantErr)
			}
			if ui.SpinnerStops != tc.wantStops {
				t.Errorf("spinner stops = %d, want %d", ui.SpinnerStops, tc.wantStops)
			}
			if len(ui.SpinnerStopErrors) != tc.wantStopErrors {
				t.Errorf("spinner stop errors = %v, want %d", ui.SpinnerStopErrors, tc.wantStopErrors)
			}
		})
	}
}

func TestEnsureProjectVMCreatesWhenNotFound(t *testing.T) {
	notFoundErr := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	decision, err := decideVMAction(notFoundErr, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionCreate {
		t.Errorf("expected vmActionCreate, got %v", decision)
	}
}

func TestEnsureProjectVMConnectsWhenRunning(t *testing.T) {
	decision, err := decideVMAction(nil, msbSdk.SandboxStatusRunning)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionConnect {
		t.Errorf("expected vmActionConnect, got %v", decision)
	}
}

func TestEnsureProjectVMStartsWhenStopped(t *testing.T) {
	decision, err := decideVMAction(nil, msbSdk.SandboxStatusStopped)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionStart {
		t.Errorf("expected vmActionStart, got %v", decision)
	}
}

func TestEnsureProjectVMStartsWhenCrashed(t *testing.T) {
	decision, err := decideVMAction(nil, msbSdk.SandboxStatusCrashed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionStart {
		t.Errorf("expected vmActionStart for crashed, got %v", decision)
	}
}

func TestEnsureProjectVMStartsWhenCreated(t *testing.T) {
	decision, err := decideVMAction(nil, msbSdk.SandboxStatusCreated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionStart {
		t.Errorf("expected vmActionStart for created, got %v", decision)
	}
}

func TestEnsureProjectVMWaitsWhenStarting(t *testing.T) {
	decision, err := decideVMAction(nil, msbSdk.SandboxStatusStarting)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != vmActionWait {
		t.Errorf("expected vmActionWait for starting, got %v", decision)
	}
}

func TestEnsureProjectVMRejectsNonAttachableStatuses(t *testing.T) {
	for _, status := range []msbSdk.SandboxStatus{
		msbSdk.SandboxStatusDraining,
		msbSdk.SandboxStatusPaused,
		msbSdk.SandboxStatus("unknown-status"),
	} {
		t.Run(string(status), func(t *testing.T) {
			if _, err := decideVMAction(nil, status); err == nil {
				t.Fatalf("decideVMAction(%q) returned nil error", status)
			}
		})
	}
}

// TestProjectVMLockSeamResolution pins the seam contract: a nil
// options.Seams.ProjectVMLock uses the production default (acquireProjectVMLock),
// while a set field is used as-is.
func TestProjectVMLockSeamResolution(t *testing.T) {
	orig := acquireProjectVMLock
	t.Cleanup(func() { acquireProjectVMLock = orig })

	defaultErr := errors.New("production default lock")
	acquireProjectVMLock = func(state.Key) (func(), error) { return nil, defaultErr }
	if _, err := projectVMLock(options.Seams{})(testVMKey()); !errors.Is(err, defaultErr) {
		t.Errorf("nil seam lock error = %v, want the production default %v", err, defaultErr)
	}

	seamErr := errors.New("injected lock")
	seam := func(state.Key) (func(), error) { return nil, seamErr }
	if _, err := projectVMLock(options.Seams{ProjectVMLock: seam})(testVMKey()); !errors.Is(err, seamErr) {
		t.Errorf("set seam lock error = %v, want %v", err, seamErr)
	}
}

// TestEnsureProjectVMFlockError covers the branch where acquiring the
// per-project flock fails on the create path.
func TestEnsureProjectVMFlockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.MustCheckoutSlug()
	k := state.Key{Slug: slug, Agent: "opencode"}
	// A directory at the lock path makes os.OpenFile fail, exercising the flock
	// acquisition error branch.
	if err := os.MkdirAll(filepath.Join(state.ProjectVMLockDir(k), "ensure-vm.lock"), 0o750); err != nil {
		t.Fatal(err)
	}

	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        k,
		RepoSlug:   k.Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error when acquiring the project flock fails")
	}
	if !contains(err.Error(), "acquire project flock") {
		t.Errorf("expected a flock-acquisition error, got %v", err)
	}
}

// TestEnsureProjectVMSkipVM covers the skip-VM early-return branch.
func TestEnsureProjectVMSkipVM(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     &msb.MockMsbClient{},
		Options:    options.RunOptions{},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     true,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM dry-run: %v", err)
	}
	if sb != nil {
		t.Error("expected nil sandbox for dry-run-vm")
	}
	if boot != VMBootConnected {
		t.Errorf("expected VMBootConnected for dry-run, got %v", boot)
	}
	if !verboseContains(ui.VerboseCalls, "dry-run: VM lifecycle skipped") {
		t.Errorf("verbose output = %v, want the dry-run skip note", ui.VerboseCalls)
	}
}

// TestEnsureProjectVMGetSandboxFatalError covers the non-not-found GetSandbox
// error branch in EnsureProjectVM.
func TestEnsureProjectVMGetSandboxFatalError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGetSandboxErr(errors.New("boom"))
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error from GetSandbox")
	}
}

// TestEnsureProjectVMConnectRetryReconnectSucceeds covers the connect-failure
// retry path where, after a refresh, the VM is still active and the re-connect
// succeeds: the session is returned as connected.
func TestEnsureProjectVMConnectRetryReconnectSucceeds(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	refreshed := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: refreshed,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM connect-retry reconnect: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootConnected {
		t.Errorf("expected VMBootConnected, got %v", boot)
	}
}

// TestEnsureProjectVMConnectRetryThenStart covers the connect-failure retry path
// where, after a refresh, the VM is no longer active and is instead started.
func TestEnsureProjectVMConnectRetryThenStart(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	refreshed := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
		StartSb: &msb.MockSandbox{Name_: "vm"},
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: refreshed,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM connect-retry start: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootStarted {
		t.Errorf("expected VMBootStarted, got %v", boot)
	}
}

// TestEnsureProjectVMPostLockDecideError covers the post-lock recheck branch
// where the existing VM has an unrecognized status.
func TestEnsureProjectVMPostLockDecideError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatus("unknown-status"),
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return handle, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error for an unrecognized status in the post-lock recheck")
	}
}

// TestEnsureProjectVMPostLockConnectFailThenStart covers the post-lock recheck
// branch where connecting to a running VM fails, the refreshed VM is no longer
// active, and it is started: the post-lock path routes a connect failure
// through the same refresh-and-resume recovery the fast path uses.
func TestEnsureProjectVMPostLockConnectFailThenStart(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	started := &msb.MockSandbox{Name_: "vm"}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusStopped,
			StartSb: started,
		},
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = postLockGetSandbox(handle)
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM post-lock connect-fail start: %v", err)
	}
	if sb != started {
		t.Fatalf("sandbox = %v, want the started sandbox", sb)
	}
	if boot != VMBootStarted {
		t.Errorf("expected VMBootStarted, got %v", boot)
	}
	if !contains(joinStrings(ui.VerboseCalls), "retrying via Start") {
		t.Errorf("expected a verbose message about the connect retry, got %v", ui.VerboseCalls)
	}
}

// TestEnsureProjectVMPostLockConnectRetryReconnectSucceeds covers the post-lock
// recheck where a connect failure is recovered by the retry, not by a bare
// Start: the refreshed VM is still running and the re-connect succeeds.
func TestEnsureProjectVMPostLockConnectRetryReconnectSucceeds(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	connected := &msb.MockSandbox{Name_: "vm"}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: &msb.MockSandboxHandle{
			Name_:     "agents-sandbox-vm-test",
			Status_:   msbSdk.SandboxStatusRunning,
			ConnectSb: connected,
		},
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = postLockGetSandbox(handle)
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM post-lock connect retry: %v", err)
	}
	if sb != connected {
		t.Fatalf("sandbox = %v, want the reconnected sandbox", sb)
	}
	if boot != VMBootConnected {
		t.Errorf("expected VMBootConnected, got %v", boot)
	}
}

// TestEnsureProjectVMPostLockConnectRetryRefreshError covers the post-lock
// recheck where the connect failure's retry cannot refresh the handle.
func TestEnsureProjectVMPostLockConnectRetryRefreshError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refreshErr: errors.New("refresh failed"),
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = postLockGetSandbox(handle)
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error when refreshing the handle after a post-lock connect failure")
	}
}

// TestEnsureProjectVMPostLockStartError covers the post-lock recheck branch
// where starting the stopped VM fails.
func TestEnsureProjectVMPostLockStartError(t *testing.T) {
	causeErr := errors.New("start failed")
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	handle := &msb.MockSandboxHandle{
		Name_:    "agents-sandbox-vm-test",
		Status_:  msbSdk.SandboxStatusStopped,
		StartErr: causeErr,
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return handle, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if !errors.Is(err, causeErr) {
		t.Fatalf("%s: error = %v, want it to wrap %v", "expected error when the post-lock start fails", err, causeErr)
	}
}

// TestEnsureProjectVMPostLockConnectReconcileWarn covers the reconcile-failure
// warning on the post-lock connect path.
func TestEnsureProjectVMPostLockConnectReconcileWarn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	handle := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8},
		ModifyErr: errors.New("modify failed"),
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return handle, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{CPUs: 4},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM post-lock connect reconcile warn: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootConnected {
		t.Errorf("expected VMBootConnected, got %v", boot)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not reconcile VM resources") {
		t.Errorf("expected a reconcile warning, got %v", ui.WarnCalls)
	}
}

// TestEnsureProjectVMPostLockStartReconcileWarn covers the reconcile-failure
// warning on the post-lock start path.
func TestEnsureProjectVMPostLockStartReconcileWarn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	handle := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusStopped,
		StartSb:   &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8},
		ModifyErr: errors.New("modify failed"),
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return handle, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{CPUs: 4},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM post-lock start reconcile warn: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootStarted {
		t.Errorf("expected VMBootStarted, got %v", boot)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not reconcile VM resources") {
		t.Errorf("expected a reconcile warning, got %v", ui.WarnCalls)
	}
}

// TestEnsureProjectVMConnectRetryRefreshError covers the branch where the
// connect-failure retry cannot refresh the handle.
func TestEnsureProjectVMConnectRetryRefreshError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refreshErr: errors.New("refresh failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error when refreshing the handle after a connect failure")
	}
}

// TestEnsureProjectVMConnectRetryReconcileWarn covers the reconcile-failure
// warning after a successful reconnect.
func TestEnsureProjectVMConnectRetryReconcileWarn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	refreshed := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8},
		ModifyErr: errors.New("modify failed"),
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: refreshed,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{CPUs: 4},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM connect-retry reconcile warn: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootConnected {
		t.Errorf("expected VMBootConnected, got %v", boot)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not reconcile VM resources") {
		t.Errorf("expected a reconcile warning, got %v", ui.WarnCalls)
	}
}

// TestEnsureProjectVMConnectRetryStartError covers the branch where, after a
// connect failure and refresh to a non-active VM, starting it fails.
func TestEnsureProjectVMConnectRetryStartError(t *testing.T) {
	causeErr := errors.New("start failed")
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	refreshed := &msb.MockSandboxHandle{
		Name_:    "agents-sandbox-vm-test",
		Status_:  msbSdk.SandboxStatusStopped,
		StartErr: causeErr,
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: refreshed,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if !errors.Is(err, causeErr) {
		t.Fatalf(
			"%s: error = %v, want it to wrap %v",
			"expected error when starting the VM after a failed connect",
			err,
			causeErr,
		)
	}
}

// TestEnsureProjectVMConnectRetryStartReconcileWarn covers the reconcile-failure
// warning when the VM is started after a failed connect.
func TestEnsureProjectVMConnectRetryStartReconcileWarn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	refreshed := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusStopped,
		StartSb:   &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8},
		ModifyErr: errors.New("modify failed"),
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: refreshed,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{CPUs: 4},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM connect-retry start reconcile warn: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootStarted {
		t.Errorf("expected VMBootStarted, got %v", boot)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not reconcile VM resources") {
		t.Errorf("expected a reconcile warning, got %v", ui.WarnCalls)
	}
}

// TestEnsureProjectVMRecreateDestroysTheHandleItIsGiven pins the recreate
// target: the VM the decision was read from is destroyed, even when its name
// no longer resolves by the time ensure-vm runs.
func TestEnsureProjectVMRecreateDestroysTheHandleItIsGiven(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	existing := &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning}

	_, boot, err := EnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:       &msb.MockMsbClient{},
		DockerClient: docker.Get(),
		Options:      options.RunOptions{Agent: "opencode", Recreate: true},
		Image:        image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume:   "vol",
		RepoPath:     "/workspace",
		Key:          testVMKey(),
		Existing:     existing,
		RepoSlug:     testVMKey().Slug,
		SkipVM:       false,
		Env:          envsecret.EnvSecrets{},
		UI:           &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if !existing.Destroyed {
		t.Error("recreate did not destroy the handle the decision was read from")
	}
	if boot != VMBootCreated {
		t.Errorf("boot = %v, want VMBootCreated", boot)
	}
}

// TestEnsureProjectVMFlockDirError covers the project-flock failure in
// EnsureProjectVM (the flock directory cannot be created).
func TestEnsureProjectVMFlockDirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)

	// Point UserStateDir at a regular file so the per-project flock directory
	// cannot be created under it (ENOTDIR).
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "userstate")
	if err := os.WriteFile(stateFile, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths {
		return failingStateDirConfigPaths{stateDir: stateFile}
	}
	t.Cleanup(func() { configpaths.Get = orig })

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error when the flock directory cannot be created")
	}
	if !strings.Contains(err.Error(), "create lock dir") {
		t.Errorf("expected a lock-dir error, got %v", err)
	}
}

// TestEnsureProjectVMDecideActionError covers the decideVMAction error branch
// (an unrecognized sandbox status).
func TestEnsureProjectVMDecideActionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatus("unknown-status"),
	})
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error for an unrecognized sandbox status")
	}
}

func TestEnsureProjectVMWaitsForStartingVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	running := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
	}
	wasRefreshed := false
	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStarting,
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			wasRefreshed = true
			return running, nil
		},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if !wasRefreshed {
		t.Fatal("expected a starting VM to be refreshed while waiting")
	}
	if sb == nil {
		t.Fatal("expected a connected sandbox")
	}
	if boot != VMBootConnected {
		t.Errorf("boot = %v, want VMBootConnected", boot)
	}
}

func TestEnsureProjectVMStartingReconcileWarning(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	running := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 1, MaxCPUs: 2},
		ModifyErr: errors.New("modify failed"),
	}
	handle := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusStarting,
		Cfg:       &msbSdk.SandboxConfig{CPUs: 1, MaxCPUs: 2},
		ModifyErr: errors.New("modify failed"),
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return running, nil
		},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{CPUs: 2},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if len(ui.WarnCalls) == 0 {
		t.Fatal("expected resource reconcile warning")
	}
}

func TestEnsureProjectVMStartingStatusError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStarting,
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusPaused}, nil
		},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected starting VM status error")
	}
}

func TestEnsureProjectVMStartsAfterConcurrentBootFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	started := &msb.MockSandbox{Name_: "vm"}
	refreshed := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
		StartSb: started,
	}
	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStarting,
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return refreshed, nil
		},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if sb != started {
		t.Fatalf("sandbox = %v, want the restarted sandbox", sb)
	}
	if boot != VMBootStarted {
		t.Errorf("boot = %v, want VMBootStarted", boot)
	}
}

func TestEnsureProjectVMConvergesCreatedStartRace(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	running := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
	}
	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusCreated,
		StartFn: func(context.Context) (msb.Sandbox, error) {
			return nil, &msbSdk.Error{
				Kind:    msbSdk.ErrSandboxStillRunning,
				Message: "another caller started the sandbox",
			}
		},
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return running, nil
		},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a connected sandbox")
	}
	if boot != VMBootConnected {
		t.Errorf("boot = %v, want VMBootConnected for the losing starter", boot)
	}
}

func TestEnsureProjectVMConnectRetryRejectsRefreshedPausedVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		ConnectErr: errors.New("connect failed"),
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusPaused}, nil
		},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected paused refresh status error")
	}
}

func TestEnsureProjectVMConnectRetryWaitsForStartingVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	running := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
	}
	refreshed := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStarting,
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return running, nil
		},
	}
	handle := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		ConnectErr: errors.New("connect failed"),
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return refreshed, nil
		},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil || sb == nil || boot != VMBootConnected {
		t.Fatalf("connect retry convergence = %v, %v, %v", sb, boot, err)
	}
}

func TestEnsureProjectVMPostLockWaitsForStartingVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	running := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
	}
	starting := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStarting,
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return running, nil
		},
	}
	callCount := 0
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return starting, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if sb == nil || boot != VMBootConnected {
		t.Fatalf("post-lock convergence = %v, %v", sb, boot)
	}
}

func TestEnsureProjectVMPostLockWaitError(t *testing.T) {
	causeErr := errors.New("refresh failed")
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	starting := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStarting,
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return nil, causeErr
		},
	}
	callCount := 0
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return starting, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if !errors.Is(err, causeErr) {
		t.Fatalf("%s: error = %v, want it to wrap %v", "expected post-lock wait error", err, causeErr)
	}
}

func TestEnsureProjectVMPostLockWaitReconcileWarning(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	running := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
	}
	starting := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusStarting,
		Cfg:       &msbSdk.SandboxConfig{CPUs: 1, MaxCPUs: 2},
		ModifyErr: errors.New("modify failed"),
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return running, nil
		},
	}
	callCount := 0
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return starting, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{CPUs: 2},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if boot != VMBootConnected {
		t.Fatalf("boot = %v, want VMBootConnected", boot)
	}
	if len(ui.WarnCalls) == 0 {
		t.Fatal("expected resource reconcile warning")
	}
}

// TestEnsureProjectVMStartError covers the Start failure branch when an
// existing stopped VM fails to start.
func TestEnsureProjectVMStartError(t *testing.T) {
	causeErr := errors.New("start failed")
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:    "agents-sandbox-vm-test",
		Status_:  msbSdk.SandboxStatusStopped,
		StartErr: causeErr,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if !errors.Is(err, causeErr) {
		t.Fatalf(
			"%s: error = %v, want it to wrap %v",
			"expected error when starting the stopped VM fails",
			err,
			causeErr,
		)
	}
}

// TestEnsureProjectVMEnsureInstalledError covers the microsandbox runtime
// install failure branch in EnsureProjectVM (create path).
func TestEnsureProjectVMEnsureInstalledError(t *testing.T) {
	causeErr := errors.New("runtime install failed")
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.EnsureInstalledFn = func(context.Context) error { return causeErr }
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	}
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if !errors.Is(err, causeErr) {
		t.Fatalf(
			"%s: error = %v, want it to wrap %v",
			"expected error when the microsandbox runtime install fails",
			err,
			causeErr,
		)
	}
}

// TestEnsureProjectVMConnectRetryReconnectError covers the connect-failure
// retry path: connect fails, Refresh succeeds, the VM is still active, and the
// re-connect also fails (the mock returns the same ConnectErr for both).
func TestEnsureProjectVMConnectRetryReconnectError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		ConnectErr: errors.New("connect failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error when reconnect after a connect failure also fails")
	}
	if len(ui.VerboseCalls) == 0 {
		t.Error("expected a verbose message about the connect failure")
	}
}

// TestEnsureProjectVMConnectRetryRefreshError covers the Refresh failure
// branch in the connect-retry path: connect fails, then refreshing the handle
// to re-evaluate its status also fails. The mock returns the handle unchanged
// on Refresh, so this is exercised by having the initial connect fail and then
// the re-connect fail on a non-active re-evaluation.
func TestEnsureProjectVMConnectRetryStartAfterRefresh(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		ConnectErr: errors.New("connect failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	// The mock's Refresh returns the same handle; with a running status the
	// retry path attempts a second Connect which also fails. We assert the
	// resulting error message mentions the connect failure.
	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error from the connect retry path")
	}
}

// TestEnsureProjectVMPostLockRecheckConnect covers the post-lock recheck path
// where another invocation created the VM while we waited for the flock and it
// is already running: we connect to it.
func TestEnsureProjectVMPostLockRecheckConnect(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	handle := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return handle, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM post-lock recheck: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootConnected {
		t.Errorf("expected VMBootConnected, got %v", boot)
	}
}

// TestEnsureProjectVMPostLockRecheckStart covers the post-lock recheck path
// where another invocation created the VM while we waited for the flock and it
// is stopped: we start it.
func TestEnsureProjectVMPostLockRecheckStart(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
		StartSb: &msb.MockSandbox{Name_: "vm"},
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return handle, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM post-lock recheck: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootStarted {
		t.Errorf("expected VMBootStarted, got %v", boot)
	}
}

// TestEnsureProjectVMRecreateStopError covers the recreate path where the old
// VM's Stop fails: the failure is logged as verbose and recreation continues.
func TestEnsureProjectVMRecreateStopError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	oldHandle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
		StopErr: errors.New("stop failed"),
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount == 1 {
			return oldHandle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}
	client.CreateSandboxFn = func(_ context.Context, name string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return &msb.MockSandbox{Name_: name}, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Recreate: true, CPUs: 1, Memory: "2G"},
		Image:      image.ImageInfo{DockerTag: "new:tag", Ref: "new:tag"},
		HomeVolume: "homevol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM recreate with stop error: %v", err)
	}
	if !boot.Booted() {
		t.Error("expected a boot after recreate")
	}
	if sb == nil {
		t.Error("expected a sandbox after recreate")
	}
}

// TestEnsureProjectVMStartSucceedsWithReconcile covers the start path with a
// successful reconcile (the resource reconciliation warning is not emitted).
func TestEnsureProjectVMStartSucceedsWithReconcile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
		StartSb: &msb.MockSandbox{Name_: "vm"},
		Cfg:     &msbSdk.SandboxConfig{CPUs: 2, MemoryMiB: 2048},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{CPUs: 2, Memory: "2048"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM start: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootStarted {
		t.Errorf("expected VMBootStarted, got %v", boot)
	}
}

// TestEnsureProjectVMConnectReconcileWarn covers the reconcile-failure warning
// branch on the connect path: reconcileResourceConfig fails but the connection
// is still used.
func TestEnsureProjectVMConnectReconcileWarn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MemoryMiB: 2048},
		ModifyErr: errors.New("modify failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{CPUs: 4},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM connect with reconcile warning: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != VMBootConnected {
		t.Errorf("expected VMBootConnected, got %v", boot)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not reconcile VM resources") {
		t.Errorf("expected a reconcile warning, got %v", ui.WarnCalls)
	}
}

// TestEnsureProjectVMStartReconcileWarn covers the reconcile-failure warning
// branch on the start path.
func TestEnsureProjectVMStartReconcileWarn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusStopped,
		StartSb:   &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MemoryMiB: 2048},
		ModifyErr: errors.New("modify failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{CPUs: 4},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM start with reconcile warning: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if !contains(joinStrings(ui.WarnCalls), "could not reconcile VM resources") {
		t.Errorf("expected a reconcile warning, got %v", ui.WarnCalls)
	}
}

// TestEnsureProjectVMRecheckNonNotFoundError covers the post-lock recheck
// branch where the second GetSandbox returns a non-not-found error.
func TestEnsureProjectVMRecheckNonNotFoundError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return nil, errors.New("transient error")
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error from the post-lock recheck")
	}
}

// TestEnsureProjectVMCreateError covers the create-failure branch in
// EnsureProjectVM (createProjectVM fails after the post-lock recheck).
func TestEnsureProjectVMCreateError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.CreateSandboxErr = errors.New("create failed")
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Memory: "1G"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error from createProjectVM")
	}
}

// TestEnsureProjectVMRecreateDestroyError covers the destroy-failure branch in
// the recreate path: destroying the old VM fails and aborts the recreate.
func TestEnsureProjectVMRecreateDestroyError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	oldHandle := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		DestroyErr: errors.New("destroy failed"),
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount == 1 {
			return oldHandle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Recreate: true, CPUs: 1, Memory: "2G"},
		Image:      image.ImageInfo{DockerTag: "new:tag", Ref: "new:tag"},
		HomeVolume: "homevol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error when destroying the old VM fails")
	}
}

// TestEnsureProjectVMRecreateGoneIdentity covers the recreate path where the old
// handle's name was already removed: recreation proceeds.
func TestEnsureProjectVMRecreateGoneIdentity(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	oldHandle := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		DestroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"},
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount == 1 {
			return oldHandle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}
	var createdName string
	client.CreateSandboxFn = func(
		_ context.Context,
		name string,
		_ ...msbSdk.SandboxOption,
	) (msb.Sandbox, error) {
		createdName = name
		return &msb.MockSandbox{Name_: name}, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Recreate: true, CPUs: 1, Memory: "2G"},
		Image:      image.ImageInfo{DockerTag: "new:tag", Ref: "new:tag"},
		HomeVolume: "homevol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM recreate with gone identity: %v", err)
	}
	if !boot.Booted() {
		t.Error("expected a boot after recreate")
	}
	if sb == nil {
		t.Error("expected a sandbox after recreate")
	}
	// The replaced VM carried a legacy name; the recreate must use the
	// prefix-free one so the rename takes effect.
	if want := ProjectVMName(testVMKey()); createdName != want {
		t.Errorf("recreated sandbox name = %q, want %q", createdName, want)
	}
	// The already-gone teardown must be logged distinctly from a replace.
	if !verboseContains(ui.VerboseCalls, "was already gone; recreating from new config") {
		t.Errorf("verbose output = %v, want the already-gone note", ui.VerboseCalls)
	}
}

// TestEnsureProjectVMRecreateReplacedIdentityFailsClosed covers the recreate path
// where the held identity was replaced by another sandbox under the same name:
// the reconfiguration must fail closed instead of attaching to the stranger,
// which would silently skip the requested image and mount changes.
func TestEnsureProjectVMRecreateReplacedIdentityFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	oldHandle := &msb.MockSandboxHandle{
		Name_:      "agents-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		DestroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"},
	}
	replacement := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	}
	created := false
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount == 1 {
			return oldHandle, nil
		}
		return replacement, nil
	}
	client.CreateSandboxFn = func(
		_ context.Context,
		name string,
		_ ...msbSdk.SandboxOption,
	) (msb.Sandbox, error) {
		created = true
		return &msb.MockSandbox{Name_: name}, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Recreate: true, CPUs: 1, Memory: "2G"},
		Image:      image.ImageInfo{DockerTag: "new:tag", Ref: "new:tag"},
		HomeVolume: "homevol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected EnsureProjectVM to fail closed when the held identity was replaced")
	}
	if !msb.IsReplaced(err) {
		t.Errorf("expected the returned error to wrap ErrSandboxReplaced, got %v", err)
	}
	if created {
		t.Error("recreate must not create a VM when the held identity was replaced")
	}
	if callCount != 1 {
		t.Errorf("GetSandbox calls = %d, want 1 (must not re-look-up the replacement)", callCount)
	}
}

// TestEnsureProjectVMRecreateHoldsCreationLock is the #397 regression: the
// recreate destroy must run while the per-project creation lock is held, so a
// concurrent invocation cannot clobber a VM another client just created.
func TestEnsureProjectVMRecreateHoldsCreationLock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)

	locked := false
	origLock := acquireProjectVMLock
	acquireProjectVMLock = func(state.Key) (func(), error) {
		locked = true
		return func() { locked = false }, nil
	}
	t.Cleanup(func() { acquireProjectVMLock = origLock })

	destroyed := false
	handle := &msb.MockSandboxHandle{
		Name_:   "vm-test-opencode",
		Status_: msbSdk.SandboxStatusRunning,
		DestroyFn: func(context.Context) error {
			if !locked {
				t.Error("recreate destroyed the VM outside the creation lock")
			}
			destroyed = true
			return nil
		},
	}
	created := false
	client := &msb.MockMsbClient{}
	// The pre-lock lookup finds the VM; the destroy frees the name, so the
	// post-lock re-check must miss and take the create path.
	client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
		if destroyed {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
		}
		return handle, nil
	}
	client.CreateSandboxFn = func(_ context.Context, name string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		if !locked {
			t.Error("recreate created the replacement VM outside the creation lock")
		}
		created = true
		return &msb.MockSandbox{Name_: name}, nil
	}
	client.ImageGetFn = func(context.Context, string) error { return nil }
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	if _, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Recreate: true, CPUs: 1, Memory: "2G"},
		Image:      image.ImageInfo{DockerTag: "new:tag", Ref: "new:tag"},
		HomeVolume: "homevol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	}); err != nil {
		t.Fatalf("EnsureProjectVM recreate: %v", err)
	}
	if !created {
		t.Fatal("expected the recreate to create a replacement VM under the lock")
	}
	// A successful teardown of a still-present VM logs the replace note.
	if !verboseContains(ui.VerboseCalls, "replaced project VM vm-test-opencode; recreating from new config") {
		t.Errorf("verbose output = %v, want the replaced-VM note", ui.VerboseCalls)
	}
}

// TestEnsureProjectVMCreateEnsureLoadedError covers the image-load failure
// branch inside createProjectVM when reached through EnsureProjectVM.
func TestEnsureProjectVMCreateEnsureLoadedError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return nil, errors.New("image export failed")
		},
	})

	client := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Memory: "1G"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if err == nil {
		t.Fatal("expected error when loading the image into microsandbox fails")
	}
}

// TestEnsureProjectVMPostLockRecheckError covers the lookup that runs again
// once the creation lock is held: a failure other than "not found" aborts the
// create and keeps its cause.
func TestEnsureProjectVMPostLockRecheckError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	causeErr := errors.New("lookup failed")
	callCount := 0
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
		callCount++
		if callCount <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return nil, causeErr
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume: "vol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         &ui,
	})
	if !errors.Is(err, causeErr) || !strings.Contains(err.Error(), "re-check sandbox") {
		t.Fatalf("error = %v, want the re-check failure wrapping %v", err, causeErr)
	}
}

// TestProjectVMCreationProjectVMLockSeam pins the injected creation-lock seam:
// EnsureProjectVM takes the lock from creation.Seams.ProjectVMLock on its create
// path.
func TestProjectVMCreationProjectVMLockSeam(t *testing.T) {
	lockErr := errors.New("lock boom")
	ui := termio.NewTestMock(t)

	_, _, err := EnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:       &msb.MockMsbClient{},
		DockerClient: docker.Get(),
		Options:      options.RunOptions{},
		Image:        image.ImageInfo{},
		HomeVolume:   "home-vol",
		RepoPath:     "/repo",
		Key:          testVMKey(),
		RepoSlug:     "repo",
		SkipVM:       false,
		Env:          envsecret.EnvSecrets{},
		UI:           &ui,
		Seams: options.Seams{ProjectVMLock: func(state.Key) (func(), error) {
			return nil, lockErr
		}},
	})
	if !errors.Is(err, lockErr) {
		t.Fatalf("EnsureProjectVM error = %v, want the injected lock's %v", err, lockErr)
	}
}

func TestEnsureProjectVM_CreatePath(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI

	notFoundErr := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, notFoundErr
	}
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	// CheckoutSlug depends on the current directory, so use a temp repo.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:  client,
		Options: options.RunOptions{Memory: "1G", TmpSize: "512M"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   tmpRepo,
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM (create): %v", err)
	}
	if !created.Booted() {
		t.Error("expected created=true on create path")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 created sandbox, got %d: %v", len(client.CreatedSandboxes), client.CreatedSandboxes)
	}
}

func TestEnsureProjectVMAppliesLabels(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI

	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	}
	client.CreateSandboxFn = func(_ context.Context, _ string, opts ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		cfg := msbSdk.SandboxConfig{}
		for _, opt := range opts {
			opt(&cfg)
		}
		if cfg.Labels[naming.LabelProject] == "" {
			t.Errorf("expected project label set, got %v", cfg.Labels)
		}
		if cfg.Labels[naming.LabelImage] == "" {
			t.Errorf("expected image label set, got %v", cfg.Labels)
		}
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:  client,
		Options: options.RunOptions{Memory: "1G", TmpSize: "512M"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   tmpRepo,
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM (create): %v", err)
	}
	if !created.Booted() || sb == nil {
		t.Fatal("expected created sandbox")
	}
}

func TestEnsureProjectVM_ReconnectPath(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI

	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return nil, errors.New("reconnect path must not create a sandbox")
	}
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:  client,
		Options: options.RunOptions{Memory: "1G", TmpSize: "512M"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   tmpRepo,
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM (reconnect): %v", err)
	}
	if created.Booted() {
		t.Error("expected created=false on reconnect path")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 0 {
		t.Fatalf("expected no sandbox created on reconnect, got %v", client.CreatedSandboxes)
	}
}

func TestEnsureProjectVM_ConnectOutcomeIsInfo(t *testing.T) {
	ui := &termio.Mock{}

	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if _, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:  client,
		Options: options.RunOptions{Memory: "1G", TmpSize: "512M"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   tmpRepo,
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	}); err != nil {
		t.Fatalf("EnsureProjectVM (connect): %v", err)
	}
	assertInfoCall(t, ui, "connected to existing project VM")
}

func TestEnsureProjectVM_StartOutcomeIsInfo(t *testing.T) {
	ui := &termio.Mock{}

	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
	})
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if _, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:  client,
		Options: options.RunOptions{Memory: "1G", TmpSize: "512M"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   tmpRepo,
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	}); err != nil {
		t.Fatalf("EnsureProjectVM (start): %v", err)
	}
	assertInfoCall(t, ui, "started existing project VM")
}

func TestEnsureProjectVM_CreateOutcomeIsInfo(t *testing.T) {
	ui := &termio.Mock{}

	notFoundErr := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, notFoundErr
	}
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if _, _, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:  client,
		Options: options.RunOptions{Memory: "1G", TmpSize: "512M"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   tmpRepo,
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	}); err != nil {
		t.Fatalf("EnsureProjectVM (create): %v", err)
	}
	assertInfoCall(t, ui, "created new project VM")
}

func TestEnsureProjectVM_ReconnectWhenImageUnchanged(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI

	oldHandle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
		Image_:  "agents-sandbox/runner-test:abc123",
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(oldHandle)
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return nil, errors.New("unchanged image must not create a sandbox")
	}
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:  client,
		Options: options.RunOptions{Memory: "1G", TmpSize: "512M"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:abc123",
			Ref:       "agents-sandbox/runner-test:abc123",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   tmpRepo,
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM (unchanged image): %v", err)
	}
	if created.Booted() {
		t.Error("expected created=false when image is unchanged")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 0 {
		t.Fatalf("expected no sandbox created, got %v", client.CreatedSandboxes)
	}
	if oldHandle.DidDestroy() {
		t.Error("expected existing VM not to be destroyed when image is unchanged")
	}
}

func TestEnsureProjectVM_NoReplacementWhenExistingImageUnknown(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI

	oldHandle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
		Image_:  "",
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(oldHandle)
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return nil, errors.New("unknown image must not create a sandbox")
	}
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:  client,
		Options: options.RunOptions{Memory: "1G", TmpSize: "512M"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:newDigest",
			Ref:       "agents-sandbox/runner-test:newDigest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   tmpRepo,
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM (unknown existing image): %v", err)
	}
	if created.Booted() {
		t.Error("expected created=false when existing image is unknown")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 0 {
		t.Fatalf("expected no sandbox created, got %v", client.CreatedSandboxes)
	}
}

func TestEnsureProjectVMRecreatesWhenFlagged(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI

	oldHandle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
		Image_:  "old:tag",
		Cfg:     &msbSdk.SandboxConfig{Image: "old:tag"},
	}
	callCount := 0
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		if callCount == 0 {
			callCount++
			return oldHandle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}
	client.CreateSandboxFn = func(_ context.Context, name string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return &msb.MockSandbox{Name_: name}, nil
	}
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	opts := options.RunOptions{ReapPolicy: options.ReapPolicy{}, Recreate: true, CPUs: 1, Memory: "2G"}
	sb, created, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    opts,
		Image:      image.ImageInfo{DockerTag: "new:tag", Ref: "new:tag"},
		HomeVolume: "homevol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if !created.Booted() {
		t.Error("expected created=true after recreate")
	}
	if sb == nil {
		t.Error("expected a sandbox after recreate")
	}
	if !oldHandle.DidDestroy() {
		t.Error("expected old VM handle.Destroy to be called")
	}
	if oldHandle.DidStop {
		t.Error("expected Destroy to replace the separate Stop call")
	}
}

func TestEnsureProjectVMReusesWhenNotFlagged(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI

	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
		Image_:  "old:tag",
		Cfg:     &msbSdk.SandboxConfig{Image: "old:tag"},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return nil, errors.New("CreateSandbox must not be called when reusing")
	}
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)

	sb, created, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
		Options:    options.RunOptions{Agent: "opencode"},
		Image:      image.ImageInfo{DockerTag: "old:tag", Ref: "old:tag"},
		HomeVolume: "homevol",
		RepoPath:   "/workspace",
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		SkipVM:     false,
		UI:         ui,
	})
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if created.Booted() {
		t.Error("expected created=false on reuse")
	}
	if sb == nil {
		t.Error("expected a sandbox on reuse")
	}
	if handle.DidDestroy() {
		t.Error("did not expect Destroy on reuse")
	}
}

// TestResumeSandboxByStatusUnavailable pins the two refusals: a known status
// the VM cannot be resumed from is "not available", any other is "unexpected".
func TestResumeSandboxByStatusUnavailable(t *testing.T) {
	for _, tc := range []struct {
		status  msbSdk.SandboxStatus
		message string
	}{
		{msbSdk.SandboxStatusDraining, "is not available"},
		{msbSdk.SandboxStatusPaused, "is not available"},
		{msbSdk.SandboxStatus("bogus"), "unexpected status"},
	} {
		_, _, err := resumeSandboxByStatus(context.Background(), nil, tc.status, "proj")
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("resumeSandboxByStatus(%v) error = %v, want one containing %q", tc.status, err, tc.message)
		}
	}
}

func TestStartExistingVMErrors(t *testing.T) {
	startErr := errors.New("start failed")
	handle := &msb.MockSandboxHandle{StartErr: startErr}
	if _, _, err := startExistingVM(context.Background(), handle); !errors.Is(err, startErr) {
		t.Fatalf("startExistingVM error = %v, want %v", err, startErr)
	}

	refreshErr := errors.New("refresh failed")
	handle = &msb.MockSandboxHandle{
		StartErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxStillRunning, Message: "already starting"},
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return nil, refreshErr
		},
	}
	if _, _, err := startExistingVM(context.Background(), handle); !errors.Is(err, refreshErr) {
		t.Fatalf("startExistingVM refresh error = %v, want %v", err, refreshErr)
	}

	startRaceErr := &msbSdk.Error{Kind: msbSdk.ErrSandboxStillRunning, Message: "already starting"}
	handle = &msb.MockSandboxHandle{
		StartErr: startRaceErr,
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusStopped}, nil
		},
	}
	if _, _, err := startExistingVM(context.Background(), handle); !errors.Is(err, startRaceErr) {
		t.Fatalf("startExistingVM non-active race error = %v, want %v", err, startRaceErr)
	}
}

func TestConvergeExistingVMRefreshError(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			return nil, errors.New("refresh failed")
		},
	}
	if _, _, err := convergeExistingVM(context.Background(), handle); err == nil {
		t.Fatal("expected refresh error")
	}
}

func TestConvergeExistingVMStartingContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handle := &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusStarting}
	if _, _, err := convergeExistingVM(ctx, handle); !errors.Is(err, context.Canceled) {
		t.Fatalf("convergeExistingVM error = %v, want context canceled", err)
	}
}

func TestConvergeExistingVMConnectError(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Status_:    msbSdk.SandboxStatusRunning,
		ConnectErr: errors.New("connect failed"),
	}
	if _, _, err := convergeExistingVM(context.Background(), handle); err == nil {
		t.Fatal("expected connect error")
	}
}

func TestConvergeExistingVMWaitsThroughStarting(t *testing.T) {
	orig := sandboxLifecyclePollInterval
	t.Cleanup(func() { sandboxLifecyclePollInterval = orig })
	sandboxLifecyclePollInterval = time.Millisecond

	refreshes := 0
	handle := &msb.MockSandboxHandle{
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			refreshes++
			if refreshes == 1 {
				return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusStarting}, nil
			}
			return &msb.MockSandboxHandle{
				Status_:   msbSdk.SandboxStatusRunning,
				ConnectSb: &msb.MockSandbox{Name_: "vm"},
			}, nil
		},
	}
	sb, boot, err := convergeExistingVM(context.Background(), handle)
	if err != nil || sb == nil || boot != VMBootConnected {
		t.Fatalf("convergeExistingVM = %v, %v, %v", sb, boot, err)
	}
	if refreshes != 2 {
		t.Fatalf("refreshes = %d, want 2", refreshes)
	}
}

func TestConvergeExistingVMRejectsUnavailableStatuses(t *testing.T) {
	for _, status := range []msbSdk.SandboxStatus{
		msbSdk.SandboxStatusDraining,
		msbSdk.SandboxStatusPaused,
		msbSdk.SandboxStatus("future-status"),
	} {
		t.Run(string(status), func(t *testing.T) {
			handle := &msb.MockSandboxHandle{Status_: status}
			if _, _, err := convergeExistingVM(context.Background(), handle); err == nil {
				t.Fatalf("expected status %q to be rejected", status)
			}
		})
	}
}

func TestConvergeExistingVMStartsInactiveVM(t *testing.T) {
	started := &msb.MockSandbox{Name_: "vm"}
	handle := &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusStopped, StartSb: started}
	sb, boot, err := convergeExistingVM(context.Background(), handle)
	if err != nil || sb != started || boot != VMBootStarted {
		t.Fatalf("convergeExistingVM = %v, %v, %v", sb, boot, err)
	}
}

func TestWaitForSandboxLifecycleCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForSandboxLifecycle(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForSandboxLifecycle error = %v, want context canceled", err)
	}
}

// TestCreateProjectVMNetworkError covers the branch where the network policy
// cannot be converted to a config.
func TestCreateProjectVMNetworkError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{}
	ui := termio.NewTestMock(t)

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options: options.RunOptions{
			Memory:  "1G",
			Network: network.Policy{Profiles: []network.Profile{network.Profile("bogus-profile")}},
		},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         &ui,
	}, "agents-sandbox-vm-test")
	if err == nil {
		t.Fatal("expected error when the network policy cannot be converted")
	}
}

// TestCreateProjectVMCreateError covers the branch where CreateSandbox fails.
func TestCreateProjectVMCreateError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{CreateSandboxErr: errors.New("create failed")}
	ui := termio.NewTestMock(t)

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options:      options.RunOptions{Memory: "1G"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         &ui,
	}, "agents-sandbox-vm-test")
	if err == nil {
		t.Fatal("expected error when CreateSandbox fails")
	}
}

// TestCreateProjectVMServeOnlyAndNetwork covers the serve-only and network
// option branches of createProjectVM: port bindings and a network config are
// attached when configured.
func TestCreateProjectVMServeOnlyAndNetwork(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{}
	ui := termio.NewTestMock(t)

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options: options.RunOptions{
			Memory:        "1G",
			ServeOnly:     true,
			ServeHostPort: ports.ServeOnlyBasePort,
			Network:       network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
		},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         &ui,
	}, "agents-sandbox-vm-test")
	if err != nil {
		t.Fatalf("createProjectVM with serve-only + network: %v", err)
	}
	if len(client.CreatedSandboxCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(client.CreatedSandboxCalls))
	}
	cfg := msbSdk.SandboxConfig{}
	for _, o := range client.CreatedSandboxCalls[0].Opts {
		o(&cfg)
	}
	if len(cfg.PortBindings) == 0 {
		t.Error("expected port bindings for serve-only mode")
	}
	if cfg.Network == nil {
		t.Error("expected a network config to be attached")
	}
}

// TestCreateProjectVMServeOnlyPublishesExactPort covers the real production
// path of createProjectVM where PrepareSandbox has already resolved a non-zero
// opts.ServeHostPort: the created VM must publish exactly that host port.
func TestCreateProjectVMServeOnlyPublishesExactPort(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{}
	ui := termio.NewTestMock(t)

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options: options.RunOptions{
			Memory:        "1G",
			ServeOnly:     true,
			ServeHostPort: 4097,
		},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         &ui,
	}, "agents-sandbox-vm-test")
	if err != nil {
		t.Fatalf("createProjectVM with serve-only + fixed host port: %v", err)
	}
	if len(client.CreatedSandboxCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(client.CreatedSandboxCalls))
	}
	cfg := msbSdk.SandboxConfig{}
	for _, o := range client.CreatedSandboxCalls[0].Opts {
		o(&cfg)
	}
	if len(cfg.PortBindings) != 1 {
		t.Fatalf("expected 1 port binding, got %d", len(cfg.PortBindings))
	}
	pb := cfg.PortBindings[0]
	if pb.HostPort != 4097 || pb.GuestPort != 4096 {
		t.Errorf("binding = host %d guest %d, want host 4097 guest 4096", pb.HostPort, pb.GuestPort)
	}
}

// TestCreateProjectVMUsesResolvedRef pins that the VM is created with the ref
// EnsureLoaded resolved (a reused legacy ref), not the freshly computed one, and
// that the image label records the same ref.
func TestCreateProjectVMUsesResolvedRef(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	key := testVMKey()
	legacyRef := naming.DockerImageRef(key.Slug, key.Agent)
	client := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}
	testUI := termio.NewTestMock(t)
	ui := &testUI

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options:      options.RunOptions{Memory: "1G"},
		Image: image.ImageInfo{
			DockerTag: legacyRef,
			Ref:       naming.RunnerImageRef(key.Slug, key.Agent),
			Digest:    "sha256:same",
			Env:       map[string]string{"FOO": "bar"},
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        key,
		RepoSlug:   key.Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         ui,
	}, ProjectVMName(key))
	if err != nil {
		t.Fatalf("createProjectVM: %v", err)
	}

	var cfg msbSdk.SandboxConfig
	for _, call := range client.CreatedSandboxCalls {
		if call.Name != ProjectVMName(key) {
			continue
		}
		for _, opt := range call.Opts {
			opt(&cfg)
		}
	}
	if cfg.Image != legacyRef {
		t.Errorf("created VM image = %q, want the reused legacy ref %q", cfg.Image, legacyRef)
	}
	if got := cfg.Labels[naming.LabelImage]; got != legacyRef {
		t.Errorf("LabelImage = %q, want %q", got, legacyRef)
	}
	if !verboseContains(ui.VerboseCalls, "adding docker env definitions to project VM environment: map[FOO:bar]") {
		t.Errorf("verbose output = %v, want the docker env note", ui.VerboseCalls)
	}
}

// TestCreateProjectVMIdleTimeout pins the single default: an unset
// RunOptions.IdleTimeout takes options.DefaultVMIdleTimeout, while a configured
// value is passed through unchanged.
func TestCreateProjectVMIdleTimeout(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	tests := []struct {
		name string
		opts time.Duration
		want time.Duration
	}{
		{name: "unset uses the options default", opts: 0, want: options.DefaultVMIdleTimeout},
		{name: "configured value is used as-is", opts: 45 * time.Second, want: 45 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := testVMKey()
			client := &msb.MockMsbClient{}
			testUI := termio.NewTestMock(t)
			ui := &testUI

			_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
				Client:       client,
				DockerClient: docker.Get(),
				Options:      options.RunOptions{Memory: "1G", IdleTimeout: tc.opts},
				Image: image.ImageInfo{
					DockerTag: "agents-sandbox/runner-test:latest",
					Ref:       "agents-sandbox/runner-test:latest",
				},
				HomeVolume: "test-home-vol",
				RepoPath:   t.TempDir(),
				Key:        key,
				RepoSlug:   key.Slug,
				Env:        envsecret.EnvSecrets{},
				UI:         ui,
			}, ProjectVMName(key))
			if err != nil {
				t.Fatalf("createProjectVM: %v", err)
			}
			cfg := createdProjectVMConfig(t, client, ProjectVMName(key))
			if cfg.IdleTimeout != tc.want {
				t.Errorf("IdleTimeout = %v, want %v", cfg.IdleTimeout, tc.want)
			}
		})
	}
}

// TestCreateProjectVMCreateSandboxError covers the CreateSandbox failure branch
// in createProjectVM.
func TestCreateProjectVMCreateSandboxError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{}
	client.CreateSandboxErr = errors.New("create failed")
	testUI := termio.NewTestMock(t)
	ui := &testUI

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options:      options.RunOptions{Memory: "1G"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         ui,
	}, "agents-sandbox-vm-test")
	if err == nil {
		t.Fatal("expected error from CreateSandbox")
	}
}

// TestCreateProjectVMNetworkConfigError covers the network-config error branch
// in createProjectVM (an invalid network profile cannot be converted).
func TestCreateProjectVMNetworkConfigError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{}
	testUI := termio.NewTestMock(t)
	ui := &testUI

	_, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options: options.RunOptions{
			Memory:  "1G",
			Network: network.Policy{Profiles: []network.Profile{network.Profile("bogus-profile")}},
		},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         ui,
	}, "agents-sandbox-vm-test")
	if err == nil {
		t.Fatal("expected error from the network config")
	}
}

func TestCreateProjectVMCallsClientCreateSandbox(t *testing.T) {
	client := &msb.MockMsbClient{}
	testUI := termio.NewTestMock(t)
	ui := &testUI
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	sb, created, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options:      options.RunOptions{Memory: "1G"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         ui,
	}, "agents-sandbox-vm-test")
	if err != nil {
		t.Fatalf("createProjectVM failed: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 created sandbox, got %d", len(client.CreatedSandboxes))
	}
	if client.CreatedSandboxes[0] != "agents-sandbox-vm-test" {
		t.Errorf("expected sandbox name %q, got %q", "agents-sandbox-vm-test", client.CreatedSandboxes[0])
	}
	cfg := msbSdk.SandboxConfig{}
	for _, opt := range client.CreatedSandboxCalls[0].Opts {
		opt(&cfg)
	}
	if cfg.Network == nil {
		t.Fatal("expected an explicit deny-by-default network config")
	}
	if cfg.Network.DefaultEgress != msbSdk.PolicyActionDeny {
		t.Errorf("default egress = %v, want deny", cfg.Network.DefaultEgress)
	}
}

func TestCreateProjectVMLoadsImageWhenNotCached(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	// The image is not yet in microsandbox (ImageGet fails), so EnsureLoaded
	// must export it from Docker and load it before creating the VM.
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})

	client := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	testUI := termio.NewTestMock(t)
	ui := &testUI

	sb, created, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options:      options.RunOptions{Memory: "1G"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:abc",
			Ref:       "agents-sandbox/runner-test:abc",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         ui,
	}, "agents-sandbox-vm-test")
	if err != nil {
		t.Fatalf("createProjectVM failed: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.LoadedImages) != 1 {
		t.Fatalf("expected the runner image to be loaded into microsandbox, got %d loads", len(client.LoadedImages))
	}
	if client.LoadedImages[0] != "agents-sandbox/runner-test:abc" {
		t.Errorf("loaded image ref = %q, want %q", client.LoadedImages[0], "agents-sandbox/runner-test:abc")
	}
}

func TestCreateProjectVMAppliesRootDiskWhenDiskSizeSet(t *testing.T) {
	client := &msb.MockMsbClient{}
	testUI := termio.NewTestMock(t)
	ui := &testUI
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	if _, _, err := createProjectVM(context.Background(), ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options:      options.RunOptions{Memory: "1G", DiskSize: "16G"},
		Image: image.ImageInfo{
			DockerTag: "agents-sandbox/runner-test:latest",
			Ref:       "agents-sandbox/runner-test:latest",
		},
		HomeVolume: "test-home-vol",
		RepoPath:   t.TempDir(),
		Key:        testVMKey(),
		RepoSlug:   testVMKey().Slug,
		Env:        envsecret.EnvSecrets{},
		UI:         ui,
	}, "agents-sandbox-vm-test"); err != nil {
		t.Fatalf("createProjectVM failed: %v", err)
	}

	if len(client.CreatedSandboxCalls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(client.CreatedSandboxCalls))
	}
	opts := client.CreatedSandboxCalls[0].Opts
	// Re-apply the captured functional options to a fresh SandboxConfig to
	// verify what createProjectVM actually set.
	var sbCfg msbSdk.SandboxConfig
	applySandboxOpts(&sbCfg, opts)
	if sbCfg.RootDisk == nil || sbCfg.RootDisk.Kind() != msbSdk.RootDiskKindManaged {
		t.Fatalf("expected managed RootDisk, got %+v", sbCfg.RootDisk)
	}
	// RootDisk.Managed(16384) — parseMemory("16G") returns 16*1024.
	if sbCfg.RootDisk.SizeMiB != 16*1024 {
		t.Errorf("expected root disk SizeMiB 16384, got %d", sbCfg.RootDisk.SizeMiB)
	}
}

func TestBuildMountsIncludesTmpfsAtTmp(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		nil,
	)

	tmpMount, ok := mounts[tmpMountPath]
	if !ok {
		t.Fatal("expected /tmp mount, not found in mounts map")
	}
	if tmpMount.Kind() != msbSdk.MountKindTmpfs {
		t.Errorf("expected /tmp to be a tmpfs mount, got kind %d", tmpMount.Kind())
	}
	if tmpMount.SizeMiB == 0 {
		t.Error("expected /tmp tmpfs to have a nonzero size cap")
	}
	if tmpMount.SizeMiB < 1024 {
		t.Errorf("expected /tmp tmpfs to be at least 1 GiB, got %d MiB", tmpMount.SizeMiB)
	}
}

func TestBuildMountsRespectsCustomTmpSize(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", 4096, options.DefaultWorkspaceQuotaMiB, nil)

	tmpMount := mounts[tmpMountPath]
	if tmpMount.SizeMiB != 4096 {
		t.Errorf("expected /tmp tmpfs size 4096 MiB, got %d", tmpMount.SizeMiB)
	}
}

func TestBuildMountsSetsWorkspaceQuota(t *testing.T) {
	mounts := buildMounts("test-home-vol", "/repo/path", options.DefaultTmpSizeMiB, 32*1024, nil)

	wsMount, ok := mounts[defaultTargetDir]
	if !ok {
		t.Fatal("expected /workspace mount, not found in mounts map")
	}
	if wsMount.Kind() != msbSdk.MountKindBind {
		t.Errorf("expected /workspace to be a bind mount, got kind %d", wsMount.Kind())
	}
	if wsMount.QuotaMiB != 32*1024 {
		t.Errorf("expected /workspace quota 32768 MiB, got %d", wsMount.QuotaMiB)
	}
}

func TestBuildMountsSetsWorkspaceMirrorHostPermissions(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		nil,
	)

	wsMount, ok := mounts[defaultTargetDir]
	if !ok {
		t.Fatal("expected /workspace mount, not found in mounts map")
	}
	if wsMount.HostPermissions != msbSdk.HostPermissionsMirror {
		t.Errorf(
			"expected /workspace host permissions %q, got %q",
			msbSdk.HostPermissionsMirror,
			wsMount.HostPermissions,
		)
	}
}

func TestBuildMountsWorkspaceQuotaDefault(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		nil,
	)

	wsMount, ok := mounts[defaultTargetDir]
	if !ok {
		t.Fatal("expected /workspace mount, not found in mounts map")
	}
	if wsMount.QuotaMiB != options.DefaultWorkspaceQuotaMiB {
		t.Errorf("expected /workspace default quota %d MiB, got %d", options.DefaultWorkspaceQuotaMiB, wsMount.QuotaMiB)
	}
}

func TestBuildMountsIncludesConfiguredBindMount(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		mounts.Mounts{
			"/home/dev/.m2": {Source: "/host/home/.m2"},
			"/home/dev/ref": {Source: "/host/ref", Readonly: true},
		},
	)

	m2, ok := mounts["/home/dev/.m2"]
	if !ok {
		t.Fatalf("expected configured .m2 bind mount, got %v", mounts)
	}
	if m2.Kind() != msbSdk.MountKindBind {
		t.Errorf("expected a bind mount, got kind %d", m2.Kind())
	}
	if m2.Bind != "/host/home/.m2" {
		t.Errorf("bind source = %q, want /host/home/.m2", m2.Bind)
	}
	if m2.Readonly {
		t.Error("expected .m2 to be writable")
	}
	if m2.HostPermissions != msbSdk.HostPermissionsDefault {
		t.Errorf(
			"expected configured mounts to keep the private host-permissions default, got %q",
			m2.HostPermissions,
		)
	}

	ref, ok := mounts["/home/dev/ref"]
	if !ok {
		t.Fatal("expected configured readonly bind mount")
	}
	if !ref.Readonly {
		t.Error("expected readonly to be propagated to the mount config")
	}
}

// TestBuildMountsKeepsManagedMounts ensures configured mounts are additive and
// never replace the managed home/workspace/tmp mounts.
func TestBuildMountsKeepsManagedMounts(t *testing.T) {
	built := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeMiB,
		options.DefaultWorkspaceQuotaMiB,
		mounts.Mounts{"/home/dev/.m2": {Source: "/host/.m2"}},
	)

	if home, ok := built[mounts.VMHomeDir]; !ok || home.Named != "test-home-vol" {
		t.Errorf("managed home mount altered: %+v", built[mounts.VMHomeDir])
	}
	if ws, ok := built[defaultTargetDir]; !ok || ws.Bind != "/repo/path" {
		t.Errorf("managed workspace mount altered: %+v", built[defaultTargetDir])
	}
	if tmp, ok := built[tmpMountPath]; !ok || tmp.Kind() != msbSdk.MountKindTmpfs {
		t.Errorf("managed tmp mount altered: %+v", built[tmpMountPath])
	}
	for _, target := range []string{mounts.VMHomeDir, tmpMountPath} {
		if got := built[target].HostPermissions; got != msbSdk.HostPermissionsDefault {
			t.Errorf("mount %q should keep the private host-permissions default, got %q", target, got)
		}
	}
}

// postLockGetSandbox returns a GetSandboxFn that misses the two lookups
// FindProjectVM makes (the current and legacy name) and answers the post-lock
// re-check with handle.
func postLockGetSandbox(handle msb.SandboxHandle) func(context.Context, string) (msb.SandboxHandle, error) {
	lookups := 0
	return func(context.Context, string) (msb.SandboxHandle, error) {
		lookups++
		if lookups <= 2 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return handle, nil
	}
}

// verboseContains reports whether any recorded verbose line contains want.
func verboseContains(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// lookUpAndEnsureProjectVM looks the project VM up through the request's msb
// client and ensures it with empty host env and secrets, as the plan-reconfig
// and ensure-vm stages do together.
// A skipVM run leaves the VM alone, so it needs no lookup and no msb mock.
func lookUpAndEnsureProjectVM(ctx context.Context, creation ProjectVMCreation) (msb.Sandbox, VMBoot, error) {
	if !creation.SkipVM {
		existing, _, err := FindProjectVM(ctx, creation.Client, creation.Key)
		if err != nil {
			return nil, VMBootConnected, err
		}
		creation.Existing = existing
	}
	creation.Env = envsecret.EnvSecrets{}
	creation.DockerClient = docker.Get()
	return EnsureProjectVM(ctx, creation)
}

// createdProjectVMConfig applies the recorded CreateSandbox options for the
// named project VM and returns the config it was created with. Other
// CreateSandbox calls (a home-volume prefill) are ignored.
func createdProjectVMConfig(t *testing.T, mock *msb.MockMsbClient, name string) msbSdk.SandboxConfig {
	t.Helper()
	for _, call := range mock.CreatedSandboxCalls {
		if call.Name != name {
			continue
		}
		var cfg msbSdk.SandboxConfig
		for _, opt := range call.Opts {
			opt(&cfg)
		}
		return cfg
	}
	t.Fatalf("no CreateSandbox call for %q; calls: %v", name, mock.CreatedSandboxes)
	return msbSdk.SandboxConfig{}
}

func assertInfoCall(t *testing.T, ui *termio.Mock, wantSubstr string) {
	t.Helper()
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, wantSubstr) {
			return
		}
	}
	t.Errorf("expected InfoCall containing %q; got: %v", wantSubstr, ui.InfoCalls)
}

func applySandboxOpts(cfg *msbSdk.SandboxConfig, opts []msbSdk.SandboxOption) {
	for _, o := range opts {
		o(cfg)
	}
}
