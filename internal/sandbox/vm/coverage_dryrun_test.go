package vm

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	sandboximage "github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestPrepareSandboxDryRunVM covers the dry-run-vm branch of PrepareSandbox:
// the VM lifecycle is skipped and the attach target falls back to the default
// workspace directory. It launches from a checkout root so the target is
// exactly /workspace regardless of where the test binary happens to run.
func TestPrepareSandboxDryRunVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Chdir(testutil.InitRepo(t))

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		return req, nil
	})
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  agentSourceTool,
		},
	)

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{DryRunVM: true}, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox (dry-run-vm): %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	if sess.Sandbox() != nil {
		t.Error("expected a nil sandbox for dry-run-vm")
	}
	if sess.Target() != "/workspace" {
		t.Errorf("expected default target /workspace, got %q", sess.Target())
	}
	if sess.GuestCWD() != "/workspace" {
		t.Errorf("expected guest CWD /workspace, got %q", sess.GuestCWD())
	}
}

// TestPrepareSandboxDryRunVMLaunchSubdirectory covers the guest-CWD derivation:
// when launched from a repository subdirectory the bound /workspace is the
// checkout root and the agent starts in the launched subdirectory. The dry-run
// harness creates no VM, so the /workspace bind source is asserted indirectly
// via resolveBindLayout (see TestResolveBindLayoutOutsideWorktreeWarns).
func TestPrepareSandboxDryRunVMLaunchSubdirectory(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		return req, nil
	})
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	repo := testutil.InitRepo(t)
	sub := filepath.Join(repo, "internal", "foo")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}
	t.Chdir(sub)

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  agentSourceTool,
		},
	)

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{DryRunVM: true}, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox (dry-run-vm, subdirectory): %v", err)
	}
	if sess.Target() != "/workspace/internal/foo" {
		t.Errorf("expected target /workspace/internal/foo, got %q", sess.Target())
	}
	if sess.GuestCWD() != "/workspace/internal/foo" {
		t.Errorf("expected guest CWD /workspace/internal/foo, got %q", sess.GuestCWD())
	}
}

// TestPrepareSandboxVolumeError covers the resolveHomeVolume failure branch of
// PrepareSandbox: a volume-creation failure is propagated.
func TestPrepareSandboxVolumeError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		return req, nil
	})
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
	}
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, errors.New("volume creation failed")
	}
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: git.ProjectSlug(), Agent: "opencode"},
		state.HomeState{AgentVersion: "1.5.0", AgentSource: agentSourceTool},
	)

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{DryRunVM: true}, &ui); err == nil {
		t.Fatal("expected error when the home volume cannot be created")
	}
}

// TestPrepareSandboxEnsureVMError covers the ensureProjectVM failure branch of
// PrepareSandbox: a VM-lookup error is propagated.
func TestPrepareSandboxEnsureVMError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		return req, nil
	})
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, errors.New("lookup failed")
	}
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  agentSourceTool,
		},
	)

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui); err == nil {
		t.Fatal("expected error when the VM lookup fails")
	}
}

// TestPrepareSandboxSetUpError covers the setUpSandbox failure branch of
// PrepareSandbox: a dockerd-startup failure is propagated.
func TestPrepareSandboxSetUpError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		return req, nil
	})
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellErr: errors.New("shell failed")}
	sh := &msb.MockSandboxHandle{
		Name_:     projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  agentSourceTool,
		},
	)

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui); err == nil {
		t.Fatal("expected error when setUpSandbox fails")
	}
}
