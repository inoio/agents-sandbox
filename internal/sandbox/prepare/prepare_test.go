package prepare

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestRuntimeHomeMigrationOfferSeamResolution pins the seam contract: a nil
// options.Seams.RuntimeHomeMigrationOffer uses the production default
// (offerRuntimeHomeMigration), while a set field is used as-is.
func TestRuntimeHomeMigrationOfferSeamResolution(t *testing.T) {
	orig := offerRuntimeHomeMigration
	t.Cleanup(func() { offerRuntimeHomeMigration = orig })

	defaultErr := errors.New("production default offer")
	offerRuntimeHomeMigration = func(context.Context, msb.Client, termio.UI) error { return defaultErr }
	if err := runtimeHomeMigrationOffer(options.Seams{})(
		context.Background(), nil, &termio.Mock{},
	); !errors.Is(err, defaultErr) {
		t.Errorf("nil seam offer error = %v, want the production default %v", err, defaultErr)
	}

	seamErr := errors.New("injected offer")
	seam := func(context.Context, msb.Client, termio.UI) error { return seamErr }
	if err := runtimeHomeMigrationOffer(options.Seams{RuntimeHomeMigrationOffer: seam})(
		context.Background(), nil, &termio.Mock{},
	); !errors.Is(err, seamErr) {
		t.Errorf("set seam offer error = %v, want %v", err, seamErr)
	}
}

// TestPlanReconfigStageWarnsAboutUnprovisionedConfigDirDependencies pins that
// plan-reconfig warns when the host opencode config dir declares dependencies
// the drop-in copy leaves behind (#288).
func TestPlanReconfigStageWarnsAboutUnprovisionedConfigDirDependencies(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	hostHome, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	configDir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatalf("create %s: %v", configDir, err)
	}
	manifest := filepath.Join(configDir, "package.json")
	if err := os.WriteFile(manifest, []byte(`{"dependencies": {"zod": "4.1.8"}}`), 0o600); err != nil {
		t.Fatalf("write %s: %v", manifest, err)
	}
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	msb.WithMsbMock(t, client)
	session := sessionIdentity{key: testVMKey(), agent: opencodeResolved(t)}
	home := preparedHome{
		client:     client,
		volumes:    volume.NewManager(docker.Get()),
		homeVolume: "vol",
		homeState:  state.HomeState{HomeVolume: "vol"},
	}

	if _, err := planReconfigStage(
		context.Background(), configpaths.Get(), session, image.ImageInfo{}, home, false,
		options.RunOptions{Agent: "opencode"}, &ui,
	); err != nil {
		t.Fatalf("planReconfigStage: %v", err)
	}

	warned := false
	for _, warning := range ui.WarnCalls {
		if strings.Contains(warning, manifest) {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning mentions %s; got: %v", manifest, ui.WarnCalls)
	}
}

// TestPlanReconfigStageLiveVMReadError pins the fail-closed lookup through the
// stage: a lookup error aborts plan-reconfig instead of planning against "no VM".
func TestPlanReconfigStageLiveVMReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	lookupErr := errors.New("msb unreachable")
	client := (&msb.MockMsbClient{}).SetGetSandboxErr(lookupErr)
	msb.WithMsbMock(t, client)
	provisionHostConfig := false
	opts := options.RunOptions{Agent: "opencode", ProvisionHostConfig: &provisionHostConfig}
	session := sessionIdentity{key: testVMKey(), agent: opencodeResolved(t)}
	home := preparedHome{client: client, volumes: volume.NewManager(docker.Get()), homeVolume: "vol"}

	_, err := planReconfigStage(
		context.Background(),
		configpaths.Get(),
		session,
		image.ImageInfo{},
		home,
		false,
		opts,
		&ui,
	)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("planReconfigStage error = %v, want %v", err, lookupErr)
	}
}

// TestPlanAndEnsureVMLookUpProjectVMOnce pins the single lookup of #341: the
// handle plan-reconfig read the decision from is the one ensure-vm attaches to.
func TestPlanAndEnsureVMLookUpProjectVMOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	connected := &msb.MockSandbox{}
	handle := &msb.MockSandboxHandle{Name_: "vm", Status_: msb.SandboxStatusRunning, ConnectSb: connected}
	lookups := 0
	client := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		lookups++
		return handle, nil
	}}
	msb.WithMsbMock(t, client)
	provisionHostConfig := false
	opts := options.RunOptions{Agent: "opencode", ProvisionHostConfig: &provisionHostConfig}
	session := sessionIdentity{key: testVMKey(), agent: opencodeResolved(t)}
	home := preparedHome{
		client:     client,
		volumes:    volume.NewManager(docker.Get()),
		homeVolume: "vol",
		homeState:  state.HomeState{HomeVolume: "vol"},
	}

	plan, err := planReconfigStage(
		context.Background(),
		configpaths.Get(),
		session,
		image.ImageInfo{},
		home,
		false,
		opts,
		&ui,
	)
	if err != nil {
		t.Fatalf("planReconfigStage: %v", err)
	}
	sb, boot, err := ensureVMStage(context.Background(), session, image.ImageInfo{}, home, plan, false, opts, &ui)
	if err != nil {
		t.Fatalf("ensureVMStage: %v", err)
	}
	if sb != connected || boot != vm.VMBootConnected {
		t.Errorf("ensureVMStage = (%v, %v), want the running VM connected", sb, boot)
	}
	if lookups != 1 {
		t.Errorf("project VM looked up %d times, want once", lookups)
	}
}

