package vm

import (
	"context"
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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

	_, _, err := createProjectVM(
		context.Background(),
		client,
		projectVMName(key),
		key,
		key.Slug,
		image.ImageInfo{
			DockerTag: legacyRef,
			Ref:       naming.RunnerImageRef(key.Slug, key.Agent),
			Digest:    "sha256:same",
		},
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{Memory: "1G"},
		envsecret.EnvSecrets{},
		ui,
	)
	if err != nil {
		t.Fatalf("createProjectVM: %v", err)
	}

	var cfg msbSdk.SandboxConfig
	for _, call := range client.CreatedSandboxCalls {
		if call.Name != projectVMName(key) {
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
}

// TestCreateProjectVMIdleTimeout pins the single default: an unset
// RunOptions.IdleTimeout takes options.DefaultVMIdleTimeout, while a configured
// value is passed through unchanged.
func TestCreateProjectVMIdleTimeout(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	tests := []struct {
		name string
		opts time.Duration
		want time.Duration
	}{
		{name: "unset uses the options default", opts: 0, want: options.DefaultVMIdleTimeout},
		{name: "configured value is used as-is", opts: 45 * time.Second, want: 45 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := testVMKey()
			client := &msb.MockMsbClient{}
			testUI := termio.NewTestMock(t)
			ui := &testUI

			_, _, err := createProjectVM(
				context.Background(),
				client,
				projectVMName(key),
				key,
				key.Slug,
				image.ImageInfo{
					DockerTag: "agents-sandbox/runner-test:latest",
					Ref:       "agents-sandbox/runner-test:latest",
				},
				"test-home-vol",
				t.TempDir(),
				options.RunOptions{Memory: "1G", IdleTimeout: tc.opts},
				envsecret.EnvSecrets{},
				ui,
			)
			if err != nil {
				t.Fatalf("createProjectVM: %v", err)
			}
			cfg := createdProjectVMConfig(t, client, projectVMName(key))
			if cfg.IdleTimeout != tc.want {
				t.Errorf("IdleTimeout = %v, want %v", cfg.IdleTimeout, tc.want)
			}
		})
	}
}

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
		context.Background(),
		client,
		"agents-sandbox-vm-test",
		testVMKey(),
		testVMKey().Slug,
		image.ImageInfo{DockerTag: "agents-sandbox/runner-test:latest", Ref: "agents-sandbox/runner-test:latest"},
		"test-home-vol",
		t.TempDir(),
		options.RunOptions{
			Memory:  "1G",
			Network: network.Policy{Profiles: []network.Profile{network.Profile("bogus-profile")}},
		},
		envsecret.EnvSecrets{},
		ui,
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
	_, err := setUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		options.RunOptions{Agent: "opencode"},
		cfs,
		&ui,
		false,
		vmBootConnected,
		mounts.WorkspaceMountPath,
	)
	if err == nil {
		t.Fatal("expected error from dockerd startup")
	}
}

// TestSetUpSandboxEnsureDaemonError covers the ensureDaemon failure branch in
// setUpSandbox: the daemon never becomes healthy and starting it fails.
func TestSetUpSandboxEnsureDaemonError(t *testing.T) {
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return "", 0, errors.New("daemon shell failed")
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		Name_:    "vm",
		FSValue_: msb.NewTestFS(nil, nil),
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
		ExecDetachedFn: func(context.Context, string, []string, ...msbSdk.JobExecOption) (msb.Job, error) {
			return nil, errors.New("daemon start failed")
		},
	}

	configpaths.WithMockConfigPaths(t)
	cfs := &reprovision.ConfigFiles{}
	_, err := setUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		options.RunOptions{Agent: "opencode"},
		cfs,
		&ui,
		false,
		vmBootConnected,
		mounts.WorkspaceMountPath,
	)
	if err == nil {
		t.Fatal("expected error from ensureDaemon")
	}
}

// TestSetUpSandboxProvisionWarn covers the provision-failure warning branch in
// setUpSandbox (config files are provisioned but the write fails; this is
// logged as a warning and does not fail the sandbox setup).
func TestSetUpSandboxProvisionWarn(t *testing.T) {
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = errors.New("write denied")
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs}

	ui := termio.NewTestMock(t)
	configpaths.WithMockConfigPaths(t)
	cfs := &reprovision.ConfigFiles{HasSnippets: true, Merged: []byte(`{"model":"x"}`)}
	_, err := setUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		options.RunOptions{Agent: "opencode"},
		cfs,
		&ui,
		false,
		vmBootCreated,
		mounts.WorkspaceMountPath,
	)
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
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", HasShebang: true},
	}, &ui)

	if !contains(joinStrings(ui.WarnCalls), "startup hook") {
		t.Errorf("expected a startup-hook failure warning, got %v", ui.WarnCalls)
	}
}
