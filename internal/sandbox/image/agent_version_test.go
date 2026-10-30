package image

import (
	"context"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestEnsureImageReportsBakedAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	rendered, err := RenderDockerfile(a, "1.2.3", nil, dockerOff)
	if err != nil {
		t.Fatalf("render Dockerfile: %v", err)
	}
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(rendered, "1.2.3"),
		agentVersionLabelKey: "1.2.3",
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels},
			}, nil
		},
	})

	info, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if info.AgentVersion != "1.2.3" {
		t.Errorf("AgentVersion = %q, want 1.2.3", info.AgentVersion)
	}
}

func TestEnsureImageReportsEmptyForSentinelLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	rendered, err := RenderDockerfile(a, "1.2.3", nil, dockerOff)
	if err != nil {
		t.Fatalf("render Dockerfile: %v", err)
	}
	labels := map[string]string{
		dockerfileIDLabelKey: computeDockerfileID(rendered, "1.2.3"),
		agentVersionLabelKey: userProvidedImageIdentity,
	}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels},
			}, nil
		},
	})

	info, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if info.AgentVersion != "" {
		t.Errorf("AgentVersion = %q, want empty for a user-provided label", info.AgentVersion)
	}
}
