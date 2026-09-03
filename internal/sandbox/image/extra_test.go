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
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
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
		"tag", "label", false, "v", "base", "id", false, &termio.Mock{},
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
		"tag", "label", false, "v", "base", "id", false, ui,
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
		"tag", false, "v", "base", "id", false, func(string) {},
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
		"tag", false, "v", "base", "id", false, func(string) {},
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
		"tag", false, "v", "base", "id", false, func(string) {},
	)
	if err == nil || !strings.Contains(err.Error(), "some other failure") ||
		strings.Contains(err.Error(), "not found or not logged in") {
		t.Errorf("buildImage error = %v, want a generic build failure wrapping the stream error", err)
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
	calls := 0
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			calls++
			if calls == 1 {
				return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
			}
			return client.ImageInspectResult{}, errors.New("inspect boom")
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
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
				return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
			case 2:
				return client.ImageInspectResult{
					InspectResponse: image.InspectResponse{ID: "sha256:built", Config: dockerConfigWith("", nil)},
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

func TestEnsureLoadedWarnsAndContinuesOnRemoveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	ui := &termio.Mock{}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{}}, nil
		},
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return io.NopCloser(strings.NewReader("tar")), nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageRemoveFn: func(_ context.Context, _ string, _ bool) error {
			return errors.New("remove boom")
		},
	}
	if err := EnsureLoaded(context.Background(), msbClient, "test-project", rTag, ui); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning when removing the stale image fails")
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Errorf("LoadedImages = %v, want a reload despite the remove failure", msbClient.LoadedImages)
	}
}

func TestEnsureLoadedReturnsErrorWhenLoadFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	rTag := runnerTag("test-project", a.Name())
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{}}, nil
		},
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
	if err := EnsureLoaded(context.Background(), msbClient, "test-project", rTag, &termio.Mock{}); err == nil {
		t.Fatal("expected EnsureLoaded to return an error when ImageLoad fails")
	}
}

func TestCachedImageMatchesDockerDockerInspectError(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("docker inspect boom")
		},
	})
	if cachedImageMatchesDocker(context.Background(), &msb.MockMsbClient{}, "ref") {
		t.Error("expected false when the Docker inspect fails")
	}
}

func TestCachedImageMatchesDockerNilConfig(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{}}, nil
		},
	})
	if cachedImageMatchesDocker(context.Background(), &msb.MockMsbClient{}, "ref") {
		t.Error("expected false when the Docker inspect has no config")
	}
}

func TestCachedImageMatchesDockerMsbInspectError(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID:     "sha256:x",
					Config: dockerConfigWith("", nil),
				},
			}, nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("msb inspect boom")
		},
	}
	if cachedImageMatchesDocker(context.Background(), msbClient, "ref") {
		t.Error("expected false when the microsandbox inspect fails")
	}
}

func TestCachedImageMatchesDockerByDigest(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID:     "sha256:same",
					Config: dockerConfigWith("", nil),
				},
			}, nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "sha256:same"}, nil
		},
	}
	if !cachedImageMatchesDocker(context.Background(), msbClient, "ref") {
		t.Error("expected true when digests match")
	}
}

func TestCachedImageMatchesDockerByLabel(t *testing.T) {
	labels := map[string]string{dockerfileIDLabelKey: "abc123"}
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID: "",
					Config: &dockerspec.DockerOCIImageConfig{
						ImageConfig: ocispec.ImageConfig{Labels: labels},
					},
				},
			}, nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "", Labels: labels}, nil
		},
	}
	if !cachedImageMatchesDocker(context.Background(), msbClient, "ref") {
		t.Error("expected true when the dockerfile-id labels match")
	}
}

func TestCachedImageMatchesDockerLabelMismatch(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID: "",
					Config: &dockerspec.DockerOCIImageConfig{
						ImageConfig: ocispec.ImageConfig{Labels: map[string]string{dockerfileIDLabelKey: "abc"}},
					},
				},
			}, nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{Digest: "", Labels: map[string]string{dockerfileIDLabelKey: "def"}}, nil
		},
	}
	if cachedImageMatchesDocker(context.Background(), msbClient, "ref") {
		t.Error("expected false when the dockerfile-id labels differ")
	}
}

func TestReplaceFinalStageFromNoFrom(t *testing.T) {
	in := []byte("RUN echo hi\n")
	if got := string(replaceFinalStageFrom(in, []byte("FROM debian:trixie-slim\n"))); got != string(in) {
		t.Errorf("replaceFinalStageFrom without a FROM must return input unchanged, got %q", got)
	}
}

func TestReplaceFinalStageFromBlockWithoutNewline(t *testing.T) {
	in := []byte("FROM opencode-sandbox/runner-base:latest\nRUN echo hi\n")
	block := []byte("FROM debian:trixie-slim")
	got := string(replaceFinalStageFrom(in, block))
	if !strings.Contains(got, "FROM debian:trixie-slim\nRUN echo hi") {
		t.Errorf("block without trailing newline must be separated from the body, got %q", got)
	}
}

func TestInspectExistingImageReturnsID(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:abc"}}, nil
		},
	})
	if got := inspectExistingImage(context.Background(), "runner-tag", &termio.Mock{}); got != "sha256:abc" {
		t.Errorf("inspectExistingImage = %q, want sha256:abc", got)
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
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (io.ReadCloser, error) {
			return nil, errors.New("pull boom")
		},
	})
	_, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", &termio.Mock{})
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
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("")), nil
		},
	})
	_, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "inspect pulled base image") {
		t.Errorf("resolveBaseDigest error = %v, want an inspect-pulled-image failure", err)
	}
}

func TestResolveBaseDigestPullDrainError(t *testing.T) {
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("not found")
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (io.ReadCloser, error) {
			return io.NopCloser(&errorReader{}), nil
		},
	})
	_, err := resolveBaseDigest(context.Background(), "debian:trixie-slim", &termio.Mock{})
	if err == nil || !strings.Contains(err.Error(), "pull base image") {
		t.Errorf("resolveBaseDigest error = %v, want a pull drain failure", err)
	}
}

// errorReader always fails reads, used to exercise the drain-failure path when
// consuming a Docker pull stream.
type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

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
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
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
