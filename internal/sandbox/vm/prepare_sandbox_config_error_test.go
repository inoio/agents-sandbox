package vm

import (
	"context"
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
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestPrepareSandboxLoadConfigFilesError covers the config-loading failure
// branch of PrepareSandbox: a malformed home manifest aborts setup after the
// home volume is resolved but before any VM is created.
func TestPrepareSandboxLoadConfigFilesError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	// A non-string source value is rejected while reading the home manifest.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .tool/x:\n"+
			"    source: 123\n")

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
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  agentSourceTool,
		},
	)

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui); err == nil {
		t.Fatal("expected error for a malformed home manifest")
	}
}
