package sandbox

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
)

func TestProjectVMName(t *testing.T) {
	got := projectVMName("myproj-aBc1234D")
	want := "opencode-msb-vm-myproj-aBc1234D"
	if got != want {
		t.Errorf("projectVMName(%q) = %q, want %q", "myproj-aBc1234D", got, want)
	}
}

func TestProjectVMNameTruncation(t *testing.T) {
	longSlug := "p-abcdef-very-long-slug-that-exceeds-the-128-byte-limit-and-then-some-more-padding"
	got := projectVMName(longSlug)
	if len(got) > maxSandboxNameLen {
		t.Errorf("expected name <= %d bytes, got %d", maxSandboxNameLen, len(got))
	}
	if len(got) < len(vmPrefix) {
		t.Errorf("name too short: %q", got)
	}
}

func TestBuildProjectVMEnvIncludesWorkspaces(t *testing.T) {
	envMap := map[string]string{
		"FOO": "bar",
	}
	buildProjectVMEnv(envMap, nil)
	if envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Errorf("expected OPENCODE_EXPERIMENTAL_WORKSPACES=true, got %q", envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"])
	}
	if envMap["PATH"] == "" {
		t.Error("expected PATH to have a fallback value")
	}
}

func TestBuildProjectVMEnvMergesImageEnvs(t *testing.T) {
	imageEnvs := map[string]string{
		"MY_KEY": "my_value",
		"PATH":   "/custom/path",
	}
	envMap := map[string]string{}
	buildProjectVMEnv(envMap, imageEnvs)
	if envMap["MY_KEY"] != "my_value" {
		t.Errorf("expected MY_KEY=my_value from image envs, got %q", envMap["MY_KEY"])
	}
	// Image env PATH should override the fallback PATH that buildProjectVMEnv sets.
	if envMap["PATH"] != "/custom/path" {
		t.Errorf("expected PATH=/custom/path from image envs, got %q", envMap["PATH"])
	}
	if envMap["OPENCODE_EXPERIMENTAL_WORKSPACES"] != "true" {
		t.Error("expected OPENCODE_EXPERIMENTAL_WORKSPACES to be set")
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
	client := &MockMsbClient{}
	testUI := testutil.TermUIMock(t)
	ui := &testUI
	cfg := Config{
		UserStateDir:  t.TempDir(),
		UserConfigDir: t.TempDir(),
	}

	sb, created, err := createProjectVM(
		context.Background(),
		client,
		"opencode-msb-vm-test",
		"opencode-msb/runner-test:latest",
		"test-home-vol",
		t.TempDir(),
		RunOptions{Memory: "1G"},
		cfg,
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
	if client.CreatedSandboxes[0] != "opencode-msb-vm-test" {
		t.Errorf("expected sandbox name %q, got %q", "opencode-msb-vm-test", client.CreatedSandboxes[0])
	}
}

func TestStopProjectVMUsesClient(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI
	client := &MockMsbClient{}
	client.SetGotSandbox(&MockSandboxHandle{
		Name_:   "opencode-msb-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	oldGet := msb.Get
	msb.Get = func() MsbClient { return client }
	defer func() { msb.Get = oldGet }()

	// ProjectSlug depends on the current directory, so use a temp repo.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	if err := StopProjectVM(context.Background(), false, false, ui); err != nil {
		t.Fatalf("StopProjectVM failed: %v", err)
	}
}

func TestEnsureProjectVM_CreatePath(t *testing.T) {
	testUI := testutil.TermUIMock(t)
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

	cfg := Config{
		UserStateDir:  filepath.Join(t.TempDir(), "state"),
		UserConfigDir: t.TempDir(),
	}

	// ProjectSlug depends on the current directory, so use a temp repo.
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := EnsureProjectVM(
		context.Background(),
		RunOptions{Memory: "1G", TmpSize: "512M"},
		cfg,
		"opencode-msb/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (create): %v", err)
	}
	if !created {
		t.Error("expected created=true on create path")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 created sandbox, got %d: %v", len(client.CreatedSandboxes), client.CreatedSandboxes)
	}
}

func TestEnsureProjectVM_ReconnectPath(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI

	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "opencode-msb-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return nil, errors.New("reconnect path must not create a sandbox")
	}
	msb.WithMsbMock(t, client)

	cfg := Config{
		UserStateDir:  filepath.Join(t.TempDir(), "state"),
		UserConfigDir: t.TempDir(),
	}

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := EnsureProjectVM(
		context.Background(),
		RunOptions{Memory: "1G", TmpSize: "512M"},
		cfg,
		"opencode-msb/runner-test:latest",
		"test-home-vol",
		tmpRepo,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (reconnect): %v", err)
	}
	if created {
		t.Error("expected created=false on reconnect path")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 0 {
		t.Fatalf("expected no sandbox created on reconnect, got %v", client.CreatedSandboxes)
	}
}

func TestEnsureProjectVM_ReconnectWhenImageUnchanged(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI

	oldHandle := &msb.MockSandboxHandle{
		Name_:   "opencode-msb-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
		Image_:  "opencode-msb/runner-test:abc123",
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(oldHandle)
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return nil, errors.New("unchanged image must not create a sandbox")
	}
	msb.WithMsbMock(t, client)

	cfg := Config{
		UserStateDir:  filepath.Join(t.TempDir(), "state"),
		UserConfigDir: t.TempDir(),
	}

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := EnsureProjectVM(
		context.Background(),
		RunOptions{Memory: "1G", TmpSize: "512M"},
		cfg,
		"opencode-msb/runner-test:abc123",
		"test-home-vol",
		tmpRepo,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (unchanged image): %v", err)
	}
	if created {
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

func TestEnsureProjectVM_RecreatesWhenImageChangedRunning(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI

	oldHandle := &msb.MockSandboxHandle{
		Name_:   "opencode-msb-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
		Image_:  "opencode-msb/runner-test:oldDigest",
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		if oldHandle.DidRemove() {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return oldHandle, nil
	}
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	msb.WithMsbMock(t, client)

	cfg := Config{
		UserStateDir:  filepath.Join(t.TempDir(), "state"),
		UserConfigDir: t.TempDir(),
	}

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := EnsureProjectVM(
		context.Background(),
		RunOptions{Memory: "1G", TmpSize: "512M"},
		cfg,
		"opencode-msb/runner-test:newDigest",
		"test-home-vol",
		tmpRepo,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (image changed): %v", err)
	}
	if !created {
		t.Error("expected created=true when image changed")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if !oldHandle.DidRemove() {
		t.Error("expected old VM to be removed when image changed")
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 recreated sandbox, got %v", client.CreatedSandboxes)
	}
}

func TestEnsureProjectVM_RecreatesWhenImageChangedStopped(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	ui := &testUI

	oldHandle := &msb.MockSandboxHandle{
		Name_:   "opencode-msb-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
		Image_:  "opencode-msb/runner-test:oldDigest",
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		if oldHandle.DidRemove() {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return oldHandle, nil
	}
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return msb.NewMockSandbox(msb.SandboxOpts{}), nil
	}
	msb.WithMsbMock(t, client)

	cfg := Config{
		UserStateDir:  filepath.Join(t.TempDir(), "state"),
		UserConfigDir: t.TempDir(),
	}

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := EnsureProjectVM(
		context.Background(),
		RunOptions{Memory: "1G", TmpSize: "512M"},
		cfg,
		"opencode-msb/runner-test:newDigest",
		"test-home-vol",
		tmpRepo,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (image changed, stopped): %v", err)
	}
	if !created {
		t.Error("expected created=true when image changed on a stopped VM")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if !oldHandle.DidRemove() {
		t.Error("expected old VM to be removed when image changed")
	}
	if len(client.CreatedSandboxes) != 1 {
		t.Fatalf("expected 1 recreated sandbox, got %v", client.CreatedSandboxes)
	}
}

// applySandboxOpts applies captured functional options to a fresh SandboxConfig
// so tests can assert what createProjectVM configured.
func TestReconcileResourceConfigAppliesCPUsAndMemory(t *testing.T) {
	handle := &MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MemoryMiB: 2048},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
	ctx := context.Background()

	if err := reconcileResourceConfig(ctx, handle, RunOptions{CPUs: 8, Memory: "4G"}); err != nil {
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
	handle := &MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 8, MemoryMiB: 4096},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
	if err := reconcileResourceConfig(context.Background(), handle, RunOptions{CPUs: 8, Memory: "4G"}); err != nil {
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
	client := &MockMsbClient{}
	testUI := testutil.TermUIMock(t)
	ui := &testUI
	cfg := Config{UserStateDir: t.TempDir(), UserConfigDir: t.TempDir()}

	if _, _, err := createProjectVM(
		context.Background(), client, "opencode-msb-vm-test",
		"opencode-msb/runner-test:latest", "test-home-vol", t.TempDir(),
		RunOptions{Memory: "1G", DiskSize: "16G"}, cfg, nil, ui,
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
	testUI := testutil.TermUIMock(t)
	ui := &testUI

	oldHandle := &msb.MockSandboxHandle{
		Name_:   "opencode-msb-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
		Image_:  "",
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(oldHandle)
	client.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		return nil, errors.New("unknown image must not create a sandbox")
	}
	msb.WithMsbMock(t, client)

	cfg := Config{
		UserStateDir:  filepath.Join(t.TempDir(), "state"),
		UserConfigDir: t.TempDir(),
	}

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)

	sb, created, err := EnsureProjectVM(
		context.Background(),
		RunOptions{Memory: "1G", TmpSize: "512M"},
		cfg,
		"opencode-msb/runner-test:newDigest",
		"test-home-vol",
		tmpRepo,
		nil,
		ui,
	)
	if err != nil {
		t.Fatalf("EnsureProjectVM (unknown existing image): %v", err)
	}
	if created {
		t.Error("expected created=false when existing image is unknown")
	}
	if sb == nil {
		t.Fatal("expected non-nil sandbox")
	}
	if len(client.CreatedSandboxes) != 0 {
		t.Fatalf("expected no sandbox created, got %v", client.CreatedSandboxes)
	}
}

func TestNeedsRecreation(t *testing.T) {
	mkConfig := func(cpus uint8, mem uint32, diskMiB uint32, tmpMiB uint32) *msbSdk.SandboxConfig {
		var rootDisk *msbSdk.RootDiskConfig
		if diskMiB > 0 {
			d := msbSdk.RootDisk.Managed(diskMiB)
			rootDisk = &d
		}
		return &msbSdk.SandboxConfig{
			CPUs:      cpus,
			MemoryMiB: mem,
			RootDisk:  rootDisk,
			Volumes: map[string]msbSdk.MountConfig{
				"/tmp": {SizeMiB: tmpMiB},
			},
		}
	}
	mkHandle := func(cfg *msbSdk.SandboxConfig, image string) SandboxHandle {
		return &MockSandboxHandle{Image_: image, Cfg: cfg}
	}

	cases := []struct {
		name   string
		handle SandboxHandle
		image  string
		opts   RunOptions
		want   bool
	}{
		{
			name:   "image change",
			handle: mkHandle(mkConfig(4, 4096, 0, 2048), "image-a"),
			image:  "image-b",
			opts:   RunOptions{},
			want:   true,
		},
		{
			name:   "tmpsize mismatch",
			handle: mkHandle(mkConfig(4, 4096, 0, 2048), "image-a"),
			image:  "image-a",
			opts:   RunOptions{TmpSize: "1G"},
			want:   true,
		},
		{
			name:   "disk mismatch (explicit)",
			handle: mkHandle(mkConfig(4, 4096, 8192, 2048), "image-a"),
			image:  "image-a",
			opts:   RunOptions{DiskSize: "16G"},
			want:   true,
		},
		{
			name:   "disk unset ignores disk",
			handle: mkHandle(mkConfig(4, 4096, 8192, 2048), "image-a"),
			image:  "image-a",
			opts:   RunOptions{},
			want:   false,
		},
		{
			name:   "no change",
			handle: mkHandle(mkConfig(4, 4096, 16384, 2048), "image-a"),
			image:  "image-a",
			opts:   RunOptions{TmpSize: "2G", DiskSize: "16G"},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsRecreation(tc.handle, tc.image, tc.opts); got != tc.want {
				t.Errorf("needsRecreation = %v, want %v", got, tc.want)
			}
		})
	}
}
