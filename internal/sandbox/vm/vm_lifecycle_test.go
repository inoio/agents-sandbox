package vm

import (
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
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
	"github.com/inoio/agents-sandbox/internal/sysinfo"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestLifecycleSpinnerIsFinishedOnPreLockResolution pins that resolving a VM
// found before the creation lock finishes its "Checking project VM" spinner,
// stopping it on success and with the error on failure, so a resolution never
// leaves the spinner running behind whatever is printed next. The re-check
// under the lock has no spinner.
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

// TestDecideVMAction pins the action for every VM status: a missing VM is
// created, a running one connected, a booting one awaited, and a created,
// stopped or crashed one started. A draining, paused or unknown VM cannot take
// a new client.
func TestDecideVMAction(t *testing.T) {
	notFound := &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	cases := []struct {
		name     string
		lookup   error
		status   msbSdk.SandboxStatus
		want     vmAction
		wantText string
	}{
		{name: "missing", lookup: notFound, want: vmActionCreate},
		{name: "running", status: msbSdk.SandboxStatusRunning, want: vmActionConnect},
		{name: "starting", status: msbSdk.SandboxStatusStarting, want: vmActionWait},
		{name: "created", status: msbSdk.SandboxStatusCreated, want: vmActionStart},
		{name: "stopped", status: msbSdk.SandboxStatusStopped, want: vmActionStart},
		{name: "crashed", status: msbSdk.SandboxStatusCrashed, want: vmActionStart},
		{
			name:     "draining",
			status:   msbSdk.SandboxStatusDraining,
			wantText: `sandbox is not available for a new client: status is "draining"`,
		},
		{
			name:     "paused",
			status:   msbSdk.SandboxStatusPaused,
			wantText: `sandbox is not available for a new client: status is "paused"`,
		},
		{name: "unknown", status: "unknown-status", wantText: "unexpected sandbox status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decideVMAction(tc.lookup, tc.status)
			if tc.wantText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantText) {
					t.Fatalf("decideVMAction(%q) error = %v, want %q", tc.status, err, tc.wantText)
				}
				return
			}
			if err != nil {
				t.Fatalf("decideVMAction(%q): %v", tc.status, err)
			}
			if got != tc.want {
				t.Errorf("decideVMAction(%q) = %v, want %v", tc.status, got, tc.want)
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

// lockRecorder is a project-VM lock seam that records whether it is held.
type lockRecorder struct{ held bool }

func (l *lockRecorder) acquire(state.Key) (func(), error) {
	l.held = true
	return func() { l.held = false }, nil
}

// missingVMCreation is a request for a project VM that does not exist yet:
// every lookup misses and creating it succeeds.
func missingVMCreation(t *testing.T, client *msb.MockMsbClient, ui *termio.Mock) ProjectVMCreation {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	if client.GetSandboxFn == nil {
		client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
	}
	return ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options:      options.RunOptions{Agent: "opencode"},
		Image:        image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		HomeVolume:   "vol",
		RepoPath:     "/workspace",
		Key:          testVMKey(),
		RepoSlug:     testVMKey().Slug,
		Seams:        options.Seams{ProjectVMLock: noProjectVMLock},
		UI:           ui,
	}
}

// TestEnsureProjectVMCreatesAMissingVM pins the create path: a project VM that
// does not exist is created under the project VM name, labelled with its
// project and image, and reported as created.
func TestEnsureProjectVMCreatesAMissingVM(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	creation := missingVMCreation(t, client, &ui)
	// The production creation lock, on the mocked state dir.
	creation.Seams.ProjectVMLock = nil

	sb, boot, err := EnsureProjectVM(context.Background(), creation)
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if sb == nil {
		t.Fatal("EnsureProjectVM returned no sandbox")
	}
	if boot != VMBootCreated {
		t.Errorf("boot = %v, want VMBootCreated", boot)
	}
	name := ProjectVMName(testVMKey())
	if !reflect.DeepEqual(client.CreatedSandboxes, []string{name}) {
		t.Fatalf("created sandboxes = %v, want only %q", client.CreatedSandboxes, name)
	}
	cfg := createdProjectVMConfig(t, client, name)
	if cfg.Labels[naming.LabelProject] == "" || cfg.Labels[naming.LabelImage] == "" {
		t.Errorf("labels = %v, want the project and image labels", cfg.Labels)
	}
	assertInfoCall(t, &ui, "created new project VM")
}

// TestEnsureProjectVMRecreatesTheHeldVM pins a recreate: under the creation
// lock, the VM the decision was read from is destroyed (never re-looked-up by
// name) and a replacement is created under the current, prefix-free project VM
// name. A VM that is already gone is still recreated; one replaced by another
// sandbox, or one that cannot be destroyed, aborts the recreate.
func TestEnsureProjectVMRecreatesTheHeldVM(t *testing.T) {
	destroyFailed := errors.New("destroy failed")
	cases := []struct {
		name        string
		destroyErr  error
		wantVerbose string
		wantErr     func(error) bool
	}{
		{
			name:        "destroyed VM is replaced",
			wantVerbose: "replaced project VM " + existingVMName + "; recreating from new config",
		},
		{
			name:        "already gone VM is recreated",
			destroyErr:  &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"},
			wantVerbose: "project VM " + existingVMName + " was already gone; recreating from new config",
		},
		{
			name:       "VM replaced by another sandbox fails closed",
			destroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"},
			wantErr:    msb.IsReplaced,
		},
		{
			name:       "destroy failure aborts the recreate",
			destroyErr: destroyFailed,
			wantErr:    func(err error) bool { return errors.Is(err, destroyFailed) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			lock := &lockRecorder{}
			destroyAttempted := false
			// The held VM carries the legacy name, which the recreate retires.
			held := &msb.MockSandboxHandle{
				Name_:   existingVMName,
				Status_: msbSdk.SandboxStatusRunning,
				DestroyFn: func(context.Context) error {
					if !lock.held {
						t.Error("destroyed the VM outside the creation lock")
					}
					destroyAttempted = true
					return tc.destroyErr
				},
			}
			lookups := 0
			client := &msb.MockMsbClient{
				GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
					lookups++
					return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
				},
				CreateSandboxFn: func(_ context.Context, name string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
					if !lock.held {
						t.Error("created the replacement outside the creation lock")
					}
					return &msb.MockSandbox{Name_: name}, nil
				},
			}
			creation := missingVMCreation(t, client, &ui)
			creation.Existing = held
			creation.Options.Recreate = true
			creation.Seams.ProjectVMLock = lock.acquire

			sb, boot, err := EnsureProjectVM(context.Background(), creation)

			if !destroyAttempted {
				t.Fatal("recreate did not destroy the held VM")
			}
			if held.DidStop {
				t.Error("recreate stopped the VM separately; Destroy replaces the Stop")
			}
			if tc.wantErr != nil {
				if err == nil || !tc.wantErr(err) {
					t.Fatalf("error = %v, want the destroy failure", err)
				}
				if len(client.CreatedSandboxes) != 0 || lookups != 0 {
					t.Errorf("created %v after %d lookups, want neither after a failed destroy",
						client.CreatedSandboxes, lookups)
				}
				return
			}
			if err != nil {
				t.Fatalf("EnsureProjectVM: %v", err)
			}
			if sb == nil || boot != VMBootCreated {
				t.Errorf("result = %v, %v, want a created sandbox", sb, boot)
			}
			name := ProjectVMName(testVMKey())
			if !reflect.DeepEqual(client.CreatedSandboxes, []string{name}) {
				t.Errorf("created sandboxes = %v, want only %q", client.CreatedSandboxes, name)
			}
			if !verboseContains(ui.VerboseCalls, tc.wantVerbose) {
				t.Errorf("verbose output = %v, want %q", ui.VerboseCalls, tc.wantVerbose)
			}
		})
	}
}

// TestEnsureProjectVMFailsBeforeCreatingAVM pins that every step before a
// missing VM is created keeps its cause, and that a failure creates nothing.
func TestEnsureProjectVMFailsBeforeCreatingAVM(t *testing.T) {
	cases := []struct {
		name string
		// arrange makes the step under test fail with cause.
		arrange  func(t *testing.T, client *msb.MockMsbClient, creation *ProjectVMCreation, cause error)
		wantText string
		// wantNoCreate is false where the failure is the create itself.
		wantNoCreate bool
	}{
		{
			name: "creation lock cannot be acquired",
			arrange: func(_ *testing.T, _ *msb.MockMsbClient, creation *ProjectVMCreation, cause error) {
				creation.Seams.ProjectVMLock = func(state.Key) (func(), error) { return nil, cause }
			},
			wantText:     "acquire project flock",
			wantNoCreate: true,
		},
		{
			name: "microsandbox runtime cannot be installed",
			arrange: func(_ *testing.T, client *msb.MockMsbClient, _ *ProjectVMCreation, cause error) {
				client.EnsureInstalledFn = func(context.Context) error { return cause }
			},
			wantText:     "microsandbox runtime",
			wantNoCreate: true,
		},
		{
			name: "re-check under the lock fails",
			arrange: func(_ *testing.T, client *msb.MockMsbClient, _ *ProjectVMCreation, cause error) {
				client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) { return nil, cause }
			},
			wantText:     "re-check sandbox",
			wantNoCreate: true,
		},
		{
			name: "image cannot be loaded into microsandbox",
			arrange: func(t *testing.T, msbClient *msb.MockMsbClient, creation *ProjectVMCreation, cause error) {
				msbClient.ImageGetFn = func(context.Context, string) error { return errors.New("not cached") }
				docker.WithDockerMock(t, &docker.MockDockerClient{
					ImageSaveFn: func(context.Context, []string, ...client.ImageSaveOption) (client.ImageSaveResult, error) {
						return nil, cause
					},
				})
				creation.DockerClient = docker.Get()
			},
			wantNoCreate: true,
		},
		{
			name: "VM cannot be created",
			arrange: func(_ *testing.T, client *msb.MockMsbClient, _ *ProjectVMCreation, cause error) {
				client.CreateSandboxErr = cause
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)
			msbClient := &msb.MockMsbClient{}
			creation := missingVMCreation(t, msbClient, &ui)
			cause := errors.New("step failed")
			tc.arrange(t, msbClient, &creation, cause)

			_, _, err := EnsureProjectVM(context.Background(), creation)
			if !errors.Is(err, cause) || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("error = %v, want %q wrapping %v", err, tc.wantText, cause)
			}
			if tc.wantNoCreate && len(msbClient.CreatedSandboxes) != 0 {
				t.Errorf("created sandboxes = %v, want none", msbClient.CreatedSandboxes)
			}
		})
	}
}

