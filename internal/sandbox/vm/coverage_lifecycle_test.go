package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// retryConnectHandle is a SandboxHandle whose Connect fails on the first call
// and then delegates to a refreshed handle on subsequent calls, modelling the
// idle-timeout race ensureProjectVM recovers from.
type retryConnectHandle struct {
	msb.SandboxHandle

	connects   int
	refresh    msb.SandboxHandle
	refreshErr error
}

func (h *retryConnectHandle) Connect(ctx context.Context) (msb.Sandbox, error) {
	h.connects++
	if h.connects == 1 {
		return nil, errors.New("connect failed")
	}
	return h.refresh.Connect(ctx)
}

func (h *retryConnectHandle) Refresh(context.Context) (msb.SandboxHandle, error) {
	if h.refreshErr != nil {
		return nil, h.refreshErr
	}
	return h.refresh, nil
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

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM connect-retry reconnect: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != vmBootConnected {
		t.Errorf("expected vmBootConnected, got %v", boot)
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

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM connect-retry start: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != vmBootStarted {
		t.Errorf("expected vmBootStarted, got %v", boot)
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

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err == nil {
		t.Fatal("expected error for an unrecognized status in the post-lock recheck")
	}
}

// postLockGetSandbox returns a GetSandboxFn that misses the two lookups
// findProjectVM makes (the current and legacy name) and answers the post-lock
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

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM post-lock connect-fail start: %v", err)
	}
	if sb != started {
		t.Fatalf("sandbox = %v, want the started sandbox", sb)
	}
	if boot != vmBootStarted {
		t.Errorf("expected vmBootStarted, got %v", boot)
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

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM post-lock connect retry: %v", err)
	}
	if sb != connected {
		t.Fatalf("sandbox = %v, want the reconnected sandbox", sb)
	}
	if boot != vmBootConnected {
		t.Errorf("expected vmBootConnected, got %v", boot)
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

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when refreshing the handle after a post-lock connect failure")
	}
}

// TestEnsureProjectVMPostLockStartError covers the post-lock recheck branch
// where starting the stopped VM fails.
func TestEnsureProjectVMPostLockStartError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	handle := &msb.MockSandboxHandle{
		Name_:    "agents-sandbox-vm-test",
		Status_:  msbSdk.SandboxStatusStopped,
		StartErr: errors.New("start failed"),
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

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when the post-lock start fails")
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

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{CPUs: 4},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM post-lock connect reconcile warn: %v", err)
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

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{CPUs: 4},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM post-lock start reconcile warn: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != vmBootStarted {
		t.Errorf("expected vmBootStarted, got %v", boot)
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

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
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

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{CPUs: 4},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM connect-retry reconcile warn: %v", err)
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

// TestEnsureProjectVMConnectRetryStartError covers the branch where, after a
// connect failure and refresh to a non-active VM, starting it fails.
func TestEnsureProjectVMConnectRetryStartError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	refreshed := &msb.MockSandboxHandle{
		Name_:    "agents-sandbox-vm-test",
		Status_:  msbSdk.SandboxStatusStopped,
		StartErr: errors.New("start failed"),
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

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err == nil {
		t.Fatal("expected error when starting the VM after a failed connect")
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

	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{CPUs: 4},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM connect-retry start reconcile warn: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != vmBootStarted {
		t.Errorf("expected vmBootStarted, got %v", boot)
	}
	if !contains(joinStrings(ui.WarnCalls), "could not reconcile VM resources") {
		t.Errorf("expected a reconcile warning, got %v", ui.WarnCalls)
	}
}
