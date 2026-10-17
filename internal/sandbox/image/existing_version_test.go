package image

import (
	"context"
	"errors"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

func withInspectLabels(t *testing.T, labels map[string]string, present bool) {
	t.Helper()
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if !present {
				return client.ImageInspectResult{}, errors.New("no such image")
			}
			return client.ImageInspectResult{Config: &dockerspec.DockerOCIImageConfig{Labels: labels}}, nil
		},
	})
}

func TestExistingAgentVersionReadsImageLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withInspectLabels(t, map[string]string{agentVersionLabelKey: "1.4.0"}, true)

	got, ok := ExistingAgentVersion(context.Background(), "proj", "opencode")
	if !ok || got != "1.4.0" {
		t.Fatalf("ExistingAgentVersion() = %q,%v want 1.4.0,true", got, ok)
	}
}

func TestExistingAgentVersionRejectsSentinels(t *testing.T) {
	for _, sentinel := range []string{"", identityNone, userProvidedImageIdentity} {
		t.Run("sentinel "+sentinel, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			withInspectLabels(t, map[string]string{agentVersionLabelKey: sentinel}, true)

			if got, ok := ExistingAgentVersion(context.Background(), "proj", "opencode"); ok {
				t.Fatalf("ExistingAgentVersion() = %q,%v want false for sentinel %q", got, ok, sentinel)
			}
		})
	}
}

func TestExistingAgentVersionAbsentImage(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withInspectLabels(t, nil, false)

	if got, ok := ExistingAgentVersion(context.Background(), "proj", "opencode"); ok {
		t.Fatalf("ExistingAgentVersion() = %q,%v want false for absent image", got, ok)
	}
}

func TestExistingAgentVersionMissingLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withInspectLabels(t, map[string]string{"unrelated": "x"}, true)

	if got, ok := ExistingAgentVersion(context.Background(), "proj", "opencode"); ok {
		t.Fatalf("ExistingAgentVersion() = %q,%v want false for missing label", got, ok)
	}
}
