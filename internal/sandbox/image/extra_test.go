package image

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestReferencesImageSkipsFromWithoutImage(t *testing.T) {
	if referencesImage([]byte("FROM --platform=linux/amd64\n"), "debian") {
		t.Error("expected referencesImage=false for a FROM line carrying only flags")
	}
}

func TestReferencesImageFalseForUnparseableRef(t *testing.T) {
	if referencesImage([]byte("FROM INVALIDREF\n"), "anything") {
		t.Error("expected referencesImage=false when the FROM reference cannot be parsed")
	}
}

func TestReferencesImageTerminatesOnCyclicStageAlias(t *testing.T) {
	// The project Dockerfile controls its own stage aliases, so it can name a
	// stage after its own base token or make two stages alias each other.
	// Resolving the final stage to its underlying image must terminate rather
	// than follow the cycle forever.
	for _, dockerfile := range []string{
		"FROM 0 AS 0\n",
		"FROM base AS builder\nFROM builder AS base\n",
	} {
		if referencesImage([]byte(dockerfile), "agents-sandbox/runner-base") {
			t.Errorf("referencesImage(%q) = true, want false", dockerfile)
		}
	}
}

func TestResolveStageBaseReturnsUnresolvedToken(t *testing.T) {
	if got := resolveStageBase("debian", map[string]string{"builder": "base"}); got != "debian" {
		t.Errorf(`resolveStageBase("debian", {builder: base}) = %q, want "debian"`, got)
	}
}

func TestResolveStageBaseStopsOnCyclicAlias(t *testing.T) {
	// The token the walk stops at is observable, not just "did it terminate":
	// a self-alias stops at "0", and a two-stage cycle entered at "b" stops at
	// "b". Asserting the value keeps the cycle guard's return honest.
	if got := resolveStageBase("0", map[string]string{"0": "0"}); got != "0" {
		t.Errorf(`resolveStageBase("0", {0: 0}) = %q, want "0"`, got)
	}
	if got := resolveStageBase("b", map[string]string{"a": "b", "b": "a"}); got != "b" {
		t.Errorf(`resolveStageBase("b", {a: b, b: a}) = %q, want "b"`, got)
	}
}

func TestFinalStageTokenSkipsFromWithoutImage(t *testing.T) {
	if got := finalStageToken([]byte("FROM --platform=linux/amd64\n")); got != "" {
		t.Errorf("finalStageToken = %q, want empty", got)
	}
}

func TestFinalStageTokenResolvesAlias(t *testing.T) {
	dockerfile := []byte("FROM golang:1.24 AS build\nFROM build AS final\nRUN echo hi\n")
	if got := finalStageToken(dockerfile); got != "golang:1.24" {
		t.Errorf("finalStageToken = %q, want golang:1.24 resolved through stage aliases", got)
	}
}

func TestBaseImageRefFallsBackToRawToken(t *testing.T) {
	if got := baseImageRef([]byte("FROM INVALIDREF\n")); got != "INVALIDREF" {
		t.Errorf("baseImageRef = %q, want the raw unparseable token", got)
	}
}

func TestBuildDockerImageError(t *testing.T) {
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, errors.New("build boom")
		},
	})
	err := buildDockerImage(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"),
		"tag", "label", false, "v", "base", "id", false, imageIdentity{}, &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected buildDockerImage to return an error when the build fails")
	}
}

func TestBuildDockerImageForwardsStreamLines(t *testing.T) {
	a := agentOpencode(t)
	ui := &termio.Mock{}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{
				Body: io.NopCloser(strings.NewReader("{\"stream\":\"Step 1/1 : FROM debian\\n\"}\n")),
			}, nil
		},
	})
	if err := buildDockerImage(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"),
		"tag", "label", false, "v", "base", "id", false, imageIdentity{}, ui,
	); err != nil {
		t.Fatalf("buildDockerImage: %v", err)
	}
	var found bool
	for _, c := range ui.VerboseCalls {
		if c == "Step 1/1 : FROM debian" {
			found = true
		}
	}
	if !found {
		t.Errorf("build output stream line not forwarded to verbose output; got %v", ui.VerboseCalls)
	}
}

func TestBuildImageReturnsErrorOnImageBuild(t *testing.T) {
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, errors.New("image build failed")
		},
	})
	err := buildImage(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"),
		"tag", false, "v", "base", "id", false, imageIdentity{}, func(string) {},
	)
	if err == nil || !strings.Contains(err.Error(), "docker image build failed") {
		t.Errorf("buildImage error = %v, want a docker build failure", err)
	}
}

func TestBuildImageDetectsPullAccessDenied(t *testing.T) {
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{
				Body: io.NopCloser(strings.NewReader(
					`{"errorDetail":{"message":"pull access denied for base"},"error":"pull access denied for base"}`,
				)),
			}, nil
		},
	})
	err := buildImage(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"),
		"tag", false, "v", "base", "id", false, imageIdentity{}, func(string) {},
	)
	if err == nil || !strings.Contains(err.Error(), "base image not found or not logged in") {
		t.Errorf("buildImage error = %v, want a pull-access-denied hint", err)
	}
}