// TestEnsureProjectVMSkipsTheLifecycleWithoutAVM pins that a run without a VM
// (--dry-run-vm) touches no VM and reports a connected boot.
func TestEnsureProjectVMSkipsTheLifecycleWithoutAVM(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	sb, boot, err := lookUpAndEnsureProjectVM(context.Background(), ProjectVMCreation{
		Client:     client,
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
	if len(client.CreatedSandboxes) != 0 {
		t.Errorf("created sandboxes = %v, want none without a VM", client.CreatedSandboxes)
	}
}

// existingVMName is the name a found VM carries in the existing-VM tests.
const existingVMName = "agents-sandbox-vm-test"

func runningVM(sb msb.Sandbox) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{Name_: existingVMName, Status_: msbSdk.SandboxStatusRunning, ConnectSb: sb}
}

func stoppedVM(sb msb.Sandbox) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{Name_: existingVMName, Status_: msbSdk.SandboxStatusStopped, StartSb: sb}
}

// startingVM is a VM another client is booting; its next refresh reads next.
func startingVM(next msb.SandboxHandle) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Name_:     existingVMName,
		Status_:   msbSdk.SandboxStatusStarting,
		RefreshFn: func(context.Context) (msb.SandboxHandle, error) { return next, nil },
	}
}

func pausedVM() *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{Name_: existingVMName, Status_: msbSdk.SandboxStatusPaused}
}