// TestEnsureVMStageAppliesThePlannedMemory pins that ensure-vm applies the
// plan's memory, which carries a raise accepted while planning, rather than the
// run's configured memory.
func TestEnsureVMStageAppliesThePlannedMemory(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:         "vm",
		Status_:       msb.SandboxStatusRunning,
		ConnectSb:     &msb.MockSandbox{},
		Cfg:           &msb.SandboxConfig{MemoryMiB: 4096, MaxMemoryMiB: 16384},
		MetricsResult: &msb.Metrics{MemoryLimitBytes: 8192 << 20},
		Plan:          &msb.SandboxModificationPlan{},
	}
	client := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	msb.WithMsbMock(t, client)

	_, _, err := ensureVMStage(
		context.Background(),
		sessionIdentity{key: testVMKey(), agent: opencodeResolved(t)},
		image.ImageInfo{},
		preparedHome{client: client, homeVolume: "vol"},
		reconfigPlan{existingVM: handle, homeVolume: "vol", limits: plannedLimits{memory: "8G"}},
		false,
		options.RunOptions{Agent: "opencode", Memory: "4G"},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureVMStage: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 || handle.ModifiedOptions[0].MemoryMiB != 8192 {
		t.Errorf("modifications = %+v, want the planned 8192 MiB applied", handle.ModifiedOptions)
	}
}

// TestPlannedLimitsApplyToTheRun pins that ensure-vm uses the limits the plan
// carries, which include raises accepted while planning.
func TestPlannedLimitsApplyToTheRun(t *testing.T) {
	limits := plannedLimits{memory: "8192M", diskSize: "6144M", workspaceQuota: "24576M", tmpSize: "3072M"}
	opts := limits.applyTo(options.RunOptions{Agent: "opencode", Memory: "4G", TmpSize: "2G"})
	if opts.Memory != "8192M" || opts.DiskSize != "6144M" || opts.WorkspaceQuota != "24576M" ||
		opts.TmpSize != "3072M" || opts.Agent != "opencode" {
		t.Errorf("applyTo = %+v, want the planned limits and the rest kept", opts)
	}
	if got := plannedLimitsOf(opts); got != limits {
		t.Errorf("plannedLimitsOf = %+v, want %+v", got, limits)
	}
}

// TestEnsureVMStageFailureSkipsFingerprints pins the ensure-vm error path: a
// failed per-project flock acquisition is returned with no sandbox, and the
// fingerprints are not persisted, since no VM was created.
func TestEnsureVMStageFailureSkipsFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	lockErr := errors.New("flock boom")
	seams := options.Seams{ProjectVMLock: func(options.ProjectVMKey) (func(), error) { return nil, lockErr }}
	k := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	ui := termio.NewTestMock(t)

	sb, _, err := ensureVMStage(
		context.Background(),
		sessionIdentity{key: k},
		image.ImageInfo{},
		preparedHome{client: &msb.MockMsbClient{}},
		reconfigPlan{homeVolume: "home-vol"},
		false,
		options.RunOptions{Seams: seams},
		&ui,
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

// TestResolveSessionIdentityErrorsWhenIdentityUnresolvable pins #354 on the run
// path: a cwd whose git dir cannot be resolved aborts before any VM work.
func TestResolveSessionIdentityErrorsWhenIdentityUnresolvable(t *testing.T) {
	ui := termio.NewTestMock(t)
	_, _, _, err := resolveSessionIdentity(
		context.Background(),
		&msb.MockMsbClient{},
		configpaths.Get(),
		unresolvableIdentityDir(t),
		opencodeResolved(t),
		false,
		&ui,
	)
	if err == nil {
		t.Fatal("resolveSessionIdentity succeeded with an unresolvable identity, want an error")
	}
}

// TestDetermineAgentVersionRunsLegacyUpdaterMigration pins that the version
// decision runs the retired updater.yaml fold: determineAgentVersion is its only
// production caller, so a removed call would silently drop the migration. An
// explicit pin makes resolveBuildVersion return without touching the network or
// the injected docker client.
func TestDetermineAgentVersionRunsLegacyUpdaterMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := determineAgentVersion(
		context.Background(),
		configpaths.Get(),
		&dockertest.MockDockerClient{},
		testVMKey(),
		opencodeResolved(t),
		&termio.Mock{},
		options.RunOptions{AgentVersion: "1.2.3"},
	); err != nil {
		t.Fatalf("determineAgentVersion: %v", err)
	}

	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); !os.IsNotExist(err) {
		t.Fatal("determineAgentVersion must run the legacy updater.yaml migration")
	}
}