func TestBuildImageReturnsGenericBuildError(t *testing.T) {
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{
				Body: io.NopCloser(strings.NewReader(`{"errorDetail":{"message":"some other failure"}}`)),
			}, nil
		},
	})
	err := buildImage(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"),
		"tag", false, "v", "base", "id", false, imageIdentity{}, func(string) {},
	)
	if err == nil || !strings.Contains(err.Error(), "some other failure") ||
		strings.Contains(err.Error(), "not found or not logged in") {
		t.Errorf("buildImage error = %v, want a generic build failure wrapping the stream error", err)
	}
}

// TestBuildImageHintsTroubleshootingOnUnresolvedName pins the podman failure
// mode that is not an engine bug: an image name with no registry, which podman
// refuses to resolve when it has no search registries. The error must point at
// the troubleshooting section naming that fix. The real message wraps the name
// and the config path in quotes; the classification only needs these two
// phrases.
func TestBuildImageHintsTroubleshootingOnUnresolvedName(t *testing.T) {
	a := agentOpencode(t)
	const engineError = "short-name debian:trixie-slim did not resolve to an alias and no " +
		"unqualified search registries are defined in /etc/containers/registries.conf"
	for _, tc := range []struct {
		name  string
		build func() (client.ImageBuildResult, error)
	}{
		{
			name: "streamed build error",
			build: func() (client.ImageBuildResult, error) {
				return client.ImageBuildResult{
					Body: io.NopCloser(strings.NewReader(`{"errorDetail":{"message":"` + engineError + `"}}`)),
				}, nil
			},
		},
		{
			name: "build call error",
			build: func() (client.ImageBuildResult, error) {
				return client.ImageBuildResult{}, errors.New(engineError)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker.WithDockerMock(t, &docker.MockDockerClient{
				ImageBuildFn: func(
					_ context.Context, _ io.Reader, _ client.ImageBuildOptions,
				) (client.ImageBuildResult, error) {
					return tc.build()
				},
			})
			err := buildImage(
				context.Background(), a, []byte("FROM debian:trixie-slim\n"),
				"tag", false, "v", "base", "id", false, imageIdentity{}, func(string) {},
			)
			if err == nil || !strings.Contains(err.Error(), "troubleshooting.html#the-base-image-cannot-be-found") {
				t.Errorf("buildImage error = %v, want a troubleshooting pointer", err)
			}
		})
	}
}

func TestScanBuildOutputUnexpectedOutput(t *testing.T) {
	err := scanBuildOutput(strings.NewReader("{not json"), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "unexpected Docker build output") {
		t.Errorf("scanBuildOutput error = %v, want an unexpected-output error", err)
	}
}

func TestEnsureImageCannotInspectBuiltImage(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	built := false
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if !built {
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			}
			return client.ImageInspectResult{}, errors.New("inspect boom")
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			built = true
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	_, err := EnsureImageWithClient(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"),
		"proj", BuildOptions{Force: true}, &termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "cannot inspect built image") {
		t.Errorf("EnsureImageWithClient error = %v, want an inspect-built-image failure", err)
	}
}

func TestEnsureImageCannotReadImageInfo(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	calls := 0
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			calls++
			switch calls {
			case 1:
				return client.ImageInspectResult{ID: "sha256:base"}, nil
			case 2:
				return client.ImageInspectResult{
					ID: "sha256:built", Config: dockerConfigWith("", nil),
				}, nil
			default:
				return client.ImageInspectResult{}, errors.New("read env boom")
			}
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	})
	_, err := EnsureImageWithClient(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"),
		"proj", BuildOptions{Force: true}, &termio.Mock{},
	)
	if err == nil || !strings.Contains(err.Error(), "inspect built image") {
		t.Errorf("EnsureImageWithClient error = %v, want an inspect-built-image failure reading env", err)
	}
}

func TestEnsureLoadedReturnsErrorWhenLoadFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageLoadFn: func(_ context.Context, _ string, _ io.Reader) error {
			return errors.New("load boom")
		},
	}
	info := ImageInfo{DockerTag: rTag, Ref: rTag, Digest: "sha256:new"}
	if _, err := EnsureLoaded(context.Background(), msbClient, info, &termio.Mock{}); err == nil {
		t.Fatal("expected EnsureLoaded to return an error when ImageLoad fails")
	}
}

func TestCachedImageMatchesMsbInspectError(t *testing.T) {
	msbClient := &msb.MockMsbClient{
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("msb inspect boom")
		},
	}
	if cachedImageMatches(
		context.Background(),
		msbClient,
		"ref",
		ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:x"},
	) {
		t.Error("expected false when the microsandbox inspect fails")
	}
}

