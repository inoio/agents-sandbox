package vm

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/termio"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestBuildProjectVMEnvMergesImageEnvs(t *testing.T) {
	imageEnvs := map[string]string{
		"MY_KEY":                           "my_value",
		"PATH":                             "/custom/path",
		"OPENCODE_EXPERIMENTAL_WORKSPACES": "true",
	}
	env := buildProjectVMEnv(map[string]string{}, imageEnvs)
	if env["MY_KEY"] != "my_value" {
		t.Errorf("expected MY_KEY=my_value from image envs, got %q", env["MY_KEY"])
	}
	// Image env PATH should override any host env default.
	if env["PATH"] != "/custom/path" {
		t.Errorf("expected PATH=/custom/path from image envs, got %q", env["PATH"])
	}
	if env["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Error("expected OPENCODE_EXPERIMENTAL_WORKSPACES to be passed through from image envs")
	}
}

func TestBuildProjectVMEnvKeepsExistingEntries(t *testing.T) {
	hostEnv := map[string]string{"FOO": "bar"}
	env := buildProjectVMEnv(hostEnv, map[string]string{"BAZ": "qux"})
	if env["FOO"] != "bar" {
		t.Errorf("expected FOO=bar preserved, got %q", env["FOO"])
	}
	if env["BAZ"] != "qux" {
		t.Errorf("expected BAZ=qux from image envs, got %q", env["BAZ"])
	}
	if len(hostEnv) != 1 {
		t.Errorf("expected the host env to be left unmodified, got %v", hostEnv)
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

func TestCreateProjectVMCallsClientCreateSandbox(t *testing.T) {
	client := &msb.MockMsbClient{}
	testUI := termio.NewTestMock(t)
	ui := &testUI
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	sb, created, err := createProjectVM(
		context.Background(),
		client,
		"agents-sandbox-vm-test",
		testVMKey(),
		testVMKey().Slug,
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{Memory: "1G"},
		envsecret.EnvSecrets{},
		ui,
	)
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

	sb, created, err := createProjectVM(
		context.Background(),
		client,
		"agents-sandbox-vm-test",
		testVMKey(),
		testVMKey().Slug,
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:abc", Ref: "agents-sandbox/runner-test:abc"},
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{Memory: "1G"},
		envsecret.EnvSecrets{},
		ui,
	)
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

func TestStopProjectVMUsesClient(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	oldGet := msb.Get
	msb.Get = func() msb.Client { return client }
	defer func() { msb.Get = oldGet }()

	// CheckoutSlug depends on the current directory, so use a temp repo.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), false, false, ui, "opencode"); err != nil {
		t.Fatalf("StopProjectVM failed: %v", err)
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

	sb, created, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		"test-home-vol",
		tmpRepo,
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (create): %v", err)
	}
	if !created.booted() {
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

	sb, created, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		"test-home-vol",
		tmpRepo,
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM (create): %v", err)
	}
	if !created.booted() || sb == nil {
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

	sb, created, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		"test-home-vol",
		tmpRepo,
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (reconnect): %v", err)
	}
	if created.booted() {
		t.Error("expected created=false on reconnect path")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 0 {
		t.Fatalf("expected no sandbox created on reconnect, got %v", client.CreatedSandboxes)
	}
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

	if _, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		"test-home-vol",
		tmpRepo,
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	); err != nil {
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

	if _, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		"test-home-vol",
		tmpRepo,
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	); err != nil {
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

	if _, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		"test-home-vol",
		tmpRepo,
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	); err != nil {
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

	sb, created, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:abc123", Ref: "agents-sandbox/runner-test:abc123"},
		"test-home-vol",
		tmpRepo,
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (unchanged image): %v", err)
	}
	if created.booted() {
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

// TestReconcileResourceConfigClampsCpusToMax verifies that CPU/memory requests
// above the boot-time maximum are clamped (not rejected).
func TestReconcileResourceConfigClampsCpusToMax(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8, MemoryMiB: 4096, MaxMemoryMiB: 8192},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
	ui := termio.NewTestMock(t)
	err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{CPUs: 16, Memory: "4G"}, &ui)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.CPUs != 8 {
		t.Errorf("expected clamped CPUs=8, got %d", mo.CPUs)
	}
	if mo.MemoryMiB != 0 {
		t.Errorf("expected no memory change (already 4G), got %d", mo.MemoryMiB)
	}
}

// applySandboxOpts applies captured functional options to a fresh SandboxConfig
// so tests can assert what createProjectVM configured.
func TestReconcileResourceConfigAppliesCPUsAndMemory(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MemoryMiB: 2048},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
	ctx := context.Background()
	ui := termio.NewTestMock(t)
	if err := reconcileResourceConfig(ctx, handle, options.RunOptions{CPUs: 8, Memory: "4G"}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig failed: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.CPUs != 8 || mo.MemoryMiB != 4096 {
		t.Errorf("ModifyOptions = CPUs=%d Mem=%d, want 8 / 4096", mo.CPUs, mo.MemoryMiB)
	}
}

func TestReconcileResourceConfigNoopWhenSame(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 8, MemoryMiB: 4096},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
	ui := termio.NewTestMock(t)
	if err := reconcileResourceConfig(
		context.Background(),
		handle,
		options.RunOptions{CPUs: 8, Memory: "4G"},
		&ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(handle.ModifiedOptions) != 0 {
		t.Errorf("expected no Modify call, got %d", len(handle.ModifiedOptions))
	}
}

func applySandboxOpts(cfg *msbSdk.SandboxConfig, opts []msbSdk.SandboxOption) {
	for _, o := range opts {
		o(cfg)
	}
}

func TestCreateProjectVMAppliesRootDiskWhenDiskSizeSet(t *testing.T) {
	client := &msb.MockMsbClient{}
	testUI := termio.NewTestMock(t)
	ui := &testUI
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	if _, _, err := createProjectVM(
		context.Background(),
		client,
		"agents-sandbox-vm-test",
		testVMKey(),
		testVMKey().Slug,
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{Memory: "1G", DiskSize: "16G"},
		envsecret.EnvSecrets{},
		ui,
	); err != nil {
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

	sb, created, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:newDigest", Ref: "agents-sandbox/runner-test:newDigest"},
		"test-home-vol",
		tmpRepo,
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (unknown existing image): %v", err)
	}
	if created.booted() {
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
	sb, created, err := ensureProjectVM(
		context.Background(),
		opts,
		image.ImageInfo{DockerTag: "new:tag", Ref: "new:tag"},
		"homevol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if !created.booted() {
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

	sb, created, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "old:tag", Ref: "old:tag"},
		"homevol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM: %v", err)
	}
	if created.booted() {
		t.Error("expected created=false on reuse")
	}
	if sb == nil {
		t.Error("expected a sandbox on reuse")
	}
	if handle.DidDestroy() {
		t.Error("did not expect Destroy on reuse")
	}
}

func TestProjectPortBindingsServeOnly(t *testing.T) {
	canonical := options.ServeOnlyBindings(options.ServeOnlyBasePort)
	tests := []struct {
		name      string
		serveOnly bool
		want      []msbSdk.PortBinding
	}{
		{"normal run", false, nil},
		{"serve only", true, canonical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := projectPortBindings(tt.serveOnly, options.ServeOnlyBasePort)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("projectPortBindings(%v) = %#v, want %#v", tt.serveOnly, got, tt.want)
			}
		})
	}
}

