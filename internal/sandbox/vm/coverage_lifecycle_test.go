package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
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
		Name_:     "opencode-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "opencode-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: refreshed,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
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
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusStopped,
		StartSb: &msb.MockSandbox{Name_: "vm"},
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "opencode-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: refreshed,
	}
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
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
		Name_:   "opencode-sandbox-vm-test",
		Status_: msbSdk.SandboxStatus("unknown-status"),
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
		t.Fatal("expected error for an unrecognized status in the post-lock recheck")
	}
}

// TestEnsureProjectVMPostLockConnectFailThenStart covers the post-lock recheck
// branch where connecting to a running VM fails and it falls through to Start.
func TestEnsureProjectVMPostLockConnectFailThenStart(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	handle := &msb.MockSandboxHandle{
		Name_:      "opencode-sandbox-vm-test",
		Status_:    msbSdk.SandboxStatusRunning,
		ConnectErr: errors.New("connect failed"),
		StartSb:    &msb.MockSandbox{Name_: "vm"},
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
		t.Fatalf("ensureProjectVM post-lock connect-fail start: %v", err)
	}
	if sb == nil {
		t.Fatal("expected a non-nil sandbox")
	}
	if boot != vmBootStarted {
		t.Errorf("expected vmBootStarted, got %v", boot)
	}
	if !contains(joinStrings(ui.VerboseCalls), "post-lock connect failed") {
		t.Errorf("expected a verbose message about the post-lock connect failure, got %v", ui.VerboseCalls)
	}
}

// TestEnsureProjectVMPostLockStartError covers the post-lock recheck branch
// where starting the stopped VM fails.
func TestEnsureProjectVMPostLockStartError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	callCount := 0
	handle := &msb.MockSandboxHandle{
		Name_:    "opencode-sandbox-vm-test",
		Status_:  msbSdk.SandboxStatusStopped,
		StartErr: errors.New("start failed"),
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
		Name_:     "opencode-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8},
		ModifyErr: errors.New("modify failed"),
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
		options.RunOptions{CPUs: 4},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
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
		Name_:     "opencode-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusStopped,
		StartSb:   &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8},
		ModifyErr: errors.New("modify failed"),
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
		options.RunOptions{CPUs: 4},
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
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
			Name_:   "opencode-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refreshErr: errors.New("refresh failed"),
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
		t.Fatal("expected error when refreshing the handle after a connect failure")
	}
}

// TestEnsureProjectVMConnectRetryReconcileWarn covers the reconcile-failure
// warning after a successful reconnect.
func TestEnsureProjectVMConnectRetryReconcileWarn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	refreshed := &msb.MockSandboxHandle{
		Name_:     "opencode-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8},
		ModifyErr: errors.New("modify failed"),
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "opencode-sandbox-vm-test",
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
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
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
		Name_:    "opencode-sandbox-vm-test",
		Status_:  msbSdk.SandboxStatusStopped,
		StartErr: errors.New("start failed"),
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "opencode-sandbox-vm-test",
			Status_: msbSdk.SandboxStatusRunning,
		},
		refresh: refreshed,
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
		t.Fatal("expected error when starting the VM after a failed connect")
	}
}

// TestEnsureProjectVMConnectRetryStartReconcileWarn covers the reconcile-failure
// warning when the VM is started after a failed connect.
func TestEnsureProjectVMConnectRetryStartReconcileWarn(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	refreshed := &msb.MockSandboxHandle{
		Name_:     "opencode-sandbox-vm-test",
		Status_:   msbSdk.SandboxStatusStopped,
		StartSb:   &msb.MockSandbox{Name_: "vm"},
		Cfg:       &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8},
		ModifyErr: errors.New("modify failed"),
	}
	handle := &retryConnectHandle{
		SandboxHandle: &msb.MockSandboxHandle{
			Name_:   "opencode-sandbox-vm-test",
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
		"img:tag",
		"vol",
		"/workspace",
		nil,
		testVMKey(),
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
