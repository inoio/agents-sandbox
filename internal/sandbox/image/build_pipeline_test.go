package image

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// runnerInspectWith builds a runner-image inspect result carrying the given
// labels, used to mock the state of an already-built runner image.
func runnerInspectWith(labels map[string]string) client.ImageInspectResult {
	return client.ImageInspectResult{InspectResponse: image.InspectResponse{
		ID:     "sha256:existing",
		Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{Labels: labels}},
	}}
}

// buildRecordingDockerMock installs a Docker mock that records whether a build
// ran and with which NoCache setting, serving runnerImage for runner refs and
// baseResult for the default debian:trixie-slim base ref.
func buildRecordingDockerMock(
	t *testing.T,
	baseResult func() (client.ImageInspectResult, error),
	runnerImage func() client.ImageInspectResult,
	built *bool,
	gotNoCache *bool,
) {
	t.Helper()
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "debian:trixie-slim" {
				return baseResult()
			}
			return runnerImage(), nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			*built = true
			*gotNoCache = opts.NoCache
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
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

// TestEnsureImageNoCacheMatchesForce verifies rebuilds use Docker's layer cache
// unless the build was forced: a dockerfile-id mismatch with Force false builds
// with NoCache false, while Force true keeps the clean --no-cache build.
func TestEnsureImageNoCacheMatchesForce(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			WithMockAgentVersion(t, "1.2.3")
			a := agentOpencode(t)
			built := false
			gotNoCache := !force
			staleRunner := func() client.ImageInspectResult {
				return runnerInspectWith(map[string]string{dockerfileIDLabelKey: "stale"})
			}
			basePresent := func() (client.ImageInspectResult, error) {
				return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
			}
			buildRecordingDockerMock(t, basePresent, staleRunner, &built, &gotNoCache)

			if _, err := EnsureImage(
				context.Background(),
				a,
				"proj",
				BuildOptions{Force: force},
				&termio.Mock{},
			); err != nil {
				t.Fatalf("EnsureImage: %v", err)
			}
			if !built {
				t.Fatal("expected a rebuild on dockerfile-id mismatch")
			}
			if gotNoCache != force {
				t.Errorf("ImageBuild NoCache = %v, want %v", gotNoCache, force)
			}
		})
	}
}

// TestEnsureImageRebuildsWhenBaseMovedLocally verifies that a locally moved
// base tag (recorded base-label digest differs from the local base ID) forces a
// rebuild even though the dockerfile-id label matches.
func TestEnsureImageRebuildsWhenBaseMovedLocally(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	gotNoCache := true
	matchedRunner := func() client.ImageInspectResult {
		return runnerInspectWith(map[string]string{
			dockerfileIDLabelKey: computeDockerfileID(RenderDockerfile(a, nil, false), "1.2.3"),
			baseImageLabelKey:    "debian:trixie-slim@sha256:old-base",
		})
	}
	movedBase := func() (client.ImageInspectResult, error) {
		return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:new-base"}}, nil
	}
	buildRecordingDockerMock(t, movedBase, matchedRunner, &built, &gotNoCache)

	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if !built {
		t.Error("expected a rebuild when the locally-tagged base image has moved")
	}
	if gotNoCache {
		t.Error("expected the base-move rebuild to use the Docker layer cache (NoCache false)")
	}
}

// TestEnsureImageSkipsWhenBaseUnchanged verifies that a runner image whose
// recorded base digest equals the locally-tagged base ID is not rebuilt.
func TestEnsureImageSkipsWhenBaseUnchanged(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	gotNoCache := false
	matchedRunner := func() client.ImageInspectResult {
		return runnerInspectWith(map[string]string{
			dockerfileIDLabelKey: computeDockerfileID(RenderDockerfile(a, nil, false), "1.2.3"),
			baseImageLabelKey:    "debian:trixie-slim@sha256:base",
		})
	}
	sameBase := func() (client.ImageInspectResult, error) {
		return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
	}
	buildRecordingDockerMock(t, sameBase, matchedRunner, &built, &gotNoCache)

	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if built {
		t.Error("expected the build to be skipped when the base image is unchanged")
	}
}