func TestProjectPortBindingsDelegatesToOptions(t *testing.T) {
	got := projectPortBindings(true, 4097)
	expected := options.ServeOnlyBindings(4097)
	if len(got) != len(expected) {
		t.Fatalf("expected %d bindings, got %d", len(expected), len(got))
	}
	if got[0].Bind != expected[0].Bind || got[0].HostPort != expected[0].HostPort ||
		got[0].GuestPort != expected[0].GuestPort || got[0].Protocol != expected[0].Protocol {
		t.Errorf("binding = %#v, want %#v", got[0], expected[0])
	}
}

func TestExitError(t *testing.T) {
	e := &ExitError{Code: 42}
	if got := e.Error(); got != "exit code 42" {
		t.Errorf("ExitError.Error() = %q, want %q", got, "exit code 42")
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
	oldGet := msb.Get
	msb.Get = func() msb.Client { return client }
	defer func() { msb.Get = oldGet }()

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := KillProjectVM(context.Background(), false, false, ui, "opencode"); err != nil {
		t.Fatalf("KillProjectVM failed: %v", err)
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

	if err := StopProjectVM(context.Background(), false, false, ui, "opencode"); err != nil {
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

	if err := StopProjectVM(context.Background(), true, true, ui, "opencode"); err != nil {
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

	if err := StopProjectVM(context.Background(), false, false, ui, "opencode"); err != nil {
		t.Fatalf("StopProjectVM on missing VM failed: %v", err)
	}
	if !strings.Contains(joinStrings(ui.InfoCalls), "no project VM found") {
		t.Errorf("expected the missing-VM note, got %v", ui.InfoCalls)
	}
}

func TestSummarizeConflicts(t *testing.T) {
	cs := []msbSdk.ModificationConflict{
		{Field: "cpus", Message: "too high"},
		{Field: "memory", Message: "not enough"},
	}
	if got := summarizeConflicts(cs); got != "cpus: too high; memory: not enough" {
		t.Errorf("summarizeConflicts() = %q, want joined string", got)
	}
	if got := summarizeConflicts(nil); got != "" {
		t.Errorf("summarizeConflicts(nil) = %q, want empty", got)
	}
}

func TestReconcileResourceConfig_AppliesCPUsAndMemory(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8, MemoryMiB: 2048, MaxMemoryMiB: 8192},
		Plan: &msbSdk.SandboxModificationPlan{},
	}
	ui := termio.NewTestMock(t)

	opts := options.RunOptions{CPUs: 4, Memory: "4G"}
	if err := reconcileResourceConfig(context.Background(), handle, opts, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig() error = %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.CPUs != 4 || mo.MemoryMiB != 4096 {
		t.Errorf("Modify options = %+v, want CPUs=4 MemoryMiB=4096", mo)
	}
	if mo.Policy != msbSdk.ModificationPolicyNoRestart {
		t.Errorf("Modify policy = %v, want NoRestart", mo.Policy)
	}
}

func TestReconcileResourceConfig_ClampsToBootMax(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 4, MemoryMiB: 2048, MaxMemoryMiB: 4096},
		Plan: &msbSdk.SandboxModificationPlan{},
	}
	ui := termio.NewTestMock(t)

	opts := options.RunOptions{CPUs: 16, Memory: "16G"}
	if err := reconcileResourceConfig(context.Background(), handle, opts, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig() error = %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.CPUs != 4 || mo.MemoryMiB != 4096 {
		t.Errorf("Modify options = %+v, want clamped CPUs=4 MemoryMiB=4096", mo)
	}
}

func TestReconcileResourceConfig_NoChangeSkipsModify(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8, MemoryMiB: 2048, MaxMemoryMiB: 8192},
	}
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(
		context.Background(),
		handle,
		options.RunOptions{CPUs: 2, Memory: "2048"},
		&ui,
	); err != nil {
		t.Fatalf("reconcileResourceConfig() error = %v", err)
	}
	if len(handle.ModifiedOptions) != 0 {
		t.Errorf("expected no Modify call when nothing changes, got %d", len(handle.ModifiedOptions))
	}
}

func TestReconcileResourceConfig_ConflictsError(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8, MemoryMiB: 2048, MaxMemoryMiB: 8192},
		Plan: &msbSdk.SandboxModificationPlan{
			Conflicts: []msbSdk.ModificationConflict{{Field: "cpus", Message: "conflict"}},
		},
	}
	ui := termio.NewTestMock(t)

	err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{CPUs: 4}, &ui)
	if err == nil {
		t.Fatal("expected error when plan has conflicts")
	}
}

func TestSessionAccessors(t *testing.T) {
	sb := &msb.MockSandbox{}
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

	nilSession := &Session{}
	if got := nilSession.Sandbox(); got != nil {
		t.Errorf("Sandbox() on nil VM = %v, want nil", got)
	}
	nilSession.Cleanup()
}
