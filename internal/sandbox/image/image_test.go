package image

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/inoio/opencode-sandbox/internal/agent"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// dockerConfigWith builds a Docker image config carrying the given agent label
// and environment for mocked ImageInspect results.
func dockerConfigWith(version string, env []string) *dockerspec.DockerOCIImageConfig {
	labels := map[string]string{}
	if version != "" {
		labels[agentLabelKey] = version
	}
	return &dockerspec.DockerOCIImageConfig{
		ImageConfig: ocispec.ImageConfig{Env: env, Labels: labels},
	}
}

func TestReferencesImageDetectsBaseImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-sandbox/runner-base:latest\nRUN echo hi\n")
	if !referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=true for Dockerfile with base image")
	}
}

func TestReferencesImageReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=false for non-base Dockerfile")
	}
}

func TestReferencesImageIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM opencode-sandbox/runner-base:latest\nFROM debian:trixie-slim\n")
	if referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=false for commented FROM")
	}
}

func TestReferencesImageMatchesImageIdentifierRegardlessOfTag(t *testing.T) {
	dockerfile := []byte("FROM opencode-sandbox/runner-base:custom\nRUN echo hi\n")
	if !referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=true for base image with a non-default tag")
	}
}

func TestReferencesImageMatchesMainImageWithStageAlias(t *testing.T) {
	dockerfile := []byte("FROM opencode-sandbox/runner-base:latest AS builder\nRUN echo hi\n")
	if !referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=true for base image declared as a stage")
	}
}

func TestReferencesImageIgnoresStageReference(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim AS base\nFROM base AS builder\n")
	if referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=false when base image is used as a stage reference")
	}
}

func TestReferencesImageIgnoresBuildFlag(t *testing.T) {
	dockerfile := []byte("FROM --platform=linux/amd64 opencode-sandbox/runner-base:latest\nRUN echo hi\n")
	if !referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=true for base image after build flag")
	}
}

func TestReferencesImageFalseWhenBaseNotLastStage(t *testing.T) {
	dockerfile := []byte("FROM opencode-sandbox/runner-base:latest AS base\nFROM debian:trixie-slim AS final\n")
	if referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=false when base image is not the last stage")
	}
}

func TestReferencesImageTrueWhenBaseIsLastStageViaAlias(t *testing.T) {
	dockerfile := []byte(
		"FROM debian:trixie-slim AS base\nFROM opencode-sandbox/runner-base:latest AS builder\nFROM builder\n",
	)
	if !referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=true when base image backs the final stage through an alias")
	}
}

func TestReferencesImageIgnoresIntermediateStage(t *testing.T) {
	dockerfile := []byte("FROM opencode-sandbox/runner-base:latest AS base\nFROM debian:trixie-slim AS final\n")
	if referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=false for base image used only as an intermediate stage")
	}
}

func TestReferencesImageTrueWhenLastStageReusesAliasOfBase(t *testing.T) {
	dockerfile := []byte("FROM opencode-sandbox/runner-base:latest AS base\nFROM base\n")
	if !referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=true when the last stage reuses an alias of the base image")
	}
}