func TestBuildOptionsCarriesNoRebuild(t *testing.T) {
	got := buildOptions(options.RunOptions{NoRebuild: true}, "1.0.0", false, false)
	if !got.NoRebuild {
		t.Error("buildOptions dropped NoRebuild")
	}
}

func TestResolveWorkspaceDirResolvesSymlinkInCwd(t *testing.T) {
	realDir, linkDir := symlinkedDir(t)
	t.Chdir(linkDir)

	got, err := resolveWorkspaceDir()
	if err != nil {
		t.Fatalf("resolveWorkspaceDir() error = %v", err)
	}
	if got != realDir {
		t.Errorf("resolveWorkspaceDir() = %q, want %q", got, realDir)
	}
}

// TestResolveWorkspaceDirGetwdError pins that a working directory deleted while
// running is reported as a not-exist error the caller can recognize.
func TestResolveWorkspaceDirGetwdError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove cwd: %v", err)
	}

	if _, err := resolveWorkspaceDir(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("resolveWorkspaceDir error = %v, want fs.ErrNotExist", err)
	}
}

func TestResolveWorkspacePathResolvesSymlink(t *testing.T) {
	realDir, linkDir := symlinkedDir(t)

	got, err := resolveWorkspacePath(linkDir)
	if err != nil {
		t.Fatalf("resolveWorkspacePath() error = %v", err)
	}
	if got != realDir {
		t.Errorf("resolveWorkspacePath() = %q, want %q", got, realDir)
	}
}

// TestResolveWorkspacePathError pins that a missing path is reported as a
// not-exist error the caller can recognize.
func TestResolveWorkspacePathError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	if _, err := resolveWorkspacePath(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("resolveWorkspacePath error = %v, want fs.ErrNotExist", err)
	}
}

func TestGuestWorkspacePath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bindRoot string
		cwd      string
		want     string
	}{
		{"cwd is bind root", "/workspace", "/workspace", "/workspace"},
		{"nested directory", "/repo", "/repo/internal/foo", "/workspace/internal/foo"},
		{"nested worktree root", "/root/wt", "/root/wt", "/workspace"},
		{"rel error falls back to mount path", "relative/bind", "/repo", mounts.WorkspaceMountPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := guestWorkspacePath(tc.bindRoot, tc.cwd); got != tc.want {
				t.Errorf("guestWorkspacePath(%q, %q) = %q, want %q", tc.bindRoot, tc.cwd, got, tc.want)
			}
		})
	}
}

// TestResolveServeHostPort pins the host port a run publishes the agent server
// on: none outside serve-only mode, the port the reconfig plan carried over
// from the existing VM, and otherwise a probed free port that no user port
// mapping claims. A probe range claimed entirely by user mappings fails.
func TestResolveServeHostPort(t *testing.T) {
	serveOnly := options.RunOptions{ServeOnly: true}

	t.Run("not serve-only", func(t *testing.T) {
		got, err := resolveServeHostPort(options.RunOptions{Agent: "opencode"}, 0)
		if err != nil || got != 0 {
			t.Errorf("resolveServeHostPort = %d, %v; want 0, nil", got, err)
		}
	})
	t.Run("planned port is kept", func(t *testing.T) {
		// Not ServeOnlyBasePort, so probing for a free port instead would differ.
		planned := ports.ServeOnlyBasePort + 17
		got, err := resolveServeHostPort(serveOnly, planned)
		if err != nil || got != planned {
			t.Errorf("resolveServeHostPort(planned %d) = %d, %v; want %d, nil", planned, got, err, planned)
		}
	})
	t.Run("no planned port probes one", func(t *testing.T) {
		got, err := resolveServeHostPort(serveOnly, 0)
		if err != nil || got < ports.ServeOnlyBasePort {
			t.Errorf("resolveServeHostPort(planned 0) = %d, %v; want a probed port >= %d",
				got, err, ports.ServeOnlyBasePort)
		}
	})
	t.Run("probe skips user mappings", func(t *testing.T) {
		opts := options.RunOptions{
			ServeOnly: true,
			Ports:     []ports.PortMapping{{HostPort: ports.ServeOnlyBasePort, GuestPort: 3000}},
		}
		got, err := resolveServeHostPort(opts, 0)
		if err != nil || got == ports.ServeOnlyBasePort {
			t.Errorf("resolveServeHostPort = %d, %v; want a port no user mapping claims", got, err)
		}
	})
	t.Run("every probed port claimed", func(t *testing.T) {
		_, err := resolveServeHostPort(options.RunOptions{ServeOnly: true, Ports: exhaustedPortMappings()}, 0)
		if err == nil || !strings.Contains(err.Error(), "no free host port") {
			t.Errorf("error = %v, want no free host port", err)
		}
	})
}

