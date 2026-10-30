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

// TestComputeDockerfileIDFoldsMergeScript asserts the identity is stable for
// identical inputs and changes when the embedded profile.d merge script changes,
// so editing the shipped script invalidates existing runner images.
func TestComputeDockerfileIDFoldsMergeScript(t *testing.T) {
	rendered := []byte("FROM scratch\n")
	original := embeddedPathMergeScript
	t.Cleanup(func() { embeddedPathMergeScript = original })

	before := computeDockerfileID(rendered, "1.2.3")
	if again := computeDockerfileID(rendered, "1.2.3"); again != before {
		t.Fatalf("computeDockerfileID not stable for identical inputs: %q vs %q", before, again)
	}

	embeddedPathMergeScript = append(append([]byte(nil), original...), []byte("\n# changed\n")...)
	if after := computeDockerfileID(rendered, "1.2.3"); after == before {
		t.Error("computeDockerfileID did not change after the merge script changed")
	}
}

// TestComputeDockerfileIDFoldsTrustHelper asserts editing the vendored CA-trust
// helper invalidates existing runner images, the same guarantee the merge-script
// fold has: the helper travels in the build context under an invariant COPY, so
// only its bytes in the identity can force a warm image to be rebuilt.
func TestComputeDockerfileIDFoldsTrustHelper(t *testing.T) {
	rendered := []byte("FROM scratch\n")
	original := embeddedTrustHelperScript
	t.Cleanup(func() { embeddedTrustHelperScript = original })

	before := computeDockerfileID(rendered, "1.2.3")
	embeddedTrustHelperScript = append(append([]byte(nil), original...), []byte("\n# changed\n")...)
	if after := computeDockerfileID(rendered, "1.2.3"); after == before {
		t.Error("computeDockerfileID did not change after the trust helper changed")
	}
}

// TestComputeDockerfileIDFoldsTrustHelperLicense asserts the vendored license is
// part of the image identity like the helper beside it: the final stage installs
// it under an invariant COPY, so only its bytes in the identity can force a warm
// image to be rebuilt after the license text changes.
func TestComputeDockerfileIDFoldsTrustHelperLicense(t *testing.T) {
	rendered := []byte("FROM scratch\n")
	original := embeddedTrustHelperLicense
	t.Cleanup(func() { embeddedTrustHelperLicense = original })

	before := computeDockerfileID(rendered, "1.2.3")
	embeddedTrustHelperLicense = append(append([]byte(nil), original...), []byte("\n# changed\n")...)
	if after := computeDockerfileID(rendered, "1.2.3"); after == before {
		t.Error("computeDockerfileID did not change after the trust helper license changed")
	}
}

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
				dockerfileIDLabelKey: computeDockerfileID(renderBytes(t, a, nil, dockerOff), "1.2.3"),
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

// TestEnsureImageReportsRenderError verifies a project Dockerfile that declares
// a reserved stage alias surfaces the render error before any build runs.
func TestEnsureImageReportsRenderError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	project := []byte("FROM debian:trixie-slim AS agents-sandbox-base\n")

	_, err := EnsureImageWithClient(context.Background(), a, project, "proj", BuildOptions{}, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "render Dockerfile") {
		t.Fatalf("EnsureImageWithClient error = %v, want a render Dockerfile error", err)
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
	// The deferred path renders before the version is known, so the identity
	// uses the version-less render.
	rendered := renderBytesVersion(t, a, "", nil, dockerOff)
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
	wantID := computeDockerfileID(renderBytes(t, a, nil, dockerOff), "1.2.3")
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
	if _, err := EnsureImage(
		context.Background(),
		a,
		"proj",
		BuildOptions{Docker: dockerOn},
		&termio.Mock{},
	); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if gotArgs == nil || gotArgs["DOCKER_VERSION"] == nil || *gotArgs["DOCKER_VERSION"] != "29.7.2" {
		t.Errorf("DOCKER_VERSION build arg = %v, want 29.7.2", gotArgs)
	}
}

