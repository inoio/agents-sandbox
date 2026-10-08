package image

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
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
				dockerfileIDLabelKey: computeDockerfileID(renderBytes(t, a, nil, false), "1.2.3"),
			}
			if ref == "debian:trixie-slim" {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID:     id,
				Config: &dockerspec.DockerOCIImageConfig{Labels: labels}}, nil
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
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: "stale"},
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
			return client.ImageInspectResult{ID: "sha256:base"}, nil
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

// TestEnsureImageResolvesAnUnpinnedAgentVersion verifies that an empty version
// is resolved to a real release before it is passed to the Docker build.
func TestEnsureImageResolvesAnUnpinnedAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	var requested string
	var gotArgs map[string]*string
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		requested = req
		return "1.2.3", nil
	})
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:base"}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			gotArgs = opts.BuildArgs
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if requested != "" {
		t.Errorf("resolver requested = %q, want empty version", requested)
	}
	if gotArgs == nil || gotArgs["OPENCODE_VERSION"] == nil {
		t.Fatalf("OPENCODE_VERSION build arg missing: %v", gotArgs)
	}
	if got := *gotArgs["OPENCODE_VERSION"]; got != "1.2.3" {
		t.Errorf("OPENCODE_VERSION build arg = %q, want 1.2.3", got)
	}
}

// TestEnsureImageReusesUnknownUserAgentWithoutResolvingVersion verifies that
// an existing user-provided image can be reused without contacting the release
// endpoint when its version was not recorded.
func TestEnsureImageReusesUnknownUserAgentWithoutResolvingVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rendered := renderBytes(t, a, nil, false)
	wantID := computeDockerfileID(rendered, userProvidedImageIdentity)
	buildCalled := false
	resolverCalled := false
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		resolverCalled = true
		return "1.2.3", nil
	})
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: wantID}}}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			buildCalled = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(
		context.Background(),
		a,
		"proj",
		BuildOptions{UserProvided: true},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if resolverCalled {
		t.Error("user-provided image reuse must not resolve an agent version")
	}
	if buildCalled {
		t.Error("user-provided image reuse must not rebuild a matching image")
	}
}

// TestEnsureImageResolvesUnknownUserAgentOnlyForARebuild verifies that a stale
// user-provided image gets a real installer version and records that resolved
// version in the image identity for subsequent reuse.
func TestEnsureImageResolvesUnknownUserAgentOnlyForARebuild(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	var gotArgs map[string]*string
	var gotDockerfileID string
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		if requested != "" {
			t.Errorf("resolver requested = %q, want empty version", requested)
		}
		return "1.2.3", nil
	})
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:existing",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{dockerfileIDLabelKey: "stale"}}}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			gotArgs = opts.BuildArgs
			if value := opts.BuildArgs["DOCKERFILE_ID"]; value != nil {
				gotDockerfileID = *value
			}
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(
		context.Background(),
		a,
		"proj",
		BuildOptions{UserProvided: true},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if gotArgs == nil || gotArgs["OPENCODE_VERSION"] == nil || *gotArgs["OPENCODE_VERSION"] != "1.2.3" {
		t.Errorf("OPENCODE_VERSION build arg = %v, want 1.2.3", gotArgs)
	}
	wantID := computeDockerfileID(renderBytes(t, a, nil, false), "1.2.3")
	if gotDockerfileID != wantID {
		t.Errorf("DOCKERFILE_ID = %q, want resolved-version identity %q", gotDockerfileID, wantID)
	}
}

// TestEnsureImageDockerAddsDockerVersionArg verifies the docker build arg is only
// passed when dind is enabled.
func TestEnsureImageDockerAddsDockerVersionArg(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	var gotArgs map[string]*string
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:base"}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			gotArgs = opts.BuildArgs
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{Docker: true}, &termio.Mock{}); err != nil {
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
			return client.ImageInspectResult{ID: "sha256:pulled"}, nil
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
	if got := baseImageRef(renderBytes(t, a, nil, false)); got != "debian:trixie-slim" {
		t.Errorf("default baseImageRef = %q", got)
	}
	custom := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, custom, false)); got != "ubuntu:24.04" {
		t.Errorf("custom baseImageRef = %q", got)
	}
}