// TestReportActivePorts pins that the published ports are reported when the
// VM is created or recreated, or kept with its ports unchanged, and not when a
// kept VM defers a port change or no ports are requested.
func TestReportActivePorts(t *testing.T) {
	requested := options.RunOptions{Ports: []ports.PortMapping{{HostPort: 3000, GuestPort: 3000}}}
	cases := []struct {
		name      string
		opts      options.RunOptions
		plan      reconfigPlan
		wantLines int
	}{
		{name: "fresh create", opts: requested, wantLines: 1},
		{
			name:      "recreate",
			opts:      requested,
			plan:      reconfigPlan{decision: reconfigDecision{Recreate: true}},
			wantLines: 1,
		},
		{
			name:      "kept with unchanged ports",
			opts:      requested,
			plan:      reconfigPlan{existingVM: &msb.MockSandboxHandle{}},
			wantLines: 1,
		},
		{
			name: "kept with deferred ports",
			opts: requested,
			plan: reconfigPlan{existingVM: &msb.MockSandboxHandle{}, decision: reconfigDecision{PortsChanged: true}},
		},
		{name: "nothing requested"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := &termio.Mock{}
			reportActivePorts(tc.opts, tc.plan, ui)
			if len(ui.InfoCalls) != tc.wantLines {
				t.Errorf("InfoCalls = %v, want %d published-port lines", ui.InfoCalls, tc.wantLines)
			}
		})
	}
}

// TestPrepareHomeVolumeAlwaysOffersRuntimeHomeMigration pins that the hub offers
// the legacy home copy on every prepare path (the --dry-run gate is gone; a
// preview never reaches the hub).
func TestPrepareHomeVolumeAlwaysOffersRuntimeHomeMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
	}

	k := state.Key{Slug: "slug", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "home-vol", ImageDigest: "sha256:abc123"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	offered := false
	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error {
		offered = true
		return nil
	}
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	if _, _, _, err := prepareHomeVolume(
		context.Background(), client, docker.Get(), k, image.ImageInfo{},
		true, /* skipVM */
		state.Fingerprints{},
		&ui,
		offerRuntimeHomeMigration,
	); err != nil {
		t.Fatalf("prepareHomeVolume() error = %v", err)
	}
	if !offered {
		t.Error("the migration offer must run on the prepare path")
	}
}

// TestPrepareHomeVolumeOfferErrorPropagates verifies that a failure from the
// runtime-home migration offer aborts volume setup with a wrapped error.
func TestPrepareHomeVolumeOfferErrorPropagates(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	wantErr := errors.New("offer boom")
	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error {
		return wantErr
	}
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	_, _, _, err := prepareHomeVolume(
		context.Background(),
		&msb.MockMsbClient{},
		docker.Get(),
		k,
		image.ImageInfo{},
		false,
		state.Fingerprints{},
		&ui,
		offerRuntimeHomeMigration,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("prepareHomeVolume() error = %v, want %v", err, wantErr)
	}
}

// TestPrepareHomeVolumeRecoversLostStateFromLiveVM pins home-volume recovery
// after state loss: with no state file but a project VM that still mounts a
// home volume, the existing volume is reused (no duplicate created) and state
// is rewritten to point at it.
func TestPrepareHomeVolumeRecoversLostStateFromLiveVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-slug-20260101T000000"
	k := state.Key{Slug: "slug", Agent: "opencode"}

	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{
				Cfg: &msb.SandboxConfig{Image: "img:tag"},
				Mounts: map[string]msb.MountConfig{
					mounts.VMHomeDir: msb.Mount.Named(recovered, msb.MountOptions{}),
				},
			}, nil
		},
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			if name == recovered {
				return &msb.MockVolumeHandle{Name_: name}, nil
			}
			return nil, fmt.Errorf("volume %s not found", name)
		},
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
			return nil, errors.New("unexpected CreateVolume")
		},
	}
	msb.WithMsbMock(t, mock)

	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error { return nil }
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	volumes, homeVol, homeState, err := prepareHomeVolume(
		context.Background(),
		mock,
		docker.Get(),
		k,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		&ui,
		offerRuntimeHomeMigration,
	)
	if err != nil {
		t.Fatalf("prepareHomeVolume() error = %v", err)
	}
	if homeVol != recovered {
		t.Errorf("home volume = %q, want recovered %q", homeVol, recovered)
	}
	if volumes == nil {
		t.Error("volume manager = nil, want the manager the reconfig steps use")
	}
	if homeState.HomeVolume != recovered {
		t.Errorf("home state volume = %q, want recovered %q", homeState.HomeVolume, recovered)
	}
	persisted, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("state not rewritten: %v", err)
	}
	if persisted.HomeVolume != recovered {
		t.Errorf("persisted home volume = %q, want %q", persisted.HomeVolume, recovered)
	}
}

