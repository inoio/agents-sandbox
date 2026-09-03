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

	"github.com/inoio/opencode-sandbox/internal/termio"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestProjectVMName(t *testing.T) {
	got := projectVMName("myproj-aBc1234D")
	want := "opencode-sandbox-vm-myproj-aBc1234D"
	if got != want {
		t.Errorf("projectVMName(%q) = %q, want %q", "myproj-aBc1234D", got, want)
	}
}

func TestProjectVMNameTruncation(t *testing.T) {
	longSlug := "p-abcdef-very-long-slug-that-exceeds-the-128-byte-limit-and-then-some-more-padding"
	got := projectVMName(longSlug)
	if len(got) > options.MaxSandboxNameLen {
		t.Errorf("expected name <= %d bytes, got %d", options.MaxSandboxNameLen, len(got))
	}
	if len(got) < len(naming.VmPrefix) {
		t.Errorf("name too short: %q", got)
	}
}

func TestBuildProjectVMEnvMergesImageEnvs(t *testing.T) {
	imageEnvs := map[string]string{
		"MY_KEY":                           "my_value",
		"PATH":                             "/custom/path",
		"OPENCODE_EXPERIMENTAL_WORKSPACES": "true",
	}
	envMap := map[string]string{}
	buildProjectVMEnv(envMap, imageEnvs)
	if envMap["MY_KEY"] != "my_value" {
		t.Errorf("expected MY_KEY=my_value from image envs, got %q", envMap["MY_KEY"])
	}
	// Image env PATH should override any envMap default.
	if envMap["PATH"] != "/custom/path" {
		t.Errorf("expected PATH=/custom/path from image envs, got %q", envMap["PATH"])
	}
	if envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Error("expected OPENCODE_EXPERIMENTAL_WORKSPACES to be passed through from image envs")
	}
}

func TestBuildProjectVMEnvKeepsExistingEntries(t *testing.T) {
	envMap := map[string]string{"FOO": "bar"}
	buildProjectVMEnv(envMap, map[string]string{"BAZ": "qux"})
	if envMap["FOO"] != "bar" {
		t.Errorf("expected FOO=bar preserved, got %q", envMap["FOO"])
	}
	if envMap["BAZ"] != "qux" {
		t.Errorf("expected BAZ=qux from image envs, got %q", envMap["BAZ"])
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

func TestCreateProjectVMCallsClientCreateSandbox(t *testing.T) {
	client := &msb.MockMsbClient{}
	testUI := termio.NewTestMock(t)
	ui := &testUI
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	sb, created, err := createProjectVM(
		context.Background(),
		client,
		"opencode-sandbox-vm-test",
		"test-slug",
		"opencode-sandbox/runner-test:latest",
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{Memory: "1G"},
		nil,
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
	if client.CreatedSandboxes[0] != "opencode-sandbox-vm-test" {
		t.Errorf("expected sandbox name %q, got %q", "opencode-sandbox-vm-test", client.CreatedSandboxes[0])
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
		"opencode-sandbox-vm-test",
		"test-slug",
		"opencode-sandbox/runner-test:abc",
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{Memory: "1G"},
		nil,
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
	if client.LoadedImages[0] != "opencode-sandbox/runner-test:abc" {
		t.Errorf("loaded image ref = %q, want %q", client.LoadedImages[0], "opencode-sandbox/runner-test:abc")
	}
}

func TestStopProjectVMUsesClient(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	oldGet := msb.Get
	msb.Get = func() msb.Client { return client }
	defer func() { msb.Get = oldGet }()

	// ProjectSlug depends on the current directory, so use a temp repo.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), false, false, ui); err != nil {
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

	// ProjectSlug depends on the current directory, so use a temp repo.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		"opencode-sandbox/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
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
		"opencode-sandbox/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
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
		Name_:   "opencode-sandbox-vm-test",
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
		"opencode-sandbox/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
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
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if _, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		"opencode-sandbox/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
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
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
	})
	msb.WithMsbMock(t, client)
	configpaths.WithMockConfigPaths(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if _, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G", TmpSize: "512M"},
		"opencode-sandbox/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
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
		"opencode-sandbox/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
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
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
		Image_:  "opencode-sandbox/runner-test:abc123",
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
		"opencode-sandbox/runner-test:abc123",
		"test-home-vol",
		tmpRepo,
		nil,
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
	if oldHandle.DidRemove() {
		t.Error("expected existing VM not to be removed when image is unchanged")
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
		context.Background(), client, "opencode-sandbox-vm-test",
		"test-slug",
		"opencode-sandbox/runner-test:latest", "test-home-vol", t.TempDir(),
		options.RunOptions{Memory: "1G", DiskSize: "16G"}, nil, ui,
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
		Name_:   "opencode-sandbox-vm-test",
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
		"opencode-sandbox/runner-test:newDigest",
		"test-home-vol",
		tmpRepo,
		nil,
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
		Name_:   "opencode-sandbox-vm-test",
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
		context.Background(), opts,
		"new:tag", "homevol", "/workspace",
		map[string]string{}, ui,
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
	if !oldHandle.DidRemove() {
		t.Error("expected old VM handle.Remove to be called")
	}
}

func TestEnsureProjectVMReusesWhenNotFlagged(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI

	handle := &msb.MockSandboxHandle{
		Name_:   "opencode-sandbox-vm-test",
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
		context.Background(), options.RunOptions{},
		"old:tag", "homevol", "/workspace",
		map[string]string{}, ui,
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
	if handle.DidRemove() {
		t.Error("did not expect Remove on reuse")
	}
}

func TestProjectPortBindingsServeOnly(t *testing.T) {
	canonical := options.ServeOnlyBindings()
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
			got := projectPortBindings(tt.serveOnly)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("projectPortBindings(%v) = %#v, want %#v", tt.serveOnly, got, tt.want)
			}
		})
	}
}

func TestProjectPortBindingsDelegatesToOptions(t *testing.T) {
	got := projectPortBindings(true)
	expected := options.ServeOnlyBindings()
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
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	oldGet := msb.Get
	msb.Get = func() msb.Client { return client }
	defer func() { msb.Get = oldGet }()

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := KillProjectVM(context.Background(), false, false, ui); err != nil {
		t.Fatalf("KillProjectVM failed: %v", err)
	}
}

func TestStopProjectVMAlreadyStopped(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
	})
	msb.WithMsbMock(t, client)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), false, false, ui); err != nil {
		t.Fatalf("StopProjectVM on already-stopped VM failed: %v", err)
	}
}

func TestStopProjectVMDryRunRemove(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	msb.WithMsbMock(t, client)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), true, true, ui); err != nil {
		t.Fatalf("StopProjectVM dry-run with remove failed: %v", err)
	}
}

func TestStopProjectVMNotFound(t *testing.T) {
	testUI := termio.NewTestMock(t)
	ui := &testUI
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	}
	msb.WithMsbMock(t, client)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), false, false, ui); err != nil {
		t.Fatalf("StopProjectVM on missing VM failed: %v", err)
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
	s := &Session{sb: sb, name: "vm-name", target: "/tmp/target", cwd: "/workspace"}
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
