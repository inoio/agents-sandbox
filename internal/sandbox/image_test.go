package sandbox

import (
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

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

func TestReferencesBaseDetectsBaseImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	if !ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=true for Dockerfile with base FROM")
	}
}

func TestReferencesBaseReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for non-base Dockerfile")
	}
}

func TestReferencesBaseIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM opencode-msb/runner-base:latest\nFROM debian:trixie-slim\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for commented FROM")
	}
}

func TestImageTag(t *testing.T) {
	got := ImageTag("myproj-aBc1234D", "sha256:abc123def456")
	expected := "opencode-msb/runner-myproj-aBc1234D:3k5q07ywpibwp5"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// recordingDockerClient captures the ImageBuild options so tests can assert
// on the build args actually forwarded to Docker. The build body is empty,
// which scanBuildOutput treats as a successful (EOF-terminated) build.
type recordingDockerClient struct {
	buildArgs map[string]*string
}

func (r *recordingDockerClient) ImageBuild(
	_ context.Context,
	_ io.Reader,
	opts client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	r.buildArgs = opts.BuildArgs
	return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
}

func (r *recordingDockerClient) ImageInspect(
	context.Context,
	string,
	...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	return client.ImageInspectResult{}, errors.New("not implemented")
}

func (r *recordingDockerClient) ImageSave(
	context.Context,
	[]string,
	...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingDockerClient) ImageRemove(
	_ context.Context,
	_ string,
	_ client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	return client.ImageRemoveResult{}, nil
}

func (r *recordingDockerClient) ImageTag(
	_ context.Context,
	_ client.ImageTagOptions,
) (client.ImageTagResult, error) {
	return client.ImageTagResult{}, nil
}

func (r *recordingDockerClient) Close() error {
	return nil
}

func TestBuildDockerImageSetsHostUserBuildArgs(t *testing.T) {
	l := &stdio.Mock{}
	dockerMock := &recordingDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")

	if err := docker.BuildDockerImage(context.Background(), dockerfile, "tag", "label", false, l); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantUID := strconv.Itoa(os.Getuid())
	wantGID := strconv.Itoa(os.Getgid())
	if v := dockerMock.buildArgs["USER_UID"]; v == nil || *v != wantUID {
		t.Errorf("USER_UID: want %q, got %v", wantUID, v)
	}
	if v := dockerMock.buildArgs["USER_GID"]; v == nil || *v != wantGID {
		t.Errorf("USER_GID: want %q, got %v", wantGID, v)
	}
}

func TestEnsureImageReturnsErrorWhenBuildFails(t *testing.T) {
	l := &stdio.Mock{}
	docker.WithDefaultErrorDockerMock(t)
	_, _, _, err := ensureImageWithClient(
		context.Background(),
		&MockMsbClient{},
		EmbeddedDockerfile,
		"test-project",
		true,
		l,
	)
	if err == nil {
		t.Error("expected error when Docker build fails")
	}
}

func TestEmbeddedDindDockerfileIsNonEmpty(t *testing.T) {
	if len(EmbeddedDindDockerfile) == 0 {
		t.Error("expected EmbeddedDindDockerfile to be non-empty")
	}
}

func TestReferencesDindBaseDetectsDindImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	if !ReferencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=true for Dockerfile with dind FROM")
	}
}

func TestReferencesDindBaseReturnsFalseForPlainBase(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	if ReferencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=false for plain base Dockerfile")
	}
}

func TestReferencesDindBaseReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if ReferencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=false for non-base Dockerfile")
	}
}

func TestReferencesDindBaseIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM opencode-msb/runner-base-dind:latest\nFROM debian:trixie-slim\n")
	if ReferencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=false for commented FROM")
	}
}

func TestReferencesBaseReturnsFalseForDindImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for dind Dockerfile (no false positive)")
	}
}

type imageInspectDockerClient struct {
	inspectID string
}

func (i *imageInspectDockerClient) ImageBuild(
	_ context.Context,
	_ io.Reader,
	_ client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
}

func (i *imageInspectDockerClient) ImageInspect(
	_ context.Context,
	_ string,
	_ ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	return client.ImageInspectResult{
		InspectResponse: image.InspectResponse{
			ID: i.inspectID,
			Config: &dockerspec.DockerOCIImageConfig{
				ImageConfig: ocispec.ImageConfig{Env: []string{"PATH=/usr/bin"}},
			},
		},
	}, nil
}

