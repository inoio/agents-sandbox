package image

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestEnsureImageSuccess(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID:     "sha256:abc123",
					Config: dockerConfigWith("1.2.3", []string{"PATH=/usr/bin"}),
				},
			}, nil
		},
	})

	info, err := EnsureImage(context.Background(), a, "test-project", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.OpenCodeVersion != "1.2.3" {
		t.Errorf("info.OpenCodeVersion = %q, want %q", info.OpenCodeVersion, "1.2.3")
	}
	if info.Env["PATH"] != "/usr/bin" {
		t.Errorf("info.Env = %v", info.Env)
	}
}

func TestEnsureImageReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	docker.WithDefaultErrorDockerMock(t)

	_, err := EnsureImage(
		context.Background(),
		a,
		"test-project",
		BuildOptions{Force: true},
		&termio.Mock{},
	)
	if err == nil {
		t.Error("expected EnsureImage to return an error when Docker build fails")
	}
}

func TestBuildSuccess(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID:     "sha256:abc123",
					Config: dockerConfigWith("1.2.3", []string{"PATH=/usr/bin"}),
				},
			}, nil
		},
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(io.LimitReader(nil, 0)), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	msb.WithMsbMock(t, msbClient)

	if err := Build(context.Background(), a, "test-project", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Errorf("expected 1 image load, got %d", len(msbClient.LoadedImages))
	}
}

func TestBuildReturnsErrorWhenImageBuildFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	docker.WithDefaultErrorDockerMock(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	if err := Build(context.Background(), a, "test-project", BuildOptions{Force: true}, &termio.Mock{}); err == nil {
		t.Error("expected Build to return an error when image build fails")
	}
}

func TestBuildReturnsErrorWhenLoadFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID:     "sha256:abc123",
					Config: dockerConfigWith("1.2.3", nil),
				},
			}, nil
		},
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return nil, errors.New("docker save failed")
		},
	})
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	})

	if err := Build(context.Background(), a, "test-project", BuildOptions{}, &termio.Mock{}); err == nil {
		t.Error("expected Build to return an error when loading the image into microsandbox fails")
	}
}

func TestInspectExistingImageReturnsEmptyOnInspectFailure(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("image not found")
		},
	})

	if got := inspectExistingImage(context.Background(), "runner-tag", &termio.Mock{}); got != "" {
		t.Errorf("inspectExistingImage = %q, want empty string on inspect failure", got)
	}
}

func TestEnsureImageReturnsErrorWhenRunnerBuildFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	// The base image build must succeed so the flow reaches the runner build,
	// which then fails.
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			if slices.Contains(opts.Tags, naming.BaseTag) {
				return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
			}
			return client.ImageBuildResult{}, errors.New("runner build boom")
		},
	})
	dockerfile := []byte("FROM " + naming.BaseImagePrefix + ":latest\nRUN echo hi\n")
	_, err := EnsureImageWithClient(
		context.Background(), a, dockerfile, "test-project",
		BuildOptions{Force: true}, &termio.Mock{},
	)
	if err == nil {
		t.Error("expected error when runner image build fails")
	}
}

func TestEnsureImageReturnsErrorWhenVersionResolveFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "", errors.New("resolve boom")
	})
	_, err := EnsureImageWithClient(
		context.Background(), a, []byte("FROM x\n"), "test-project",
		BuildOptions{}, &termio.Mock{},
	)
	if err == nil {
		t.Error("expected error when resolving agent version fails")
	}
}
