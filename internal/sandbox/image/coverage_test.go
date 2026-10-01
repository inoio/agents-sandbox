package image

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
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

func TestEnsureImageWithClientErrorWhenInterceptionCAUnavailable(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	blocked := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	_, err := EnsureImageWithClient(
		context.Background(), a, nil, "test-project", BuildOptions{}, &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error when the TLS interception CA cannot be resolved")
	}
	if !strings.Contains(err.Error(), "resolve TLS interception CA") {
		t.Errorf("error = %q, want it to mention the CA resolution", err)
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