// TestResolveBaseDigestPullsAbsentBase verifies the pull-then-inspect path: the
// reference is pulled as given (the caller passes the qualified base ref), while
// the returned provenance label keeps the familiar spelling.
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
		ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			pulled = ref
			return docker.PullResponse{Body: strings.NewReader("")}, nil
		},
	})
	const qualified = "docker.io/library/debian:trixie-slim"
	got, err := resolveBaseDigest(context.Background(), qualified, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if pulled != qualified {
		t.Errorf("ImagePull called with %q, want %q", pulled, qualified)
	}
	if got != "debian:trixie-slim@sha256:pulled" {
		t.Errorf("resolveBaseDigest = %q, want the familiar spelling in the label", got)
	}
}

// TestResolveBaseDigestSurfacesPullFailure pins that a registry-level pull
// failure is surfaced as the classified base-image error with its troubleshooting
// link, not swallowed and re-reported as a misleading post-pull inspect failure.
// Both delivery paths are covered: the pull call's own error and a registry
// failure inside the stream. The test also asserts a surfaced failure leaves no
// second inspect to confuse the message.
func TestResolveBaseDigestSurfacesPullFailure(t *testing.T) {
	const pullFailure = `pull access denied for private/base, repository does not exist or may require 'docker login'`
	for _, tc := range []struct {
		name string
		pull func() (client.ImagePullResponse, error)
	}{
		{
			name: "failure delivered inside the stream",
			pull: func() (client.ImagePullResponse, error) {
				return docker.PullResponse{WaitErr: errors.New(pullFailure)}, nil
			},
		},
		{
			name: "failure returned by the pull call",
			pull: func() (client.ImagePullResponse, error) {
				return nil, errors.New(pullFailure)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspects := 0
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					inspects++
					return client.ImageInspectResult{}, errors.New("No such image")
				},
				ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
					return tc.pull()
				},
			})
			_, err := resolveBaseDigest(context.Background(), "private/base:latest", false, &termio.Mock{})
			if err == nil || !strings.Contains(err.Error(), "base image not found or not logged in") {
				t.Errorf("resolveBaseDigest error = %v, want the classified base-image failure", err)
			}
			if err != nil && !strings.Contains(err.Error(), "the-base-image-cannot-be-found") {
				t.Errorf("resolveBaseDigest error = %v, want the troubleshooting docs link", err)
			}
			if inspects != 1 {
				t.Errorf("ImageInspect called %d times, want 1 (a surfaced pull failure must not re-inspect)", inspects)
			}
		})
	}
}

// TestResolveBaseDigestKeepsUnparseableRef covers a project Dockerfile FROM that
// is not a valid image reference: the recorded label carries it through
// unchanged rather than dropping the base it could not parse.
func TestResolveBaseDigestKeepsUnparseableRef(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:odd"}, nil
		},
	})
	const unparseable = "Not A Valid Ref"
	got, err := resolveBaseDigest(context.Background(), unparseable, false, &termio.Mock{})
	if err != nil {
		t.Fatalf("resolveBaseDigest: %v", err)
	}
	if got != unparseable+"@sha256:odd" {
		t.Errorf("resolveBaseDigest = %q, want the ref carried through unchanged", got)
	}
}

func TestBaseImageRef(t *testing.T) {
	a := agentOpencode(t)
	if got := baseImageRef(renderBytes(t, a, nil, dockerOff)); got != distroBaseRef {
		t.Errorf("default baseImageRef = %q, want %q", got, distroBaseRef)
	}
	custom := []byte("FROM ubuntu:24.04\nRUN echo hi\n")
	if got := baseImageRef(renderBytes(t, a, custom, dockerOff)); got != "ubuntu:24.04" {
		t.Errorf("custom baseImageRef = %q", got)
	}
}
