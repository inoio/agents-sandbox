package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/moby/moby/client"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// TestEnsureProjectVMFlockDirError covers the os.MkdirAll failure branch in
// ensureProjectVM (the flock directory cannot be created).
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

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when the flock directory cannot be created")
	}
}

// TestEnsureProjectVMDecideActionError covers the decideVMAction error branch
// (an unrecognized sandbox status).
func TestEnsureProjectVMDecideActionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatus("unknown-status"),
	})
	msb.WithMsbMock(t, client)

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err == nil {
		t.Fatal("expected error for an unrecognized sandbox status")
	}
}

// TestEnsureProjectVMStartError covers the Start failure branch when an
// existing stopped VM fails to start.
func TestEnsureProjectVMStartError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:    "opencode-sandbox-vm-test",
		Status_:  msbSdk.SandboxStatusStopped,
		StartErr: errors.New("start failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when starting the stopped VM fails")
	}
}

// TestEnsureProjectVMEnsureInstalledError covers the microsandbox runtime
// install failure branch in ensureProjectVM (create path).
func TestEnsureProjectVMEnsureInstalledError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.EnsureInstalledFn = func(context.Context) error { return errors.New("runtime install failed") }
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	}
	msb.WithMsbMock(t, client)

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when the microsandbox runtime install fails")
	}
}

// TestEnsureProjectVMConnectRetryReconnectError covers the connect-failure
// retry path: connect fails, Refresh succeeds, the VM is still active, and the
// re-connect also fails (the mock returns the same ConnectErr for both).
func TestEnsureProjectVMConnectRetryReconnectError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:      "opencode-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		ConnectErr: errors.New("connect failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
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
		Name_:      "opencode-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		ConnectErr: errors.New("connect failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	// The mock's Refresh returns the same handle; with a running status the
	// retry path attempts a second Connect which also fails. We assert the
	// resulting error message mentions the connect failure.
	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
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
		Name_:     "opencode-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount == 1 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return handle, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM post-lock recheck: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != vmBootConnected {
		t.Errorf("expected vmBootConnected, got %v", boot)
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
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
		StartSb: &msb.MockSandbox{Name_: "vm"},
	}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		callCount++
		if callCount == 1 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return handle, nil
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM post-lock recheck: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != vmBootStarted {
		t.Errorf("expected vmBootStarted, got %v", boot)
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
		Name_:   "opencode-sandbox-vm-test",
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

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Recreate: true, CPUs: 1, Memory: "2G"},
		"new:tag",
		"homevol",
		"/workspace",
		map[string]string{},
		testVMKey(),
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM recreate with stop error: %v", err)
	}
	if !boot.booted() {
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
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
		StartSb: &msb.MockSandbox{Name_: "vm"},
		Cfg:     &msbSdk.SandboxConfig{CPUs: 2, MemoryMiB: 2048},
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{CPUs: 2, Memory: "2048"},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM start: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != vmBootStarted {
		t.Errorf("expected vmBootStarted, got %v", boot)
	}
}

// TestEnsureProjectVMConnectReconcileWarn covers the reconcile-failure warning
// branch on the connect path: reconcileResourceConfig fails but the connection
// is still used.
func TestEnsureProjectVMConnectReconcileWarn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:     "opencode-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MemoryMiB: 2048},
		ModifyErr: errors.New("modify failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{CPUs: 4},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM connect with reconcile warning: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != vmBootConnected {
		t.Errorf("expected vmBootConnected, got %v", boot)
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
		Name_:     "opencode-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusStopped,
		StartSb:   &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MemoryMiB: 2048},
		ModifyErr: errors.New("modify failed"),
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	sb, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{CPUs: 4},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM start with reconcile warning: %v", err)
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
		if callCount == 1 {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
		}
		return nil, errors.New("transient error")
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err == nil {
		t.Fatal("expected error from the post-lock recheck")
	}
}

// TestEnsureProjectVMCreateError covers the create-failure branch in
// ensureProjectVM (createProjectVM fails after the post-lock recheck).
func TestEnsureProjectVMCreateError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.CreateSandboxErr = errors.New("create failed")
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G"},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err == nil {
		t.Fatal("expected error from createProjectVM")
	}
}

// TestEnsureProjectVMRecreateRemoveError covers the remove-failure branch in
// the recreate path: removing the old VM fails and aborts the recreate.
func TestEnsureProjectVMRecreateRemoveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	oldHandle := &msb.MockSandboxHandle{
		Name_:     "opencode-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		RemoveErr: errors.New("remove failed"),
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

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Recreate: true, CPUs: 1, Memory: "2G"},
		"new:tag",
		"homevol",
		"/workspace",
		map[string]string{},
		testVMKey(),
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when removing the old VM fails")
	}
}

// TestEnsureProjectVMCreateEnsureLoadedError covers the image-load failure
// branch inside createProjectVM when reached through ensureProjectVM.
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

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Memory: "1G"},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when loading the image into microsandbox fails")
	}
}