// TestPrepareHomeVolumeRecoversPersistedAdoptionVolume pins that a home volume
// adoption recorded in the checkout state, but never handed to this run in
// memory, is recovered from the state instead of a fresh one being created.
func TestPrepareHomeVolumeRecoversPersistedAdoptionVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const recovered = "agents-sandbox-home-widget-aaa-opencode-9"
	k := state.Key{Slug: "widget-bbb", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{RecoveredHomeVolume: recovered}); err != nil {
		t.Fatalf("seed adoption-recovered state: %v", err)
	}

	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "legacy VM already destroyed"}
		},
		GetVolumeFn: func(_ context.Context, name string) (msb.VolumeHandle, error) {
			if name == recovered {
				return &msb.MockVolumeHandle{Name_: name}, nil
			}
			return nil, fmt.Errorf("volume %s not found", name)
		},
		CreateVolumeFn: func(_ context.Context, name string, _ msb.VolumeSpec) (msb.VolumeHandle, error) {
			t.Fatalf("CreateVolume must not be called during recovery (got %q)", name)
			return nil, errors.New("unexpected CreateVolume")
		},
	}
	msb.WithMsbMock(t, mock)

	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error { return nil }
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	_, homeVol, _, err := prepareHomeVolume(
		context.Background(),
		mock,
		docker.Get(),
		k,
		image.ImageInfo{Digest: "sha256:def"},
		false,
		state.Fingerprints{},
		&ui,
		offerRuntimeHomeMigration,
	)
	if err != nil {
		t.Fatalf("prepareHomeVolume() error = %v", err)
	}
	if homeVol != recovered {
		t.Errorf("home volume = %q, want the adoption-recovered %q", homeVol, recovered)
	}
}

// TestPersistAgentVersionWarnsOnError verifies that a failure to record the
// baked agent version only warns: recording must never fail a run.
func TestPersistAgentVersionWarnsOnError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	persistAgentVersion(k, "2.0.0", ui)

	if !contains(joinStrings(ui.WarnCalls), "could not persist the agent version") {
		t.Errorf("expected a persist warning, got %v", ui.WarnCalls)
	}
}

func TestPersistOfferedVersionMarksExistingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	persistOfferedVersion(k, "2.0.0", &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !hs.Offered("2.0.0") {
		t.Errorf("offered set not recorded: %+v", hs.OfferedVersions)
	}
}

func TestPersistOfferedVersionSkipsMissingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}

	persistOfferedVersion(k, "2.0.0", &termio.Mock{})

	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
}

func TestPersistOfferedVersionNoVersionIsNoop(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	persistOfferedVersion(k, "", &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if len(hs.OfferedVersions) != 0 {
		t.Errorf("expected no offered versions, got %v", hs.OfferedVersions)
	}
}

func TestPersistOfferedVersionWarnsOnError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	persistOfferedVersion(k, "2.0.0", ui)

	if !contains(joinStrings(ui.WarnCalls), "could not persist project state") {
		t.Errorf("expected a persist warning, got %v", ui.WarnCalls)
	}
}