// TestCachedImageMatches pins the content-identity contract: only a cached
// digest equal to the Docker image ID counts as a match. A cached image that
// records no digest is unknown content and must reload — the dockerfile-id
// label is a source signature that can match while the bytes differ, so it is
// never trusted.
func TestCachedImageMatches(t *testing.T) {
	cases := []struct {
		name      string
		msbDigest string
		msbLabels map[string]string
		msbNil    bool
		info      ImageInfo
		want      bool
	}{
		{
			name:      "equal digest matches",
			msbDigest: "sha256:same",
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:same"},
			want:      true,
		},
		{
			name:      "different digest never matches, even with equal label",
			msbDigest: "sha256:old",
			msbLabels: map[string]string{dockerfileIDLabelKey: "abc"},
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc"},
			want:      false,
		},
		{
			name:      "missing digest never matches, even with equal label",
			msbLabels: map[string]string{dockerfileIDLabelKey: "abc123"},
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new", DockerfileID: "abc123"},
			want:      false,
		},
		{
			name:      "missing digest and no labels never matches",
			msbLabels: map[string]string{},
			info:      ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			want:      false,
		},
		{
			name:      "cached digest with no requested digest never matches",
			msbDigest: "sha256:same",
			info:      ImageInfo{DockerTag: "ref", Ref: "ref"},
			want:      false,
		},
		{
			name: "no cached digest and no requested digest never matches",
			info: ImageInfo{DockerTag: "ref", Ref: "ref"},
			want: false,
		},
		{
			name:   "nil inspect result never matches",
			msbNil: true,
			info:   ImageInfo{DockerTag: "ref", Ref: "ref", Digest: "sha256:new"},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msbClient := &msb.MockMsbClient{
				ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
					if tc.msbNil {
						return nil, nil //nolint:nilnil // the seam can return a nil config with no error
					}
					return &msbSdk.ImageConfig{Digest: tc.msbDigest, Labels: tc.msbLabels}, nil
				},
			}
			if got := cachedImageMatches(context.Background(), msbClient, tc.info.Ref, tc.info); got != tc.want {
				t.Errorf("cachedImageMatches() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSplitFinalStageNoFrom(t *testing.T) {
	earlier, base, body := splitFinalStage([]byte("RUN echo hi\n"))
	if earlier != "" || body != "" {
		t.Errorf(
			"splitFinalStage without a FROM must return the input as the base, got earlier=%q base=%q body=%q",
			earlier,
			base,
			body,
		)
	}
	if base != "RUN echo hi\n" {
		t.Errorf("base = %q, want the input", base)
	}
}

func TestInjectDockerBlockMarkerWithoutTrailingNewline(t *testing.T) {
	got, injected := injectDockerBlock("RUN prereq\n# agents-sandbox:dind", true)
	if !injected {
		t.Fatal("expected injection at the legacy dind marker")
	}
	want := "RUN prereq\n" + dockerMergeBlock()
	if got != want {
		t.Errorf("injectDockerBlock = %q, want %q", got, want)
	}
}

func TestReadImageInfoFromDockerNilConfig(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{}}, nil
		},
	})
	env, err := readImageInfoFromDocker(context.Background(), "runner-tag")
	if err != nil {
		t.Fatalf("readImageInfoFromDocker: %v", err)
	}
	if env != nil {
		t.Errorf("env = %v, want nil when the image has no config", env)
	}
}

func TestResolveBaseDigestPullError(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("not found")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return nil, errors.New("pull boom")
		},
	})
	_, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", false, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "pull base image") {
		t.Errorf("resolveBaseDigest error = %v, want a pull failure", err)
	}
}

func TestResolveBaseDigestInspectAfterPullError(t *testing.T) {
	inspects := 0
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			inspects++
			return client.ImageInspectResult{}, errors.New("not found")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return docker.PullResponse{Body: strings.NewReader("")}, nil
		},
	})
	_, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", false, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "inspect pulled base image") {
		t.Errorf("resolveBaseDigest error = %v, want an inspect-pulled-image failure", err)
	}
}

func TestResolveBaseDigestPullStreamError(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("not found")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return docker.PullResponse{WaitErr: errors.New("read boom")}, nil
		},
	})
	_, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", false, &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "pull base image") {
		t.Errorf("resolveBaseDigest error = %v, want a pull stream failure", err)
	}
}

func TestResolveAgentVersionFallsBackToEmpty(t *testing.T) {
	got, err := resolveAgentVersion(context.Background(), plainAgent{}, "")
	if err != nil {
		t.Fatalf("resolveAgentVersion: %v", err)
	}
	if got != "" {
		t.Errorf(
			"resolveAgentVersion = %q, want empty for an agent without an UpgradeChecker and no requested version",
			got,
		)
	}
}

func TestEnsureImageReturnsBuildError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a := agentOpencode(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:base"}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, errors.New("build boom")
		},
	})
	_, err := EnsureImageWithClient(
		context.Background(), a, []byte("FROM debian:trixie-slim\n"),
		"proj", BuildOptions{Force: true}, &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected EnsureImageWithClient to return an error when the build fails")
	}
}