func TestImageTag(t *testing.T) {
	got := imageTag("myproj-aBc1234D", "sha256:abc123def456")
	expected := "opencode-sandbox/runner-myproj-aBc1234D:3k5q07ywpibwp5"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestBuildDockerImageSetsHostUserBuildArgs(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	m := &docker.MockDockerClient{}
	var capturedBuildArgs map[string]*string
	m.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
		capturedBuildArgs = opts.BuildArgs
		return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	docker.WithDockerMock(t, m)

	a, _ := agent.Lookup("opencode")
	if err := buildImage(
		context.Background(),
		a,
		dockerfile,
		"tag",
		false,
		"",
		"debian:trixie-slim",
		false,
		func(string) {},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantUID := strconv.Itoa(os.Getuid())
	wantGID := strconv.Itoa(os.Getgid())
	if v := capturedBuildArgs["USER_UID"]; v == nil || *v != wantUID {
		t.Errorf("USER_UID: want %q, got %v", wantUID, v)
	}
	if v := capturedBuildArgs["USER_GID"]; v == nil || *v != wantGID {
		t.Errorf("USER_GID: want %q, got %v", wantGID, v)
	}
}

func TestDockerfileTarContainsDockerfile(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	tarBuf, err := dockerfileTar(dockerfile)
	if err != nil {
		t.Fatalf("dockerfileTar failed: %v", err)
	}

	tr := tar.NewReader(tarBuf)
	header, err := tr.Next()
	if err != nil {
		t.Fatalf("unexpected error reading tar: %v", err)
	}
	if header.Name != "Dockerfile" {
		t.Errorf("expected tar entry 'Dockerfile', got %q", header.Name)
	}
	content, err := io.ReadAll(tr)
	if err != nil {
		t.Fatalf("unexpected error reading tar content: %v", err)
	}
	if !bytes.Equal(content, dockerfile) {
		t.Errorf("tar content does not match dockerfile")
	}
}

func TestScanBuildOutputForwardsStreamLines(t *testing.T) {
	body := strings.NewReader(`{"stream":"Step 1/1 : FROM debian\n"}
{"stream":"\n"}
{"stream":"Successfully built abc123\n"}`)

	var got []string
	if err := scanBuildOutput(body, func(s string) { got = append(got, s) }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"Step 1/1 : FROM debian", "Successfully built abc123"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected lines %q, got %q", want, got)
	}
}

func TestScanBuildOutputReturnsErrorMessage(t *testing.T) {
	body := strings.NewReader(
		`{"errorDetail":{"message":"pull access denied for base"},"error":"pull access denied for base"}`,
	)

	err := scanBuildOutput(body, func(string) {})
	if err == nil {
		t.Fatal("expected scanBuildOutput to return an error")
	}
	if !strings.Contains(err.Error(), "pull access denied") {
		t.Errorf("expected error to mention the build failure, got %q", err)
	}
}

func TestEnsureImageReturnsErrorWhenBuildFails(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	l := &termio.Mock{}
	docker.WithDefaultErrorDockerMock(t)
	_, err := EnsureImageWithClient(
		context.Background(),
		a,
		nil,
		"test-project",
		BuildOptions{Force: true},
		l,
	)
	if err == nil {
		t.Error("expected error when Docker build fails")
	}
}

func TestReferencesImageDetectsDindImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-sandbox/runner-base-dind:latest\nRUN echo hi\n")
	if !referencesImage(dockerfile, naming.BaseDindImagePrefix) {
		t.Error("expected referencesImage=true for Dockerfile with dind FROM")
	}
}

func TestReferencesImageReturnsFalseForPlainBase(t *testing.T) {
	dockerfile := []byte("FROM opencode-sandbox/runner-base:latest\nRUN echo hi\n")
	if referencesImage(dockerfile, naming.BaseDindImagePrefix) {
		t.Error("expected referencesImage=false for plain base Dockerfile")
	}
}

func TestReferencesImageReturnsFalseForDindOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if referencesImage(dockerfile, naming.BaseDindImagePrefix) {
		t.Error("expected referencesImage=false for non-base Dockerfile")
	}
}

func TestReferencesImageIgnoresDindComment(t *testing.T) {
	dockerfile := []byte("# FROM opencode-sandbox/runner-base-dind:latest\nFROM debian:trixie-slim\n")
	if referencesImage(dockerfile, naming.BaseDindImagePrefix) {
		t.Error("expected referencesImage=false for commented FROM")
	}
}

func TestReferencesImageDindDoesNotMatchBase(t *testing.T) {
	dockerfile := []byte("FROM opencode-sandbox/runner-base-dind:latest\nRUN echo hi\n")
	if referencesImage(dockerfile, naming.BaseImagePrefix) {
		t.Error("expected referencesImage=false for dind Dockerfile with base image identifier (no false positive)")
	}
}

func TestEnsureImageDoesNotCreateDigestAliasTag(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	var tagged []string
	m := &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:abc123"}}, nil
		},
		ImageTagFn: func(_ context.Context, opts client.ImageTagOptions) (client.ImageTagResult, error) {
			tagged = append(tagged, opts.Target)
			return client.ImageTagResult{}, nil
		},
	}
	docker.WithDockerMock(t, m)

	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	_, err := EnsureImageWithClient(
		context.Background(),
		a,
		dockerfile,
		"test-project",
		BuildOptions{},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tagged) != 0 {
		t.Errorf("expected no digest alias tags, got: %v", tagged)
	}
}

func TestEnsureImageDoesNotLoadIntoMSB(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID:     "sha256:abc123",
					Config: dockerConfigWith("1.0.0", []string{"PATH=/usr/bin"}),
				},
			}, nil
		},
	})
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error {
			return errors.New("image not in cache")
		},
	}
	dockerfile := []byte("FROM opencode-sandbox/runner-base:latest\nRUN echo hi\n")
	_, err := EnsureImageWithClient(
		context.Background(),
		a,
		dockerfile,
		"test-project",
		BuildOptions{Force: false},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("EnsureImage must not load into microsandbox; got %d loads", len(msbClient.LoadedImages))
	}
}