// TestHomeWritersPreserveUnrelatedFields is a property guard over every HomeState
// writer of the run preparation: each writer owns a small set of fields and must leave every
// other field of the existing document untouched. Each case seeds a fully
// populated state, runs one writer, and compares the result against the seed
// with only the writer's owned fields advanced.
func TestHomeWritersPreserveUnrelatedFields(t *testing.T) {
	seed := func() state.HomeState {
		return state.HomeState{
			HomeVolume:      "vol",
			ImageDigest:     "d",
			AgentVersion:    "1.0.0",
			AgentSource:     state.AgentSourceTool,
			DockerSource:    state.AgentSourceTool,
			OfferedVersions: []string{"1.1.0"},
			EnvState:        state.EnvState{Hash: "e"},
			SecretState:     state.SecretState{Hash: "s"},
			NetworkState:    state.NetworkState{Hash: "n"},
			MountState:      state.MountState{Hash: "m"},
		}
	}

	cases := []struct {
		name string
		run  func(t *testing.T, k state.Key)
		want func() state.HomeState
	}{
		{
			name: "persistFingerprints",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				fingerprints := state.Fingerprints{
					Env:     state.EnvState{Hash: "e2"},
					Secret:  state.SecretState{Hash: "s2"},
					Network: state.NetworkState{Hash: "n2"},
					Mount:   state.MountState{Hash: "m2"},
				}
				if err := persistFingerprints(k, fingerprints); err != nil {
					t.Fatalf("persistFingerprints: %v", err)
				}
			},
			want: func() state.HomeState {
				w := seed()
				w.EnvState = state.EnvState{Hash: "e2"}
				w.SecretState = state.SecretState{Hash: "s2"}
				w.NetworkState = state.NetworkState{Hash: "n2"}
				w.MountState = state.MountState{Hash: "m2"}
				return w
			},
		},
		{
			name: "persistOfferedVersion",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				persistOfferedVersion(k, "2.0.0", &termio.Mock{})
			},
			want: func() state.HomeState {
				w := seed()
				w.OfferedVersions = []string{"1.1.0", "2.0.0"}
				return w
			},
		},
		{
			name: "recoverAgentVersion",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				orig := existingAgentVersion
				existingAgentVersion = func(context.Context, docker.Client, string, string) (string, bool) { return "1.2.3", true }
				t.Cleanup(func() { existingAgentVersion = orig })
				// The recorded baseline disagrees, so the image label wins.
				if got := recoverAgentVersion(
					context.Background(),
					docker.Get(),
					k,
					"0.0.0",
					&termio.Mock{},
				); got != "1.2.3" {
					t.Fatalf("recoverAgentVersion = %q, want 1.2.3", got)
				}
			},
			want: func() state.HomeState {
				w := seed()
				w.AgentVersion = "1.2.3"
				return w
			},
		},
		{
			name: "persistAgentVersion",
			run: func(t *testing.T, k state.Key) {
				t.Helper()
				persistAgentVersion(k, "2.0.0", &termio.Mock{})
			},
			want: func() state.HomeState {
				w := seed()
				w.AgentVersion = "2.0.0"
				return w
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			k := state.Key{Slug: "proj", Agent: "opencode"}
			if err := seedHomeState(k, seed()); err != nil {
				t.Fatalf("seed home state: %v", err)
			}
			tc.run(t, k)
			got, err := state.ReadState(k)
			if err != nil {
				t.Fatalf("ReadState: %v", err)
			}
			if want := tc.want(); !reflect.DeepEqual(*got, want) {
				t.Errorf("writer changed unrelated fields:\n got %+v\nwant %+v", *got, want)
			}
		})
	}
}

func TestLiveHomeVolumeReturnsMountedVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{
				Cfg: &msb.SandboxConfig{Image: "img:tag"},
				Mounts: map[string]msb.MountConfig{
					mounts.VMHomeDir: msb.Mount.Named("home-vol", msb.MountOptions{}),
				},
			}, nil
		},
	}

	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), mock, k, &ui); got != "home-vol" {
		t.Errorf("liveHomeVolume() = %q, want %q", got, "home-vol")
	}
}

func TestLiveHomeVolumeNoVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), &msb.MockMsbClient{}, k, &ui); got != "" {
		t.Errorf("liveHomeVolume() = %q, want empty when no VM exists", got)
	}
}

// TestLiveHomeVolumeMountsError pins that a mounts read failure on an otherwise
// readable VM yields no volume instead of failing the run.
func TestLiveHomeVolumeMountsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{
				Cfg:       &msb.SandboxConfig{Image: "img:tag"},
				MountsErr: errors.New("mounts boom"),
			}, nil
		},
	}

	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), mock, k, &ui); got != "" {
		t.Errorf("liveHomeVolume() = %q, want empty on mounts error", got)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, " "), "home-volume recovery") {
		t.Errorf("expected a verbose mounts-read log, got %v", ui.VerboseCalls)
	}
}

// TestLiveHomeVolumeLookupErrorLogs pins that a transient (non-not-found) VM
// lookup failure is verbose-logged instead of silently falling back to a fresh
// volume, which could orphan the surviving one.
func TestLiveHomeVolumeLookupErrorLogs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return nil, errors.New("lookup boom")
		},
	}

	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), mock, k, &ui); got != "" {
		t.Errorf("liveHomeVolume() = %q, want empty on lookup error", got)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, " "), "home-volume recovery") {
		t.Errorf("expected a verbose lookup-error log, got %v", ui.VerboseCalls)
	}
}

// TestLiveHomeVolumeNilHandle pins that a lookup returning no handle and no
// error yields no volume.
func TestLiveHomeVolumeNilHandle(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := state.Key{Slug: "slug", Agent: "opencode"}
	mock := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return nil, nil //nolint:nilnil // exercises the defensive nil-handle branch
		},
	}

	ui := termio.NewTestMock(t)
	if got := liveHomeVolume(context.Background(), mock, k, &ui); got != "" {
		t.Errorf("liveHomeVolume() = %q, want empty for a nil handle", got)
	}
}