// connectFailsThen is a running VM whose first Connect fails (it auto-stopped
// in between); refreshing it reads next.
func connectFailsThen(next msb.SandboxHandle) *retryConnectHandle {
	return &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{Name_: existingVMName, Status_: msbSdk.SandboxStatusRunning},
		refresh:       next,
	}
}

// withFailingReconcile makes reconciling the VM's resources fail.
func withFailingReconcile(h *msb.MockSandboxHandle) *msb.MockSandboxHandle {
	h.Cfg = &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8}
	h.ModifyErr = errors.New("modify failed")
	return h
}

func noProjectVMLock(state.Key) (func(), error) { return func() {}, nil }

// TestEnsureProjectVMResolvesAnExistingVM pins what EnsureProjectVM makes of a
// VM it finds, by status: a running VM is connected, a stopped one started, a
// starting one awaited, and a connect that fails because the VM auto-stopped in
// between is resumed from the refreshed status. A failed resource reconcile
// only warns. Each case runs for a VM found before the creation lock and for
// one another invocation created while this one waited for the lock.
func TestEnsureProjectVMResolvesAnExistingVM(t *testing.T) {
	type outcome int
	const (
		resolved outcome = iota
		failsWithCause
		refused
	)
	cases := []struct {
		name string
		// vm returns the VM as found; sb is the sandbox a resolution returns,
		// cause the error a failure must wrap.
		vm        func(sb msb.Sandbox, cause error) msb.SandboxHandle
		outcome   outcome
		wantBoot  VMBoot
		wantInfo  string
		wantRetry bool
		wantWarn  bool
		// wantRefusal is part of the error a refused status reports.
		wantRefusal string
	}{
		{
			name:     "running VM is connected",
			vm:       func(sb msb.Sandbox, _ error) msb.SandboxHandle { return runningVM(sb) },
			wantBoot: VMBootConnected,
			wantInfo: "connected to existing project VM",
		},
		{
			name:     "stopped VM is started",
			vm:       func(sb msb.Sandbox, _ error) msb.SandboxHandle { return stoppedVM(sb) },
			wantBoot: VMBootStarted,
			wantInfo: "started existing project VM",
		},
		{
			name:     "starting VM is awaited and connected",
			vm:       func(sb msb.Sandbox, _ error) msb.SandboxHandle { return startingVM(runningVM(sb)) },
			wantBoot: VMBootConnected,
		},
		{
			name:     "starting VM whose boot failed is started",
			vm:       func(sb msb.Sandbox, _ error) msb.SandboxHandle { return startingVM(stoppedVM(sb)) },
			wantBoot: VMBootStarted,
		},
		{
			name: "start lost to a concurrent start is converged",
			vm: func(sb msb.Sandbox, _ error) msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   existingVMName,
					Status_: msbSdk.SandboxStatusCreated,
					StartFn: func(context.Context) (msb.Sandbox, error) {
						return nil, &msbSdk.Error{
							Kind:    msbSdk.ErrSandboxStillRunning,
							Message: "started by another caller",
						}
					},
					RefreshFn: func(context.Context) (msb.SandboxHandle, error) { return runningVM(sb), nil },
				}
			},
			wantBoot: VMBootConnected,
		},
		{
			name:      "failed connect to a still running VM reconnects",
			vm:        func(sb msb.Sandbox, _ error) msb.SandboxHandle { return connectFailsThen(runningVM(sb)) },
			wantBoot:  VMBootConnected,
			wantRetry: true,
		},
		{
			name:      "failed connect to a VM that stopped starts it",
			vm:        func(sb msb.Sandbox, _ error) msb.SandboxHandle { return connectFailsThen(stoppedVM(sb)) },
			wantBoot:  VMBootStarted,
			wantRetry: true,
		},
		{
			name: "failed connect to a VM that is starting awaits it",
			vm: func(sb msb.Sandbox, _ error) msb.SandboxHandle {
				return connectFailsThen(startingVM(runningVM(sb)))
			},
			wantBoot:  VMBootConnected,
			wantRetry: true,
		},
		{
			name:     "connected VM whose reconcile fails is kept",
			vm:       func(sb msb.Sandbox, _ error) msb.SandboxHandle { return withFailingReconcile(runningVM(sb)) },
			wantBoot: VMBootConnected,
			wantInfo: "connected to existing project VM",
			wantWarn: true,
		},
		{
			name:     "started VM whose reconcile fails is kept",
			vm:       func(sb msb.Sandbox, _ error) msb.SandboxHandle { return withFailingReconcile(stoppedVM(sb)) },
			wantBoot: VMBootStarted,
			wantInfo: "started existing project VM",
			wantWarn: true,
		},
		{
			name: "awaited VM whose reconcile fails is kept",
			vm: func(sb msb.Sandbox, _ error) msb.SandboxHandle {
				return withFailingReconcile(startingVM(runningVM(sb)))
			},
			wantBoot: VMBootConnected,
			wantWarn: true,
		},
		{
			name: "reconnected VM whose reconcile fails is kept",
			vm: func(sb msb.Sandbox, _ error) msb.SandboxHandle {
				return connectFailsThen(withFailingReconcile(runningVM(sb)))
			},
			wantBoot:  VMBootConnected,
			wantRetry: true,
			wantWarn:  true,
		},
		{
			name: "restarted VM whose reconcile fails is kept",
			vm: func(sb msb.Sandbox, _ error) msb.SandboxHandle {
				return connectFailsThen(withFailingReconcile(stoppedVM(sb)))
			},
			wantBoot:  VMBootStarted,
			wantRetry: true,
			wantWarn:  true,
		},
		{
			name: "unknown status is refused",
			vm: func(msb.Sandbox, error) msb.SandboxHandle {
				return &msb.MockSandboxHandle{Name_: existingVMName, Status_: msbSdk.SandboxStatus("unknown-status")}
			},
			outcome:     refused,
			wantRefusal: `unexpected sandbox status: "unknown-status"`,
		},
		{
			name: "start failure is reported",
			vm: func(_ msb.Sandbox, cause error) msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:    existingVMName,
					Status_:  msbSdk.SandboxStatusStopped,
					StartErr: cause,
				}
			},
			outcome: failsWithCause,
		},
		{
			name: "failure while awaiting a starting VM is reported",
			vm: func(_ msb.Sandbox, cause error) msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:     existingVMName,
					Status_:   msbSdk.SandboxStatusStarting,
					RefreshFn: func(context.Context) (msb.SandboxHandle, error) { return nil, cause },
				}
			},
			outcome: failsWithCause,
		},
		{
			name:        "starting VM that pauses is refused",
			vm:          func(msb.Sandbox, error) msb.SandboxHandle { return startingVM(pausedVM()) },
			outcome:     refused,
			wantRefusal: `status is "paused"`,
		},
		{
			name: "refresh after a failed connect fails",
			vm: func(_ msb.Sandbox, cause error) msb.SandboxHandle {
				return &retryConnectHandle{
					SandboxHandle: &msb.MockSandboxHandle{Name_: existingVMName, Status_: msbSdk.SandboxStatusRunning},
					refreshErr:    cause,
				}
			},
			outcome:   failsWithCause,
			wantRetry: true,
		},
		{
			name:        "failed connect to a VM that paused is refused",
			vm:          func(msb.Sandbox, error) msb.SandboxHandle { return connectFailsThen(pausedVM()) },
			outcome:     refused,
			wantRetry:   true,
			wantRefusal: `status is "paused"`,
		},
		{
			name: "reconnect that fails again is reported",
			vm: func(_ msb.Sandbox, cause error) msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:      existingVMName,
					Status_:    msbSdk.SandboxStatusRunning,
					ConnectErr: cause,
				}
			},
			outcome:   failsWithCause,
			wantRetry: true,
		},
		{
			name: "start after a failed connect fails",
			vm: func(_ msb.Sandbox, cause error) msb.SandboxHandle {
				return connectFailsThen(&msb.MockSandboxHandle{
					Name_:    existingVMName,
					Status_:  msbSdk.SandboxStatusStopped,
					StartErr: cause,
				})
			},
			outcome:   failsWithCause,
			wantRetry: true,
		},
	}

	paths := []struct {
		name string
		// arrange hands EnsureProjectVM the VM: as the handle found before
		// the lock, or through the lookup that runs again under the lock.
		arrange func(client *msb.MockMsbClient, creation *ProjectVMCreation, vm msb.SandboxHandle)
	}{
		{"found before the lock", func(_ *msb.MockMsbClient, creation *ProjectVMCreation, vm msb.SandboxHandle) {
			creation.Existing = vm
		}},
		{
			"created while waiting for the lock",
			func(client *msb.MockMsbClient, _ *ProjectVMCreation, vm msb.SandboxHandle) {
				client.SetGotSandbox(vm)
			},
		},
	}

	for _, path := range paths {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				ui := termio.NewTestMock(t)
				client := &msb.MockMsbClient{}
				creation := ProjectVMCreation{
					Client:       client,
					DockerClient: docker.Get(),
					// CPUs differs from the VM's recorded config, so a resolution
					// reconciles the resources.
					Options:    options.RunOptions{Agent: "opencode", CPUs: 4},
					Image:      image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
					HomeVolume: "vol",
					RepoPath:   "/workspace",
					Key:        testVMKey(),
					RepoSlug:   testVMKey().Slug,
					Seams:      options.Seams{ProjectVMLock: noProjectVMLock},
					UI:         &ui,
				}
				want := &msb.MockSandbox{Name_: existingVMName}
				cause := errors.New("lifecycle failed")
				path.arrange(client, &creation, tc.vm(want, cause))

				sb, boot, err := EnsureProjectVM(context.Background(), creation)

				switch tc.outcome {
				case resolved:
					if err != nil {
						t.Fatalf("EnsureProjectVM: %v", err)
					}
					if sb != want {
						t.Errorf("sandbox = %v, want the VM's sandbox %v", sb, want)
					}
					if boot != tc.wantBoot {
						t.Errorf("boot = %v, want %v", boot, tc.wantBoot)
					}
				case failsWithCause:
					if !errors.Is(err, cause) {
						t.Fatalf("error = %v, want it to wrap %v", err, cause)
					}
				case refused:
					if err == nil || !strings.Contains(err.Error(), tc.wantRefusal) {
						t.Fatalf("error = %v, want a refusal containing %q", err, tc.wantRefusal)
					}
				}
				if len(client.CreatedSandboxes) != 0 {
					t.Errorf("created sandboxes %v, want none for an existing VM", client.CreatedSandboxes)
				}
				if tc.wantInfo != "" {
					// The VM is named as found before the lock, and by the project
					// VM name the re-check looked up after it.
					name := ProjectVMName(testVMKey())
					if creation.Existing != nil {
						name = existingVMName
					}
					assertInfoCall(t, &ui, tc.wantInfo+": "+name)
				}
				if got := verboseContains(ui.VerboseCalls, "retrying via Start"); got != tc.wantRetry {
					t.Errorf("connect retry logged = %v, want %v (verbose %v)", got, tc.wantRetry, ui.VerboseCalls)
				}
				if gotWarn := len(ui.WarnCalls) > 0; gotWarn != tc.wantWarn {
					t.Errorf("warnings = %v, want a reconcile warning: %v", ui.WarnCalls, tc.wantWarn)
				}
				if tc.wantWarn &&
					!strings.Contains(strings.Join(ui.WarnCalls, "\n"), "could not reconcile VM resources") {
					t.Errorf("warnings = %v, want the reconcile warning", ui.WarnCalls)
				}
			})
		}
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

