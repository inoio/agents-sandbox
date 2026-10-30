package vm

import (
	"context"
	"errors"
	"io"
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
)

// TestPrepareSandboxImageError covers the image-setup failure branch of
// PrepareSandbox: when the opencode version cannot be resolved, the image
// cannot be built and PrepareSandbox returns an error.
func TestPrepareSandboxImageError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "", errors.New("cannot resolve opencode version")
	})

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui); err == nil {
		t.Fatal("expected error when the opencode version cannot be resolved")
	}
}

// TestPrepareSandboxVersionResolutionError covers the branch where resolving
// the opencode version up front fails (before the image is touched): the
// interactive upgrade prompt errors, so PrepareSandbox returns the error
// without attempting to build the image.
func TestPrepareSandboxVersionResolutionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedHomeState(
		state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"},
		state.HomeState{AgentVersion: "1.0.0", AgentSource: agentSourceTool},
	)
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", errors.New("selection failed")
		},
	}

	if _, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, ui); err == nil {
		t.Fatal("expected error when the opencode version prompt fails")
	}
}

// stubPrepareSandboxImage pins agent-version resolution and the Docker
// build/inspect path so PrepareSandbox resolves a runner image with digest
// "sha256:abc123" without the network or a Docker daemon.
func stubPrepareSandboxImage(t *testing.T) {
	t.Helper()

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
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})
}

// existingProjectVMHandle is a running project VM handle backed by an empty
// sandbox, so the reconfig decision reads a live VM without any Docker or KVM.
func existingProjectVMHandle(k state.Key) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Name_:     projectVMName(k),
		Status_:   msbSdk.SandboxStatusRunning,
		Cfg:       &msbSdk.SandboxConfig{},
		ConnectSb: &msb.MockSandbox{Name_: "vm", FSValue_: msb.NewTestFS(nil, nil)},
	}
}

// TestPrepareSandboxReconfigDecisionError covers the resolveReconfigDecision
// error branch of PrepareSandbox: a change that needs a rebuild while another
// client is attached prompts for the rebuild, and answering "quit" aborts the
// startup before any VM work.
func TestPrepareSandboxReconfigDecisionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	stubPrepareSandboxImage(t)

	k := state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"}
	existing := existingProjectVMHandle(k)
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		Volumes:    []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return existing, nil
	}
	msb.WithMsbMock(t, mock)

	// An empty NetworkState counts as changed against the live VM, so the run
	// wants a rebuild; the image digest otherwise matches.
	if err := seedHomeState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.5.0",
		AgentSource:  agentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	// Another attached client turns the rebuild into a prompt; quitting aborts.
	lease, err := state.AcquireClientLease(k)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer lease()

	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "q", nil
	}

	if _, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui); err == nil {
		t.Fatal("expected PrepareSandbox to fail when the rebuild prompt is quit")
	}
}

// TestPrepareSandboxReconfigApplyHomeActionError covers the
// applyReconfigHomeAction error branch of PrepareSandbox: switching to a new
// image while reusing the home volume asks how to handle the volume, and a
// failing migrate aborts the startup.
func TestPrepareSandboxReconfigApplyHomeActionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	stubPrepareSandboxImage(t)

	k := state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"}
	existing := existingProjectVMHandle(k)
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		Volumes:    []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
		CreateVolumeFn: func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return nil, errors.New("create volume failed")
		},
	}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return existing, nil
	}
	msb.WithMsbMock(t, mock)

	// A home-volume digest that differs from the built sha256:abc123 makes the
	// run switch images and ask how to handle the home volume.
	if err := seedHomeState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:old",
		AgentVersion: "1.5.0",
		AgentSource:  agentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	ui.SelectFn = func(prompt string, _ []termio.Choice, _ string) (string, error) {
		if strings.Contains(prompt, "Docker image changed") {
			return "m", nil
		}
		return "", nil
	}

	_, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
	if err == nil {
		t.Fatal("expected PrepareSandbox to fail when the home-volume migrate fails")
	}
	if !strings.Contains(err.Error(), "apply home action") {
		t.Errorf("error = %v, want it to mention the apply home action failure", err)
	}
}