// TestPersistConfigHashesRecordsFingerprints pins that the desired fingerprints
// are persisted after a fresh VM creation.
func TestPersistConfigHashesRecordsFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	slug := "hashproj"
	k := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}}

	persistConfigHashes(
		k,
		state.Fingerprints{
			Network: state.BuildNetworkState(policy),
			Mount:   state.BuildMountState(nil),
		},
		&ui,
	)

	st, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after persistConfigHashes: %v", err)
	}
	if st.NetworkState.Hash == "" {
		t.Error("expected network state hash to be persisted")
	}
	if st.MountState.Hash == "" {
		t.Error("expected mount state hash to be persisted")
	}
}

// TestPersistConfigHashesWarnsOnError pins that a failure to persist the
// fingerprints (a corrupt state file) is a warning, not a run failure.
func TestPersistConfigHashesWarnsOnError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	slug := "corrupthashproj"
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	persistConfigHashes(
		state.Key{Slug: slug, Agent: "opencode"},
		state.Fingerprints{
			Network: state.BuildNetworkState(network.Policy{Profiles: []network.Profile{network.ProfileNone}}),
		},
		&ui,
	)

	if len(ui.WarnCalls) != 1 || !contains(ui.WarnCalls[0], "persisting config fingerprints") {
		t.Errorf("expected one warning about persisting config fingerprints, got %v", ui.WarnCalls)
	}
}

func TestProvisionHostConfig(t *testing.T) {
	truePtr := true
	falsePtr := false
	cases := []struct {
		name string
		opts options.RunOptions
		want bool
	}{
		{name: "nil enables by default", opts: options.RunOptions{Agent: "opencode"}, want: true},
		{name: "explicit true", opts: options.RunOptions{ProvisionHostConfig: &truePtr}, want: true},
		{name: "explicit false", opts: options.RunOptions{ProvisionHostConfig: &falsePtr}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := provisionHostConfig(tc.opts); got != tc.want {
				t.Errorf("provisionHostConfig(%+v) = %v, want %v", tc.opts, got, tc.want)
			}
		})
	}
}

func TestSessionServeHostPort(t *testing.T) {
	s := &Session{serveHostPort: 4096}
	if got := s.ServeHostPort(); got != 4096 {
		t.Errorf("ServeHostPort() = %d, want 4096", got)
	}
}

// TestSessionAccessors pins the Session accessors and that Cleanup detaches
// the sandbox handle, and is a no-op on a session without one.
func TestSessionAccessors(t *testing.T) {
	detached := false
	sb := &msb.MockSandbox{DetachFn: func(context.Context) error {
		detached = true
		return nil
	}}
	s := &Session{sb: sb, name: "vm-name", target: "/tmp/target", cwd: "/workspace", homeVolume: "home-vol"}
	if got := s.HomeVolume(); got != "home-vol" {
		t.Errorf("HomeVolume() = %q, want home-vol", got)
	}
	if got := s.Sandbox(); got != sb {
		t.Errorf("Sandbox() = %v, want the stored sandbox", got)
	}
	if got := s.Target(); got != "/tmp/target" {
		t.Errorf("Target() = %q, want /tmp/target", got)
	}
	s.Cleanup()
	if !detached {
		t.Error("Cleanup() did not detach the sandbox")
	}

	nilSession := &Session{}
	if got := nilSession.Sandbox(); got != nil {
		t.Errorf("Sandbox() on nil VM = %v, want nil", got)
	}
	nilSession.Cleanup()
}

func TestSessionGuestCWD(t *testing.T) {
	s := &Session{guestCWD: "/workspace/sub"}
	if got := s.GuestCWD(); got != "/workspace/sub" {
		t.Errorf("GuestCWD() = %q, want %q", got, "/workspace/sub")
	}
}

func symlinkedDir(t *testing.T) (realDir, linkDir string) {
	t.Helper()
	realDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	linkDir = filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	return realDir, linkDir
}

