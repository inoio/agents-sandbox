package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/homeconfig"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// TestCreateProjectVMCreateSandboxError covers the CreateSandbox failure branch
// in createProjectVM.
func TestCreateProjectVMCreateSandboxError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	client := &msb.MockMsbClient{}
	client.CreateSandboxErr = errors.New("create failed")
	testUI := termio.NewTestMock(t)
	ui := &testUI

	_, _, err := createProjectVM(
		context.Background(), client, "opencode-sandbox-vm-test",
		testVMKey(), "opencode-sandbox/runner-test:latest", "test-home-vol", t.TempDir(),
		options.RunOptions{Memory: "1G"}, nil, ui,
	)
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

	_, _, err := createProjectVM(
		context.Background(), client, "opencode-sandbox-vm-test",
		testVMKey(), "opencode-sandbox/runner-test:latest", "test-home-vol", t.TempDir(),
		options.RunOptions{
			Memory:  "1G",
			Network: network.Policy{Profile: network.Profile("bogus-profile")},
		}, nil, ui,
	)
	if err == nil {
		t.Fatal("expected error from the network config")
	}
}

// TestSetUpSandboxDockerdError covers the dockerd-start failure branch in
// setUpSandbox.
func TestSetUpSandboxDockerdError(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		Name_:    "vm",
		FSValue_: msb.NewTestFS(nil, nil),
		ShellErr: errors.New("dockerd binary check failed"),
	}

	configpaths.WithMockConfigPaths(t)
	cfs := &reprovision.ConfigFiles{}
	_, err := setUpSandbox(context.Background(), sb, options.RunOptions{}, cfs, &ui, false, vmBootConnected)
	if err == nil {
		t.Fatal("expected error from dockerd startup")
	}
}

// TestSetUpSandboxEnsureDaemonError covers the ensureDaemon failure branch in
// setUpSandbox: the daemon never becomes healthy and starting it fails.
func TestSetUpSandboxEnsureDaemonError(t *testing.T) {
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return "", 0, errors.New("daemon shell failed")
	})
	defer SetDaemonShellFunc(orig)

	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		Name_:    "vm",
		FSValue_: msb.NewTestFS(nil, nil),
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}

	configpaths.WithMockConfigPaths(t)
	cfs := &reprovision.ConfigFiles{}
	_, err := setUpSandbox(context.Background(), sb, options.RunOptions{}, cfs, &ui, false, vmBootConnected)
	if err == nil {
		t.Fatal("expected error from ensureDaemon")
	}
}

// TestSetUpSandboxProvisionWarn covers the provision-failure warning branch in
// setUpSandbox (config files are provisioned but the write fails; this is
// logged as a warning and does not fail the sandbox setup).
func TestSetUpSandboxProvisionWarn(t *testing.T) {
	orig := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(orig)

	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = errors.New("write denied")
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs}

	ui := termio.NewTestMock(t)
	configpaths.WithMockConfigPaths(t)
	cfs := &reprovision.ConfigFiles{HasSnippets: true, Merged: []byte(`{"model":"x"}`)}
	_, err := setUpSandbox(context.Background(), sb, options.RunOptions{}, cfs, &ui, false, vmBootCreated)
	if err != nil {
		t.Fatalf("setUpSandbox should not fail on provision warning: %v", err)
	}
	if !contains(joinStrings(ui.WarnCalls), "provision failed") {
		t.Errorf("expected a provision warning, got %v", ui.WarnCalls)
	}
}

// TestRunStartupHooksAttachError covers the startup-hook AttachWith failure
// branch in runStartupHooks: a failing hook is logged as a warning and the
// remaining hooks continue to run.
func TestRunStartupHooksAttachError(t *testing.T) {
	sb := &msb.MockSandbox{
		Name_:     "vm",
		AttachErr: errors.New("attach failed"),
	}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", Interpreter: "/bin/sh"},
	}, &ui)

	if !contains(joinStrings(ui.WarnCalls), "startup hook") {
		t.Errorf("expected a startup-hook failure warning, got %v", ui.WarnCalls)
	}
}
