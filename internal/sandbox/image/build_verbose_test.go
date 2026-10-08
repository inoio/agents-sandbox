package image

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func verboseContains(ui *termio.Mock, substr string) bool {
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, substr) {
			return true
		}
	}
	return false
}

func TestEnsureImageVerboseNamesChangedInputs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "2.0.0")
	a := agentOpencode(t)
	oldLabels := computeImageIdentity(nil, "1.0.0", false).values()
	oldLabels[dockerfileIDLabelKey] = "stale"
	built := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, ref string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: oldLabels},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			built = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	ui := &termio.Mock{}
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, ui); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !built {
		t.Fatal("expected a build when the identity changed")
	}
	if !verboseContains(ui, "rebuilding runner image") || !verboseContains(ui, "agent version changed") {
		t.Errorf("verbose calls = %v, want the rebuild reason", ui.VerboseCalls)
	}
}

func TestEnsureImageVerboseReportsReuse(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(RenderDockerfile(a, nil, false), "1.2.3"),
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, ref string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels},
			}, nil
		},
	})
	ui := &termio.Mock{}
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, ui); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !verboseContains(ui, "reusing runner image") {
		t.Errorf("verbose calls = %v, want a reuse line", ui.VerboseCalls)
	}
}

func TestEnsureImageVerboseReportsForcedRebuild(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, _ string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:base"}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	ui := &termio.Mock{}
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{Force: true}, ui); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !verboseContains(ui, "forced by --rebuild") {
		t.Errorf("verbose calls = %v, want a forced-rebuild reason", ui.VerboseCalls)
	}
}

func TestEnsureLoadedVerboseReportsUpToDate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, _ string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:same", Config: dockerConfigWith("", nil)}, nil
		},
	})
	ui := &termio.Mock{}
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}
	if err := EnsureLoaded(context.Background(), msbClient, "test-project", rTag, ui); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "is up to date") {
		t.Errorf("verbose calls = %v, want an up-to-date line", ui.VerboseCalls)
	}
}

func TestEnsureLoadedVerboseReportsNotPresent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	ui := &termio.Mock{}
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	if err := EnsureLoaded(
		context.Background(), msbClient, "test-project", "agents-sandbox/runner-p:opencode", ui,
	); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "not present in microsandbox cache") {
		t.Errorf("verbose calls = %v, want a not-present reason", ui.VerboseCalls)
	}
}

func TestEnsureLoadedVerboseReportsStaleCache(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, _ string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:docker-new", Config: dockerConfigWith("", nil)}, nil
		},
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})
	ui := &termio.Mock{}
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:msb-old"}, nil
		},
	}
	if err := EnsureLoaded(context.Background(), msbClient, "test-project", rTag, ui); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if !verboseContains(ui, "cached content differs from the Docker image") {
		t.Errorf("verbose calls = %v, want a stale-cache reason", ui.VerboseCalls)
	}
}