// TestPlanReconfigStageAppliesAnAcceptedRaiseToThisRun pins that a raise
// accepted at launch applies to the VM this run creates or reconciles, not only
// to the next run: the launch right after an OOM kill would otherwise boot at the
// limit that was just hit, and a recorded disk hit accepted at launch likewise.
// A declined offer keeps the configured limits.
func TestPlanReconfigStageAppliesAnAcceptedRaiseToThisRun(t *testing.T) {
	tests := []struct {
		answer     string
		wantMemory string
		wantTmp    string
	}{
		{answer: "raise", wantMemory: "8192M", wantTmp: "6144M"},
		{answer: "keep", wantMemory: "4G", wantTmp: "2G"},
	}
	for _, tc := range tests {
		t.Run(tc.answer, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			withoutMemoryEnv(t)
			t.Setenv("AGENTS_SANDBOX_TMP_SIZE", "")
			t.Setenv("OPENCODE_SANDBOX_TMP_SIZE", "")
			handle := &msb.MockSandboxHandle{
				Name_:      "vm",
				Status_:    msb.SandboxStatusRunning,
				ConnectSb:  &msb.MockSandbox{},
				Cfg:        &msb.SandboxConfig{MemoryMiB: 4096, MaxMemoryMiB: 16384},
				LogsResult: []msb.LogEntry{memoryRaiseLogEntry(time.Now().Add(-time.Hour))},
			}
			client := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
				return handle, nil
			}}
			msb.WithMsbMock(t, client)
			provisionHostConfig := false
			opts := options.RunOptions{
				Agent: "opencode", Memory: "4G", TmpSize: "2G", ProvisionHostConfig: &provisionHostConfig,
			}
			session := sessionIdentity{key: testVMKey(), agent: opencodeResolved(t)}
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn:            func(string, []termio.Choice, string) (string, error) { return tc.answer, nil },
				InputFn: func(prompt, _ string) (string, error) {
					// A /tmp raise must stay below the memory limit, so it takes
					// a value below the boot maximum; the memory raise is free.
					if strings.Contains(prompt, "/tmp") {
						return "6G", nil
					}
					return "8G", nil
				},
			}
			home := preparedHome{
				client:     client,
				volumes:    volume.NewManager(docker.Get()),
				homeVolume: "vol",
				homeState: state.HomeState{HomeVolume: "vol", DiskPressure: map[string]state.DiskPressureState{
					"tmp": {SeenAt: time.Now().Add(-time.Hour), SizeKiB: 2 << 20, AvailKiB: 100 << 10},
				}},
			}

			plan, err := planReconfigStage(
				context.Background(), configpaths.Get(), session, image.ImageInfo{}, home, false, opts, ui,
			)
			if err != nil {
				t.Fatalf("planReconfigStage: %v", err)
			}
			if plan.limits.memory != tc.wantMemory || plan.limits.tmpSize != tc.wantTmp {
				t.Errorf("plan memory %q tmp %q, want %q and %q for this run",
					plan.limits.memory, plan.limits.tmpSize, tc.wantMemory, tc.wantTmp)
			}
		})
	}
}

// TestPlanReconfigStageSkipVMSuppressesMemoryRaise pins the --dry-run-vm guard:
// a skipVM prepare must not prompt or persist a memory or disk raise, matching the
// deferred home action (specs/resources/resource-limit-proposal.md §1, §8).
func TestPlanReconfigStageSkipVMSuppressesMemoryRaise(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withoutMemoryEnv(t)
	kill := time.Now().Add(-time.Hour)
	handle := &msb.MockSandboxHandle{
		Name_:      "vm",
		Status_:    msb.SandboxStatusRunning,
		ConnectSb:  &msb.MockSandbox{},
		Cfg:        &msb.SandboxConfig{MemoryMiB: 4096, MaxMemoryMiB: 8192},
		LogsResult: []msb.LogEntry{memoryRaiseLogEntry(kill)},
	}
	client := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	msb.WithMsbMock(t, client)
	provisionHostConfig := false
	opts := options.RunOptions{Agent: "opencode", ProvisionHostConfig: &provisionHostConfig}
	session := sessionIdentity{key: testVMKey(), agent: opencodeResolved(t)}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			selects++
			return "keep", nil
		},
		InputFn: func(_, defaultValue string) (string, error) { return defaultValue, nil },
	}
	home := preparedHome{
		client:     client,
		volumes:    volume.NewManager(docker.Get()),
		homeVolume: "vol",
		homeState: state.HomeState{HomeVolume: "vol", DiskPressure: map[string]state.DiskPressureState{
			"tmp": {SeenAt: kill, SizeKiB: 2 << 20, AvailKiB: 100 << 10},
		}},
	}

	if _, err := planReconfigStage(
		context.Background(),
		configpaths.Get(),
		session,
		image.ImageInfo{},
		home,
		true,
		opts,
		ui,
	); err != nil {
		t.Fatalf("skipVM planReconfigStage: %v", err)
	}
	if selects != 0 {
		t.Fatalf("selects=%d on a skipVM prepare, want 0 (a dry run must not prompt)", selects)
	}

	// The same prepare without skipVM offers the recorded hit.
	if _, err := planReconfigStage(
		context.Background(),
		configpaths.Get(),
		session,
		image.ImageInfo{},
		home,
		false,
		opts,
		ui,
	); err != nil {
		t.Fatalf("planReconfigStage: %v", err)
	}
	if selects != 2 {
		t.Fatalf("selects=%d on a real prepare, want 2 (the memory and the /tmp hit)", selects)
	}
}