func (i *imageInspectDockerClient) ImageSave(
	_ context.Context,
	_ []string,
	_ ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (i *imageInspectDockerClient) ImageRemove(
	_ context.Context,
	_ string,
	_ client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	return client.ImageRemoveResult{}, nil
}

func (i *imageInspectDockerClient) ImageTag(
	_ context.Context,
	_ client.ImageTagOptions,
) (client.ImageTagResult, error) {
	return client.ImageTagResult{}, nil
}

func (i *imageInspectDockerClient) Close() error {
	return nil
}

type tagTrackingDockerClient struct {
	builtTags []string
}

func (t *tagTrackingDockerClient) ImageBuild(
	_ context.Context,
	_ io.Reader,
	opts client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	t.builtTags = append(t.builtTags, opts.Tags...)
	return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
}

func (t *tagTrackingDockerClient) ImageInspect(
	context.Context,
	string,
	...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	return client.ImageInspectResult{}, errors.New("not implemented")
}

func (t *tagTrackingDockerClient) ImageSave(
	context.Context,
	[]string,
	...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	return nil, errors.New("not implemented")
}

func (t *tagTrackingDockerClient) ImageRemove(
	_ context.Context,
	_ string,
	_ client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	return client.ImageRemoveResult{}, nil
}

func (t *tagTrackingDockerClient) ImageTag(
	_ context.Context,
	_ client.ImageTagOptions,
) (client.ImageTagResult, error) {
	return client.ImageTagResult{}, nil
}

func (t *tagTrackingDockerClient) Close() error {
	return nil
}

func TestEnsureImageBuildsDindBaseWhenDockerfileReferencesDind(t *testing.T) {
	dockerMock := &tagTrackingDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	_, _, _, err := ensureImageWithClient(
		context.Background(),
		&MockMsbClient{},
		dockerfile,
		"test-project",
		false,
		&stdio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error from ImageInspect, got nil")
	}
	wantTags := []string{BaseTag, DindBaseTag, "opencode-msb/runner-test-project:latest"}
	if !reflect.DeepEqual(dockerMock.builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", dockerMock.builtTags, wantTags)
	}
}

func TestEnsureImageDoesNotBuildDindForPlainBase(t *testing.T) {
	dockerMock := &tagTrackingDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	_, _, _, err := ensureImageWithClient(
		context.Background(),
		&MockMsbClient{},
		dockerfile,
		"test-project",
		false,
		&stdio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error from ImageInspect, got nil")
	}
	wantTags := []string{BaseTag, "opencode-msb/runner-test-project:latest"}
	if !reflect.DeepEqual(dockerMock.builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", dockerMock.builtTags, wantTags)
	}
}

func TestEnsureImageDoesNotBuildDindOnForceWithoutReference(t *testing.T) {
	dockerMock := &tagTrackingDockerClient{}
	docker.WithDockerMock(t, dockerMock)
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	_, _, _, err := ensureImageWithClient(
		context.Background(),
		&MockMsbClient{},
		dockerfile,
		"test-project",
		true,
		&stdio.Mock{},
	)
	if err == nil {
		t.Fatal("expected error from ImageInspect, got nil")
	}
	wantTags := []string{BaseTag, "opencode-msb/runner-test-project:latest"}
	if !reflect.DeepEqual(dockerMock.builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", dockerMock.builtTags, wantTags)
	}
}

func TestEnsureImageLoadsIntoMSBWhenNotCached(t *testing.T) {
	dockerMock := &imageInspectDockerClient{inspectID: "sha256:abc123"}
	docker.WithDockerMock(t, dockerMock)
	msbClient := &MockMsbClient{
		imageGetErr: errors.New("image not in cache"),
	}
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	_, _, _, err := ensureImageWithClient(
		context.Background(),
		msbClient,
		dockerfile,
		"test-project",
		false,
		&stdio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.loadedImages) != 1 {
		t.Fatalf("expected 1 image load, got %d", len(msbClient.loadedImages))
	}
	if !strings.HasPrefix(msbClient.loadedImages[0], "opencode-msb/runner-test-project:") {
		t.Errorf("unexpected loaded image ref: %s", msbClient.loadedImages[0])
	}
}
