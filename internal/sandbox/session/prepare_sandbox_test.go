package session

import (
	"context"
	"io"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	mobyimage "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	sandboximage "github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

// TestPrepareSandboxLoadsHomeYamlOnce verifies that a full startup loads the
// home.yaml manifests exactly once: a missing host source is warned about a
// single time. This guards against the regression where config files were
// loaded twice (once in decideReconfig and once in setUpSandbox), which made
// the warning (and the underlying BuildHomeFiles computation) run twice.
func TestPrepareSandboxLoadsHomeYamlOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// A project home.yaml whose host source does not exist triggers the
	// "does not exist on the host; skipping" warning. The source is a relative
	// path, resolved against the (empty) project config dir, so it cannot exist.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "home.yaml", "missing-file.txt: missing-file.txt\n")

	// Pin the resolved opencode version so image resolution needs no network.
	sandboximage.WithMockOpenCodeVersion(t, "1.0.0")

	// The image is considered up to date: the same version is "latest", so
	// maybePromptOpenCodeUpgrade does not offer a rebuild.
	origUpgradeInfo := openCodeUpgradeInfo
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "1.0.0", nil }
	t.Cleanup(func() { openCodeUpgradeInfo = origUpgradeInfo })

	// Docker build + inspect are mocked so EnsureImage can "build" the runner
	// image and produce a deterministic digest.
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: mobyimage.InspectResponse{
					ID:     "sha256:abc123",
					Config: &dockerspec.DockerOCIImageConfig{},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug(&termio.Mock{})

	// The msb image cache already holds the digest image (ImageGet succeeds),
	// so EnsureImage short-circuits before exporting/loading anything. The
	// image carries the pinned opencode version label.
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     projectVMName(slug),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.0.0"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	// Seed home state so ResolveHomeVolume reuses the existing volume and
	// digest matches (no image-change home-volume prompt).
	state.WriteState(slug, state.HomeState{HomeVolume: "home-vol", ImageDigest: "sha256:abc123"})

	// The opencode daemon is healthy inside the VM, so ensureDaemon returns
	// immediately instead of starting and polling.
	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == "curl -sfm2 "+daemonHealthURL {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	ui := termio.NewTestMock(t)
	sess, err := prepareSandbox(context.Background(), options.RunOptions{}, &ui)
	if err != nil {
		t.Fatalf("prepareSandbox: %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	defer sess.cleanup()

	missingWarnings := 0
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "does not exist on the host; skipping") {
			missingWarnings++
		}
	}
	if missingWarnings != 1 {
		t.Errorf("expected exactly 1 'missing home.yaml source' warning, got %d (warnings: %v)",
			missingWarnings, ui.WarnCalls)
	}
}
