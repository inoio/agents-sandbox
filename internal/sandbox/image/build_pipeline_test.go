package image

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// TestEnsureImageSkipsBuildWhenDockerfileIDMatches verifies the build is skipped
// when the existing runner image already carries a matching dockerfile-id label.
func TestEnsureImageSkipsBuildWhenDockerfileIDMatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			id := "sha256:existing"
			labels := map[string]string{
				dockerfileIDLabelKey: computeDockerfileID(RenderDockerfile(a, nil, false), "1.2.3"),
			}
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
			}
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{
				ID:     id,
				Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{Labels: labels}},
			}}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			built = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	info, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if built {
		t.Error("expected the docker build to be skipped when the dockerfile-id label matches")
	}
	if info.Digest != "sha256:existing" {
		t.Errorf("Digest = %q, want the existing image ID", info.Digest)
	}
}

// TestEnsureImageBuildsWhenDockerfileIDMismatches verifies the build runs when
// the existing runner image's dockerfile-id label differs from the current one.
func TestEnsureImageBuildsWhenDockerfileIDMismatches(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
			}
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					ImageConfig: ocispec.ImageConfig{Labels: map[string]string{dockerfileIDLabelKey: "stale"}},
				},
			}}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			built = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !built {
		t.Error("expected the docker build to run when the dockerfile-id label mismatches")
	}
}

// TestEnsureImageBuildArgsIncludeBaseAndAgentVersion asserts the BASE_IMAGE
// provenance build arg and the pinned agent version arg.
func TestEnsureImageBuildArgsIncludeBaseAndAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	var gotArgs map[string]*string
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			gotArgs = opts.BuildArgs
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	_, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	for key, want := range map[string]string{
		"BASE_IMAGE":       "debian:trixie-slim@sha256:base",
		"OPENCODE_VERSION": "1.2.3",
	} {
		if gotArgs == nil || gotArgs[key] == nil || *gotArgs[key] != want {
			t.Errorf("build arg %s = %v, want %q", key, gotArgs, want)
		}
	}
}

// TestEnsureImageDindAddsDockerVersionArg verifies the dind build arg is only
// passed when dind is enabled.
func TestEnsureImageDindAddsDockerVersionArg(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	var gotArgs map[string]*string
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			gotArgs = opts.BuildArgs
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{Dind: true}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if gotArgs == nil || gotArgs["DOCKER_VERSION"] == nil || *gotArgs["DOCKER_VERSION"] != "29.7.2" {
		t.Errorf("DOCKER_VERSION build arg = %v, want 29.7.2", gotArgs)
	}
}

// TestResolveBaseDigestPullsAbsentBase verifies the pull-then-inspect path.
func TestResolveBaseDigestPullsAbsentBase(t *testing.T) {
	var pulled string
	inspects := 0
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			inspects++
			if inspects == 1 {
				return client.ImageInspectResult{}, errors.New("not found")
			}
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:pulled"}}, nil
		},
		ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (io.ReadCloser, error) {
			pulled = ref
			return io.NopCloser(strings.NewReader("")), nil
		},
	})
	got, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", &termio.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if pulled != "debian:trixie-slim" {
		t.Errorf("ImagePull called with %q, want %q", pulled, "debian:trixie-slim")
	}
	if got != "debian:trixie-slim@sha256:pulled" {
		t.Errorf("resolveBaseDigest = %q", got)
	}
}

func TestBaseImageRef(t *testing.T) {
	a := agentOpencode(t)
	if got := baseImageRef(RenderDockerfile(a, nil, false)); got != "debian:trixie-slim" {
		t.Errorf("default baseImageRef = %q", got)
	}
	custom := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	if got := baseImageRef(RenderDockerfile(a, custom, false)); got != "ubuntu:24.04" {
		t.Errorf("custom baseImageRef = %q", got)
	}
}