// TestConvergeExistingVM pins how waiting for a booting VM ends: it polls
// while the VM is starting, connects once it runs, starts it if the boot
// stopped it, and refuses a VM that cannot take a new client. A failed refresh
// or connect, and a cancelled wait, keep their cause.
func TestConvergeExistingVM(t *testing.T) {
	orig := sandboxLifecyclePollInterval
	t.Cleanup(func() { sandboxLifecyclePollInterval = orig })
	sandboxLifecyclePollInterval = time.Millisecond

	sb := &msb.MockSandbox{Name_: "vm"}
	cause := errors.New("lifecycle failed")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	// refreshesTo returns a handle whose refreshes read the given handles in
	// order, and the counter of how many it served.
	refreshesTo := func(handles ...msb.SandboxHandle) (*msb.MockSandboxHandle, *int) {
		served := 0
		return &msb.MockSandboxHandle{RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
			next := handles[served]
			served++
			return next, nil
		}}, &served
	}
	cases := []struct {
		name          string
		ctx           context.Context
		handle        func() (msb.SandboxHandle, *int)
		wantBoot      VMBoot
		wantRefreshes int
		wantCause     error
		wantText      string
	}{
		{
			name: "polls while starting and connects once running",
			handle: func() (msb.SandboxHandle, *int) {
				return refreshesTo(
					&msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusStarting},
					&msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusRunning, ConnectSb: sb},
				)
			},
			wantBoot:      VMBootConnected,
			wantRefreshes: 2,
		},
		{
			name: "starts a VM whose boot stopped it",
			handle: func() (msb.SandboxHandle, *int) {
				return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusStopped, StartSb: sb}, nil
			},
			wantBoot: VMBootStarted,
		},
		{
			name: "refresh failure",
			handle: func() (msb.SandboxHandle, *int) {
				return &msb.MockSandboxHandle{RefreshFn: func(context.Context) (msb.SandboxHandle, error) {
					return nil, cause
				}}, nil
			},
			wantCause: cause,
		},
		{
			name: "connect failure",
			handle: func() (msb.SandboxHandle, *int) {
				return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusRunning, ConnectErr: cause}, nil
			},
			wantCause: cause,
		},
		{
			name: "cancelled while starting",
			ctx:  cancelled,
			handle: func() (msb.SandboxHandle, *int) {
				return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusStarting}, nil
			},
			wantCause: context.Canceled,
		},
		{
			name: "draining VM is refused",
			handle: func() (msb.SandboxHandle, *int) {
				return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusDraining}, nil
			},
			wantText: `not available for a new client: status is "draining"`,
		},
		{
			name: "paused VM is refused",
			handle: func() (msb.SandboxHandle, *int) {
				return &msb.MockSandboxHandle{Status_: msbSdk.SandboxStatusPaused}, nil
			},
			wantText: `not available for a new client: status is "paused"`,
		},
		{
			name: "unknown status is refused",
			handle: func() (msb.SandboxHandle, *int) {
				return &msb.MockSandboxHandle{Status_: "future-status"}, nil
			},
			wantText: `unexpected sandbox status: "future-status"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			handle, refreshes := tc.handle()
			got, boot, err := convergeExistingVM(ctx, handle)
			switch {
			case tc.wantCause != nil:
				if !errors.Is(err, tc.wantCause) {
					t.Fatalf("error = %v, want %v", err, tc.wantCause)
				}
			case tc.wantText != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantText) {
					t.Fatalf("error = %v, want %q", err, tc.wantText)
				}
			default:
				if err != nil || got != sb || boot != tc.wantBoot {
					t.Fatalf("convergeExistingVM = %v, %v, %v; want %v, %v", got, boot, err, sb, tc.wantBoot)
				}
			}
			if refreshes != nil && *refreshes != tc.wantRefreshes {
				t.Errorf("refreshes = %d, want %d", *refreshes, tc.wantRefreshes)
			}
		})
	}
}

// newVMCreation is a create request for the project VM with opts; the runner
// image is cached in microsandbox.
func newVMCreation(
	t *testing.T,
	client *msb.MockMsbClient,
	ui *termio.Mock,
	opts options.RunOptions,
) ProjectVMCreation {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	return ProjectVMCreation{
		Client:       client,
		DockerClient: docker.Get(),
		Options:      opts,
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
	}
}

// TestCreateProjectVMConfiguresTheVM pins the config a project VM is created
// with: the host's CPUs unless a count is configured, the default idle timeout
// unless one is configured, a managed root disk only for a disk size, a
// deny-by-default network unless a profile opens it, and published ports only
// for serve-only mode's planned host port and the user's port mappings.
func TestCreateProjectVMConfiguresTheVM(t *testing.T) {
	cases := []struct {
		name  string
		opts  options.RunOptions
		check func(t *testing.T, cfg msbSdk.SandboxConfig)
	}{
		{
			name: "defaults",
			opts: options.RunOptions{Memory: "1G"},
			check: func(t *testing.T, cfg msbSdk.SandboxConfig) {
				if cfg.CPUs != sysinfo.NumCPUs() || cfg.MaxCPUs != sysinfo.NumCPUs() {
					t.Errorf("CPUs/MaxCPUs = %d/%d, want the host's %d", cfg.CPUs, cfg.MaxCPUs, sysinfo.NumCPUs())
				}
				if cfg.IdleTimeout != options.DefaultVMIdleTimeout {
					t.Errorf("IdleTimeout = %v, want the default %v", cfg.IdleTimeout, options.DefaultVMIdleTimeout)
				}
				if cfg.RootDisk != nil {
					t.Errorf("RootDisk = %+v, want none without a disk size", cfg.RootDisk)
				}
				if cfg.Network == nil || cfg.Network.DefaultEgress != msbSdk.PolicyActionDeny {
					t.Errorf("network = %+v, want deny-by-default egress", cfg.Network)
				}
				if len(cfg.PortBindings) != 0 {
					t.Errorf("port bindings = %v, want none outside serve-only mode", cfg.PortBindings)
				}
			},
		},
		{
			name: "configured CPUs",
			opts: options.RunOptions{Memory: "1G", CPUs: 1},
			check: func(t *testing.T, cfg msbSdk.SandboxConfig) {
				if cfg.CPUs != 1 || cfg.MaxCPUs != sysinfo.NumCPUs() {
					t.Errorf("CPUs/MaxCPUs = %d/%d, want 1/%d", cfg.CPUs, cfg.MaxCPUs, sysinfo.NumCPUs())
				}
			},
		},
		{
			name: "configured idle timeout",
			opts: options.RunOptions{Memory: "1G", IdleTimeout: 45 * time.Second},
			check: func(t *testing.T, cfg msbSdk.SandboxConfig) {
				if cfg.IdleTimeout != 45*time.Second {
					t.Errorf("IdleTimeout = %v, want 45s", cfg.IdleTimeout)
				}
			},
		},
		{
			name: "disk size",
			opts: options.RunOptions{Memory: "1G", DiskSize: "16G"},
			check: func(t *testing.T, cfg msbSdk.SandboxConfig) {
				if cfg.RootDisk == nil || cfg.RootDisk.Kind() != msbSdk.RootDiskKindManaged ||
					cfg.RootDisk.SizeMiB != 16*1024 {
					t.Errorf("RootDisk = %+v, want a managed 16 GiB disk", cfg.RootDisk)
				}
			},
		},
		{
			name: "public network",
			opts: options.RunOptions{
				Memory:  "1G",
				Network: network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
			},
			check: func(t *testing.T, cfg msbSdk.SandboxConfig) {
				if cfg.Network == nil || !slices.ContainsFunc(cfg.Network.Rules, func(r msbSdk.PolicyRule) bool {
					return r.Action == msbSdk.PolicyActionAllow && r.Destination == "public"
				}) {
					t.Errorf("network = %+v, want an egress rule allowing public destinations", cfg.Network)
				}
			},
		},
		{
			name: "serve-only on the planned host port",
			opts: options.RunOptions{Memory: "1G", ServeOnly: true, ServeHostPort: ports.ServeOnlyBasePort + 17},
			check: func(t *testing.T, cfg msbSdk.SandboxConfig) {
				want := ports.ServeOnlyBindings(ports.ServeOnlyBasePort + 17)
				if !reflect.DeepEqual(cfg.PortBindings, want) {
					t.Errorf("port bindings = %#v, want %#v", cfg.PortBindings, want)
				}
			},
		},
		{
			name: "user port mappings",
			opts: options.RunOptions{Memory: "1G", Ports: []ports.PortMapping{{HostPort: 3000, GuestPort: 8080}}},
			check: func(t *testing.T, cfg msbSdk.SandboxConfig) {
				want := ports.UserPortBindings([]ports.PortMapping{{HostPort: 3000, GuestPort: 8080}})
				if !reflect.DeepEqual(cfg.PortBindings, want) {
					t.Errorf("port bindings = %#v, want the user mapping %#v", cfg.PortBindings, want)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &msb.MockMsbClient{}
			ui := termio.NewTestMock(t)
			name := ProjectVMName(testVMKey())

			sb, created, err := createProjectVM(context.Background(), newVMCreation(t, client, &ui, tc.opts), name)
			if err != nil {
				t.Fatalf("createProjectVM: %v", err)
			}
			if sb == nil || !created {
				t.Fatalf("createProjectVM = %v, created %v; want a created sandbox", sb, created)
			}
			if !reflect.DeepEqual(client.CreatedSandboxes, []string{name}) {
				t.Fatalf("created sandboxes = %v, want only %q", client.CreatedSandboxes, name)
			}
			// The cached-image check and the VM start each finish their spinner.
			if ui.SpinnerStops != 2 || len(ui.SpinnerStopErrors) != 0 {
				t.Errorf("spinner stops = %d, errors %v; want two clean stops", ui.SpinnerStops, ui.SpinnerStopErrors)
			}
			tc.check(t, createdProjectVMConfig(t, client, name))
		})
	}
}

// TestCreateProjectVMReportsFailures pins that a network policy that cannot be
// applied and a failed create are reported with their cause, nothing is
// reported as created, and the spinner is finished with the error.
func TestCreateProjectVMReportsFailures(t *testing.T) {
	createFailed := errors.New("create failed")
	cases := []struct {
		name     string
		opts     options.RunOptions
		client   *msb.MockMsbClient
		wantText string
	}{
		{
			name: "unknown network profile",
			opts: options.RunOptions{
				Memory:  "1G",
				Network: network.Policy{Profiles: []network.Profile{network.Profile("bogus-profile")}},
			},
			client:   &msb.MockMsbClient{},
			wantText: "network config: ",
		},
		{
			name:     "create fails",
			opts:     options.RunOptions{Memory: "1G"},
			client:   &msb.MockMsbClient{CreateSandboxErr: createFailed},
			wantText: "create sandbox: create failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := termio.NewTestMock(t)

			sb, created, err := createProjectVM(
				context.Background(),
				newVMCreation(t, tc.client, &ui, tc.opts),
				ProjectVMName(testVMKey()),
			)
			if err == nil || !strings.Contains(err.Error(), tc.wantText) || errors.Unwrap(err) == nil {
				t.Fatalf("error = %v, want %q wrapping its cause", err, tc.wantText)
			}
			if sb != nil || created {
				t.Errorf("createProjectVM = %v, created %v; want nothing created", sb, created)
			}
			if len(ui.SpinnerStopErrors) != 1 {
				t.Errorf("spinner stop errors = %v, want the spinner stopped with the error", ui.SpinnerStopErrors)
			}
		})
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