// TestEnsureImageSkipsWhenBaseLabelMissing verifies that an existing runner
// image without the base provenance label is not rebuilt just because the base
// cannot be compared.
func TestEnsureImageSkipsWhenBaseLabelMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	gotNoCache := false
	matchedRunner := func() client.ImageInspectResult {
		return runnerInspectWith(map[string]string{
			dockerfileIDLabelKey: computeDockerfileID(RenderDockerfile(a, nil, false), "1.2.3"),
		})
	}
	basePresent := func() (client.ImageInspectResult, error) {
		return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
	}
	buildRecordingDockerMock(t, basePresent, matchedRunner, &built, &gotNoCache)

	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if built {
		t.Error("expected the build to be skipped when the runner image has no base label")
	}
}

// TestEnsureImageSkipsWhenBaseUninspectable verifies the skip path stays
// offline-safe: an uninspectable local base image must not force a rebuild.
func TestEnsureImageSkipsWhenBaseUninspectable(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	gotNoCache := false
	matchedRunner := func() client.ImageInspectResult {
		return runnerInspectWith(map[string]string{
			dockerfileIDLabelKey: computeDockerfileID(RenderDockerfile(a, nil, false), "1.2.3"),
			baseImageLabelKey:    "debian:trixie-slim@sha256:base",
		})
	}
	baseUninspectable := func() (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, errors.New("base inspect boom")
	}
	buildRecordingDockerMock(t, baseUninspectable, matchedRunner, &built, &gotNoCache)

	if _, err := EnsureImage(context.Background(), a, "proj", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if built {
		t.Error("expected the build to be skipped when the local base image cannot be inspected")
	}
}

// TestBaseMovedLocally covers the decision table of the base-move helper.
func TestBaseMovedLocally(t *testing.T) {
	const rTag = "agents-sandbox/runner-proj:opencode-latest"
	const baseRef = "debian:trixie-slim"
	cases := []struct {
		name         string
		runnerResult client.ImageInspectResult
		runnerErr    error
		baseResult   client.ImageInspectResult
		baseErr      error
		want         bool
	}{
		{
			name:         "digest differs from local base id",
			runnerResult: runnerInspectWith(map[string]string{baseImageLabelKey: "debian:trixie-slim@sha256:old"}),
			baseResult:   client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:new"}},
			want:         true,
		},
		{
			name:         "digest equals local base id",
			runnerResult: runnerInspectWith(map[string]string{baseImageLabelKey: "debian:trixie-slim@sha256:same"}),
			baseResult:   client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:same"}},
			want:         false,
		},
		{
			name:       "runner inspect error",
			runnerErr:  errors.New("boom"),
			baseResult: client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:new"}},
			want:       false,
		},
		{
			name:         "runner has no config",
			runnerResult: client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:existing"}},
			baseResult:   client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:new"}},
			want:         false,
		},
		{
			name:         "base label missing",
			runnerResult: runnerInspectWith(map[string]string{dockerfileIDLabelKey: "id"}),
			baseResult:   client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:new"}},
			want:         false,
		},
		{
			name:         "base label without digest",
			runnerResult: runnerInspectWith(map[string]string{baseImageLabelKey: "debian:trixie-slim"}),
			baseResult:   client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:new"}},
			want:         false,
		},
		{
			name:         "base inspect error",
			runnerResult: runnerInspectWith(map[string]string{baseImageLabelKey: "debian:trixie-slim@sha256:old"}),
			baseErr:      errors.New("boom"),
			want:         false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
					if ref == baseRef {
						return tc.baseResult, tc.baseErr
					}
					return tc.runnerResult, tc.runnerErr
				},
			})
			if got := baseMovedLocally(context.Background(), rTag, baseRef); got != tc.want {
				t.Errorf("baseMovedLocally = %v, want %v", got, tc.want)
			}
		})
	}
}
