package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
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
			name: "connect-or-start error",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{Name_: "vm", Status_: msb.SandboxStatusPaused}
			},
			wantErr:        true,
			wantStopErrors: 1,
		},
		{
			name: "start success",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   "vm",
					Status_: msb.SandboxStatusStopped,
					StartSb: &msb.MockSandbox{Name_: "vm"},
				}
			},
			wantStops: 1,
		},
		{
			name: "connect success",
			handle: func() msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:     "vm",
					Status_:   msb.SandboxStatusRunning,
					ConnectSb: &msb.MockSandbox{Name_: "vm"},
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

// TestProjectVMLockSeamResolution pins the seam contract: a nil
// options.Seams.ProjectVMLock uses the production default (acquireProjectVMLock),
// while a set field is called with the state.Key mapped to options.ProjectVMKey.
func TestProjectVMLockSeamResolution(t *testing.T) {
	orig := acquireProjectVMLock
	t.Cleanup(func() { acquireProjectVMLock = orig })

	defaultErr := errors.New("production default lock")
	acquireProjectVMLock = func(state.Key) (func(), error) { return nil, defaultErr }
	if _, err := projectVMLock(options.Seams{})(testVMKey()); !errors.Is(err, defaultErr) {
		t.Errorf("nil seam lock error = %v, want the production default %v", err, defaultErr)
	}

	seamErr := errors.New("injected lock")
	var gotKey options.ProjectVMKey
	seam := func(k options.ProjectVMKey) (func(), error) { gotKey = k; return nil, seamErr }
	if _, err := projectVMLock(options.Seams{ProjectVMLock: seam})(testVMKey()); !errors.Is(err, seamErr) {
		t.Errorf("set seam lock error = %v, want %v", err, seamErr)
	}
	if wantKey := (options.ProjectVMKey{Slug: "testproj", Agent: "opencode"}); gotKey != wantKey {
		t.Errorf("seam key = %+v, want %+v", gotKey, wantKey)
	}
}

// relativeStatePaths is a ConfigPaths whose roots are relative, standing in for
// an unresolved state root ($HOME unset, or a mutation-sweep helper returning
// the zero value). The project-VM lock dir derives from UserStateDir, so it is
// relative to the process working directory — exactly what the absolute-path
// guard rejects.
type relativeStatePaths struct{}

func (relativeStatePaths) UserConfigDir() string                { return "cfg" }
func (relativeStatePaths) UserSlugConfigDir(slug string) string { return filepath.Join("cfg", slug) }
func (relativeStatePaths) UserCacheDir() string                 { return "cache" }
func (relativeStatePaths) UserStateDir() string                 { return "state" }
func (relativeStatePaths) UserDataDir() string                  { return "data" }
func (relativeStatePaths) UserHomeDir() (string, error)         { return "home", nil }
func (relativeStatePaths) ProjectConfigDir() string             { return ".agents-sandbox" }
func (relativeStatePaths) ProjectConfigDirAbs() string          { return "" }
func (relativeStatePaths) IgnoredWorktreeConfigDir() string     { return "" }

func (relativeStatePaths) ProjectDockerfile() string {
	return filepath.Join(".agents-sandbox", "Dockerfile")
}

// TestAcquireProjectVMLockRejectsARelativeStateRoot pins the VM caller: the
// production creation lock vm.EnsureProjectVM reaches through this default must
// reject a lock dir that did not resolve to an absolute path rather than creating
// the lock file under the process working directory.
func TestAcquireProjectVMLockRejectsARelativeStateRoot(t *testing.T) {
	// Pin the CWD so a write made with the guard removed lands in a temp tree,
	// not the package directory.
	t.Chdir(t.TempDir())
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths { return relativeStatePaths{} }
	t.Cleanup(func() { configpaths.Get = orig })

	release, err := acquireProjectVMLock(testVMKey())
	if err == nil {
		release()
		t.Fatal("acquireProjectVMLock with a relative state root = nil error, want a rejection")
	}
}

// lockRecorder is a project-VM lock seam that records whether it is held.
type lockRecorder struct{ held bool }

func (l *lockRecorder) acquire(options.ProjectVMKey) (func(), error) {
	l.held = true
	return func() { l.held = false }, nil
}

// missingVMCreation is a request for a project VM that does not exist yet:
// every lookup misses and creating it succeeds.
func missingVMCreation(t *testing.T, client *msb.MockMsbClient, ui *termio.Mock) ProjectVMCreation {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	dockertest.WithNoopDockerMock(t)
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

// TestEnsureProjectVMFailsClosedOnATakenName pins that a sandbox appearing
// under the project VM name after the re-check under the creation lock is
// neither replaced nor attached to: the create fails and names the sandbox.
func TestEnsureProjectVMFailsClosedOnATakenName(t *testing.T) {
	ui := termio.NewTestMock(t)
	taken := &msbSdk.Error{Kind: msbSdk.ErrSandboxAlreadyExists, Message: "sandbox already exists"}
	client := &msb.MockMsbClient{
		CreateSandboxFn: func(context.Context, string, msb.SandboxSpec) (msb.Sandbox, error) {
			return nil, taken
		},
	}
	creation := missingVMCreation(t, client, &ui)

	sb, _, err := EnsureProjectVM(context.Background(), creation)

	if sb != nil || !msb.IsSandboxAlreadyExists(err) {
		t.Fatalf("EnsureProjectVM = %v, %v; want no sandbox and the already-exists error", sb, err)
	}
	name := ProjectVMName(testVMKey())
	if want := "project VM " + strconv.Quote(name) + " appeared"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to contain %q", err, want)
	}
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
				Status_: msb.SandboxStatusRunning,
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
				CreateSandboxFn: func(_ context.Context, name string, _ msb.SandboxSpec) (msb.Sandbox, error) {
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
				creation.Seams.ProjectVMLock = func(options.ProjectVMKey) (func(), error) { return nil, cause }
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
				dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
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
	return &msb.MockSandboxHandle{Name_: existingVMName, Status_: msb.SandboxStatusRunning, ConnectSb: sb}
}

func stoppedVM(sb msb.Sandbox) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{Name_: existingVMName, Status_: msb.SandboxStatusStopped, StartSb: sb}
}

// withFailingReconcile makes reconciling the VM's resources fail.
func withFailingReconcile(h *msb.MockSandboxHandle) *msb.MockSandboxHandle {
	h.Cfg = &msb.SandboxConfig{CPUs: 2, MaxCPUs: 8}
	h.ModifyErr = errors.New("modify failed")
	return h
}

func noProjectVMLock(options.ProjectVMKey) (func(), error) { return func() {}, nil }

// TestEnsureProjectVMResolvesAnExistingVM pins what EnsureProjectVM makes of a
// VM it finds: the runtime's connect-or-start connects or boots it, and the
// boot is reported as started only when this run booted it, so a VM another
// caller booted counts as connected. A failed connect-or-start is reported with
// its cause and a failed resource reconcile only warns. Each case runs for a VM
// found before the creation lock and for one another invocation created while
// this one waited for the lock.
func TestEnsureProjectVMResolvesAnExistingVM(t *testing.T) {
	type outcome int
	const (
		resolved outcome = iota
		failsWithCause
		failsReplaced
	)
	replaced := &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"}
	cases := []struct {
		name string
		// vm returns the VM as found; sb is the sandbox a resolution returns,
		// cause the error a failure must wrap.
		vm       func(sb msb.Sandbox, cause error) msb.SandboxHandle
		outcome  outcome
		wantBoot VMBoot
		wantInfo string
		wantWarn bool
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
			name: "VM another caller booted is connected",
			vm: func(sb msb.Sandbox, _ error) msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   existingVMName,
					Status_: msb.SandboxStatusStopped,
					ConnectOrStartFn: func(context.Context) (msb.Sandbox, bool, error) {
						return sb, false, nil
					},
				}
			},
			wantBoot: VMBootConnected,
			wantInfo: "connected to existing project VM",
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
			name: "connect-or-start failure is reported",
			vm: func(_ msb.Sandbox, cause error) msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:    existingVMName,
					Status_:  msb.SandboxStatusStopped,
					StartErr: cause,
				}
			},
			outcome: failsWithCause,
		},
		{
			name: "replaced VM fails closed",
			vm: func(msb.Sandbox, error) msb.SandboxHandle {
				return &msb.MockSandboxHandle{
					Name_:   existingVMName,
					Status_: msb.SandboxStatusRunning,
					ConnectOrStartFn: func(context.Context) (msb.Sandbox, bool, error) {
						return nil, false, replaced
					},
				}
			},
			outcome: failsReplaced,
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
				case failsReplaced:
					if !msb.IsReplaced(err) || sb != nil {
						t.Fatalf("EnsureProjectVM = %v, %v; want a replaced error and no sandbox", sb, err)
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
	dockertest.WithNoopDockerMock(t)
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
		check func(t *testing.T, spec msb.SandboxSpec)
	}{
		{
			name: "defaults",
			opts: options.RunOptions{Memory: "1G"},
			check: func(t *testing.T, spec msb.SandboxSpec) {
				//testenv:allow asserts the VM CPU default is the host's CPU count
				hostCPUs := sysinfo.NumCPUs()
				if spec.CPUs != hostCPUs || spec.MaxCPUs != hostCPUs {
					t.Errorf("CPUs/MaxCPUs = %d/%d, want the host's %d", spec.CPUs, spec.MaxCPUs, hostCPUs)
				}
				if spec.IdleTimeout != options.DefaultVMIdleTimeout {
					t.Errorf("IdleTimeout = %v, want the default %v", spec.IdleTimeout, options.DefaultVMIdleTimeout)
				}
				if spec.RootDiskMiB != 0 {
					t.Errorf("RootDiskMiB = %d, want none without a disk size", spec.RootDiskMiB)
				}
				if !slices.Equal(spec.Network.Profiles, []msb.NetworkProfile{msb.NetworkProfilePublic}) {
					t.Errorf("network profiles = %v, want the default public profile", spec.Network.Profiles)
				}
				if len(spec.Ports) != 0 {
					t.Errorf("ports = %v, want none outside serve-only mode", spec.Ports)
				}
			},
		},
		{
			name: "configured CPUs",
			opts: options.RunOptions{Memory: "1G", CPUs: 1},
			check: func(t *testing.T, spec msb.SandboxSpec) {
				//testenv:allow asserts the VM CPU maximum is the host's CPU count
				hostCPUs := sysinfo.NumCPUs()
				if spec.CPUs != 1 || spec.MaxCPUs != hostCPUs {
					t.Errorf("CPUs/MaxCPUs = %d/%d, want 1/%d", spec.CPUs, spec.MaxCPUs, hostCPUs)
				}
			},
		},
		{
			name: "configured idle timeout",
			opts: options.RunOptions{Memory: "1G", IdleTimeout: 45 * time.Second},
			check: func(t *testing.T, spec msb.SandboxSpec) {
				if spec.IdleTimeout != 45*time.Second {
					t.Errorf("IdleTimeout = %v, want 45s", spec.IdleTimeout)
				}
			},
		},
		{
			name: "memory above the boot maximum is clamped to it",
			opts: options.RunOptions{Memory: fmt.Sprintf("%dM", BootMaxMemoryMiB()+1024)},
			check: func(t *testing.T, spec msb.SandboxSpec) {
				if spec.MemoryMiB != BootMaxMemoryMiB() || spec.MaxMemoryMiB != BootMaxMemoryMiB() {
					t.Errorf("memory = %d MiB, max = %d MiB; want both at the boot maximum %d MiB",
						spec.MemoryMiB, spec.MaxMemoryMiB, BootMaxMemoryMiB())
				}
			},
		},
		{
			name: "disk size",
			opts: options.RunOptions{Memory: "1G", DiskSize: "16G"},
			check: func(t *testing.T, spec msb.SandboxSpec) {
				if spec.RootDiskMiB != 16*1024 {
					t.Errorf("RootDiskMiB = %d, want a managed 16 GiB disk", spec.RootDiskMiB)
				}
			},
		},
		{
			name: "public network",
			opts: options.RunOptions{
				Memory:  "1G",
				Network: network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
			},
			check: func(t *testing.T, spec msb.SandboxSpec) {
				if !slices.Contains(spec.Network.Profiles, msb.NetworkProfilePublic) {
					t.Errorf("network profiles = %v, want the public profile", spec.Network.Profiles)
				}
			},
		},
		{
			name: "serve-only on the planned host port",
			opts: options.RunOptions{Memory: "1G", ServeOnly: true, ServeHostPort: ports.ServeOnlyBasePort + 17},
			check: func(t *testing.T, spec msb.SandboxSpec) {
				want := ports.ServeOnlyBindings(ports.ServeOnlyBasePort + 17)
				if !reflect.DeepEqual(spec.Ports, want) {
					t.Errorf("port bindings = %#v, want %#v", spec.Ports, want)
				}
			},
		},
		{
			name: "user port mappings",
			opts: options.RunOptions{Memory: "1G", Ports: []ports.PortMapping{{HostPort: 3000, GuestPort: 8080}}},
			check: func(t *testing.T, spec msb.SandboxSpec) {
				want := ports.UserPortBindings([]ports.PortMapping{{HostPort: 3000, GuestPort: 8080}})
				if !reflect.DeepEqual(spec.Ports, want) {
					t.Errorf("port bindings = %#v, want the user mapping %#v", spec.Ports, want)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &msb.MockMsbClient{}
			ui := termio.NewTestMock(t)
			name := ProjectVMName(testVMKey())

			sb, err := createProjectVM(context.Background(), newVMCreation(t, client, &ui, tc.opts), name)
			if err != nil {
				t.Fatalf("createProjectVM: %v", err)
			}
			if sb == nil {
				t.Fatal("createProjectVM returned no sandbox")
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

			sb, err := createProjectVM(
				context.Background(),
				newVMCreation(t, tc.client, &ui, tc.opts),
				ProjectVMName(testVMKey()),
			)
			if err == nil || !strings.Contains(err.Error(), tc.wantText) || errors.Unwrap(err) == nil {
				t.Fatalf("error = %v, want %q wrapping its cause", err, tc.wantText)
			}
			if sb != nil {
				t.Errorf("createProjectVM = %v, want no sandbox", sb)
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
	dockertest.WithNoopDockerMock(t)
	key := testVMKey()
	legacyRef := naming.DockerImageRef(key.Slug, key.Agent)
	client := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, ref string) error {
			if ref == legacyRef {
				return nil
			}
			return errors.New("not cached")
		},
		ImageInspectFn: func(_ context.Context, _ string) (*msb.ImageConfig, error) {
			return &msb.ImageConfig{Digest: "sha256:same"}, nil
		},
	}
	testUI := termio.NewTestMock(t)
	ui := &testUI

	_, err := createProjectVM(context.Background(), ProjectVMCreation{
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

	spec := createdProjectVMConfig(t, client, ProjectVMName(key))
	if spec.Image != legacyRef {
		t.Errorf("created VM image = %q, want the reused legacy ref %q", spec.Image, legacyRef)
	}
	if got := spec.Labels[naming.LabelImage]; got != legacyRef {
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
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})

	client := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	testUI := termio.NewTestMock(t)
	ui := &testUI

	sb, err := createProjectVM(context.Background(), ProjectVMCreation{
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

// TestEffectiveTmpSizeMiBClampsToMemory pins the creation-side clamp: the
// resolved tmp is capped at the effective memory with a warning, so a host-RAM
// clamp cannot leave a RAM-backed tmpfs larger than the VM's memory.
func TestEffectiveTmpSizeMiBClampsToMemory(t *testing.T) {
	ui := &termio.Mock{}
	if got := effectiveTmpSizeMiB("48G", 32*1024, ui); got != 32*1024 {
		t.Errorf("effectiveTmpSizeMiB = %d, want 32768 (clamped to memory)", got)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning when the tmp is clamped to memory")
	}

	ui = &termio.Mock{}
	if got := effectiveTmpSizeMiB("2G", 32*1024, ui); got != 2048 {
		t.Errorf("effectiveTmpSizeMiB = %d, want 2048 (already below memory)", got)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none when the tmp is below memory", ui.WarnCalls)
	}
}

// TestEffectiveTmpSizeMiBAtMemoryDoesNotWarn pins that a tmp equal to the
// effective memory is not clamped (nothing to clamp) and does not warn.
func TestEffectiveTmpSizeMiBAtMemoryDoesNotWarn(t *testing.T) {
	ui := &termio.Mock{}
	if got := effectiveTmpSizeMiB("2048M", 2048, ui); got != 2048 {
		t.Errorf("effectiveTmpSizeMiB = %d, want 2048", got)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("warnings = %v, want none at the memory", ui.WarnCalls)
	}
}

// TestEffectiveTmpSizeMiBDerivesFromEffectiveMemory pins that an unset tmp
// derives from the effective memory, so a host-RAM clamp also lowers the tmp
// default.
func TestEffectiveTmpSizeMiBDerivesFromEffectiveMemory(t *testing.T) {
	ui := &termio.Mock{}
	if got := effectiveTmpSizeMiB("", 2048, ui); got != 1024 {
		t.Errorf("effectiveTmpSizeMiB(\"\", 2048) = %d, want 1024", got)
	}
}

func TestBuildMountsIncludesTmpfsAtTmp(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeCapMiB,
		options.DefaultWorkspaceQuotaMiB,
		nil,
	)

	tmpMount, ok := mounts[tmpMountPath]
	if !ok {
		t.Fatal("expected /tmp mount, not found in mounts map")
	}
	if tmpMount.Kind != msb.MountKindTmpfs {
		t.Errorf("expected /tmp to be a tmpfs mount, got kind %d", tmpMount.Kind)
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
	mounts := buildMounts("test-home-vol", "/repo/path", options.DefaultTmpSizeCapMiB, 32*1024, nil)

	wsMount, ok := mounts[defaultTargetDir]
	if !ok {
		t.Fatal("expected /workspace mount, not found in mounts map")
	}
	if wsMount.Kind != msb.MountKindBind {
		t.Errorf("expected /workspace to be a bind mount, got kind %d", wsMount.Kind)
	}
	if wsMount.QuotaMiB != 32*1024 {
		t.Errorf("expected /workspace quota 32768 MiB, got %d", wsMount.QuotaMiB)
	}
}

func TestBuildMountsSetsWorkspaceMirrorHostPermissions(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeCapMiB,
		options.DefaultWorkspaceQuotaMiB,
		nil,
	)

	wsMount, ok := mounts[defaultTargetDir]
	if !ok {
		t.Fatal("expected /workspace mount, not found in mounts map")
	}
	if wsMount.HostPermissions != msb.HostPermissionsMirror {
		t.Errorf(
			"expected /workspace host permissions %q, got %q",
			msb.HostPermissionsMirror,
			wsMount.HostPermissions,
		)
	}
}

func TestBuildMountsWorkspaceQuotaDefault(t *testing.T) {
	mounts := buildMounts(
		"test-home-vol",
		"/repo/path",
		options.DefaultTmpSizeCapMiB,
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
		options.DefaultTmpSizeCapMiB,
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
	if m2.Kind != msb.MountKindBind {
		t.Errorf("expected a bind mount, got kind %d", m2.Kind)
	}
	if m2.Bind != "/host/home/.m2" {
		t.Errorf("bind source = %q, want /host/home/.m2", m2.Bind)
	}
	if m2.Readonly {
		t.Error("expected .m2 to be writable")
	}
	if m2.HostPermissions != msb.HostPermissionsDefault {
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
		options.DefaultTmpSizeCapMiB,
		options.DefaultWorkspaceQuotaMiB,
		mounts.Mounts{"/home/dev/.m2": {Source: "/host/.m2"}},
	)

	if home, ok := built[mounts.VMHomeDir]; !ok || home.Named != "test-home-vol" {
		t.Errorf("managed home mount altered: %+v", built[mounts.VMHomeDir])
	}
	if ws, ok := built[defaultTargetDir]; !ok || ws.Bind != "/repo/path" {
		t.Errorf("managed workspace mount altered: %+v", built[defaultTargetDir])
	}
	if tmp, ok := built[tmpMountPath]; !ok || tmp.Kind != msb.MountKindTmpfs {
		t.Errorf("managed tmp mount altered: %+v", built[tmpMountPath])
	}
	for _, target := range []string{mounts.VMHomeDir, tmpMountPath} {
		if got := built[target].HostPermissions; got != msb.HostPermissionsDefault {
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

// createdProjectVMConfig returns the spec the named project VM was created
// with. Other CreateSandbox calls (a home-volume prefill) are ignored.
func createdProjectVMConfig(t *testing.T, mock *msb.MockMsbClient, name string) msb.SandboxSpec {
	t.Helper()
	for _, call := range mock.CreatedSandboxCalls {
		if call.Name == name {
			return call.Spec
		}
	}
	t.Fatalf("no CreateSandbox call for %q; calls: %v", name, mock.CreatedSandboxes)
	return msb.SandboxSpec{}
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

// TestBootMaxMemoryMiBIsTheHostRAM pins the boot maximum a project VM is created
// with: the host's installed RAM in whole GiB, never zero on a real host.
func TestBootMaxMemoryMiBIsTheHostRAM(t *testing.T) {
	//testenv:allow asserts the boot maximum is the host's whole-GiB RAM
	want := options.ParseMemoryGiB(uint32(sysinfo.TotalMemoryGiB()))
	if got := BootMaxMemoryMiB(); got == 0 || got != want {
		t.Errorf("BootMaxMemoryMiB = %d, want the host RAM %d MiB", got, want)
	}
}

// TestBootMemoryMiB pins the memory a new project VM boots with: the configured
// value, or the boot maximum with a warning when it asks for more.
func TestBootMemoryMiB(t *testing.T) {
	maxMiB := BootMaxMemoryMiB()
	tests := []struct {
		name     string
		memory   string
		want     uint32
		wantWarn bool
	}{
		{name: "below the maximum", memory: "1G", want: 1024},
		{name: "at the maximum", memory: fmt.Sprintf("%dM", maxMiB), want: maxMiB},
		{name: "above the maximum", memory: fmt.Sprintf("%dM", maxMiB+1), want: maxMiB, wantWarn: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ui := &termio.Mock{}
			if got := bootMemoryMiB(tc.memory, ui); got != tc.want {
				t.Errorf("bootMemoryMiB(%q) = %d, want %d", tc.memory, got, tc.want)
			}
			if warned := len(ui.WarnCalls) > 0; warned != tc.wantWarn {
				t.Errorf("warnings %q, want a warning = %v", ui.WarnCalls, tc.wantWarn)
			}
		})
	}
}