func TestBuildImagePassesAgentVersionBuildArg(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	orig := resolveAgentVersion
	resolveAgentVersion = func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		return requested, nil
	}
	t.Cleanup(func() { resolveAgentVersion = orig })

	m := &docker.MockDockerClient{}
	var captured map[string]*string
	m.ImageInspectFn = func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{InspectResponse: image.InspectResponse{ID: "sha256:base"}}, nil
	}
	m.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
		captured = opts.BuildArgs
		return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	docker.WithDockerMock(t, m)

	_, err := EnsureImageWithClient(
		context.Background(),
		a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{Force: true, AgentVersion: "1.2.3"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected build args to be captured")
	}
	v := captured["OPENCODE_VERSION"]
	if v == nil || *v != "1.2.3" {
		t.Errorf("OPENCODE_VERSION build arg = %v, want %q", v, "1.2.3")
	}
	if base := captured["BASE_IMAGE"]; base == nil || *base != "debian:trixie-slim@sha256:base" {
		t.Errorf("BASE_IMAGE build arg = %v, want %q", base, "debian:trixie-slim@sha256:base")
	}
}

func TestEnsureImageReadsVersionAndEnvFromDocker(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	orig := resolveAgentVersion
	resolveAgentVersion = func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "2.0.0", nil
	}
	t.Cleanup(func() { resolveAgentVersion = orig })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID:     "sha256:abc123",
					Config: dockerConfigWith("2.0.0", []string{"PATH=/usr/bin"}),
				},
			}, nil
		},
	})

	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}

	info, err := EnsureImageWithClient(
		context.Background(),
		a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{Force: false},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Fatalf("expected no microsandbox load from EnsureImage, got %d", len(msbClient.LoadedImages))
	}
	if info.Env["PATH"] != "/usr/bin" {
		t.Errorf("info.Env = %v", info.Env)
	}
}

func TestEnsureImageReturnsDigestImageRefAsTag(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID: "sha256:abc123",
					Config: &dockerspec.DockerOCIImageConfig{
						ImageConfig: ocispec.ImageConfig{Env: []string{"PATH=/usr/bin"}},
					},
				},
			}, nil
		},
	})
	info, err := EnsureImageWithClient(
		context.Background(),
		a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{Force: false},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := imageTag("test-project", "sha256:abc123")
	if info.Tag != want {
		t.Errorf("info.Tag = %q, want digest-based image ref %q", info.Tag, want)
	}
}

func TestEnsureImageReadsVersionFromDocker(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	configpaths.WithMockConfigPaths(t)
	orig := resolveAgentVersion
	resolveAgentVersion = func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "3.0.0", nil
	}
	t.Cleanup(func() { resolveAgentVersion = orig })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: image.InspectResponse{
					ID:     "sha256:abc123",
					Config: dockerConfigWith("3.0.0", nil),
				},
			}, nil
		},
	})

	msbClient := &msb.MockMsbClient{}

	info, err := EnsureImageWithClient(
		context.Background(),
		a,
		[]byte("FROM debian:trixie-slim\nRUN echo hi\n"),
		"test-project",
		BuildOptions{Force: false},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load, got %d loads", len(msbClient.LoadedImages))
	}
	if info.Env == nil {
		t.Error("expected a non-nil env map from the built image")
	}
}

func TestEnsureLoadedSkipsWhenAlreadyCached(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	m := &docker.MockDockerClient{}
	docker.WithDockerMock(t, m)

	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
	}
	if err := EnsureLoaded(
		context.Background(),
		msbClient,
		"test-project",
		"opencode-sandbox/runner-test-project:abc",
		&termio.Mock{},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load when image is cached, got %d loads", len(msbClient.LoadedImages))
	}
}

func TestEnsureLoadedLoadsWhenNotCached(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, refs []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			if len(refs) != 1 || refs[0] != runnerTag("test-project") {
				t.Errorf("ImageSave refs = %v, want runner tag %q", refs, runnerTag("test-project"))
			}
			return io.NopCloser(strings.NewReader("tar-data")), nil
		},
	})

	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	if err := EnsureLoaded(
		context.Background(),
		msbClient,
		"test-project",
		"opencode-sandbox/runner-test-project:abc",
		&termio.Mock{},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Fatalf("expected 1 image load, got %d", len(msbClient.LoadedImages))
	}
	if msbClient.LoadedImages[0] != "opencode-sandbox/runner-test-project:abc" {
		t.Errorf(
			"loaded image ref = %q, want %q",
			msbClient.LoadedImages[0],
			"opencode-sandbox/runner-test-project:abc",
		)
	}
}

func TestEnsureLoadedReturnsErrorWhenSaveFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageSaveFn: func(_ context.Context, _ []string, _ ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return nil, errors.New("docker save failed")
		},
	})

	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return errors.New("not cached") },
	}
	err := EnsureLoaded(
		context.Background(),
		msbClient,
		"test-project",
		"opencode-sandbox/runner-test-project:abc",
		&termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error when Docker save fails")
	}
	if len(msbClient.LoadedImages) != 0 {
		t.Errorf("expected no load on save failure, got %d", len(msbClient.LoadedImages))
	}
}
