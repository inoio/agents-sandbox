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

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func TestReferencesBaseDetectsBaseImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	if !referencesBase(dockerfile) {
		t.Error("expected ReferencesBase=true for Dockerfile with base FROM")
	}
}

func TestReferencesBaseReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if referencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for non-base Dockerfile")
	}
}

func TestReferencesBaseIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM opencode-msb/runner-base:latest\nFROM debian:trixie-slim\n")
	if referencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for commented FROM")
	}
}

func TestImageTag(t *testing.T) {
	got := imageTag("myproj-aBc1234D", "sha256:abc123def456")
	expected := "opencode-msb/runner-myproj-aBc1234D:3k5q07ywpibwp5"
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
	l := &termio.Mock{}

	if err := docker.BuildDockerImage(context.Background(), dockerfile, "tag", "label", false, l); err != nil {
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

func TestEnsureImageReturnsErrorWhenBuildFails(t *testing.T) {
	l := &termio.Mock{}
	docker.WithDefaultErrorDockerMock(t)
	_, _, _, err := ensureImageWithClient(
		context.Background(),
		&MockMsbClient{},
		embeddedDockerfile,
		"test-project",
		true,
		l,
	)
	if err == nil {
		t.Error("expected error when Docker build fails")
	}
}

func TestEmbeddedDindDockerfileIsNonEmpty(t *testing.T) {
	if len(embeddedDindDockerfile) == 0 {
		t.Error("expected EmbeddedDindDockerfile to be non-empty")
	}
}

func TestReferencesDindBaseDetectsDindImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	if !referencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=true for Dockerfile with dind FROM")
	}
}

func TestReferencesDindBaseReturnsFalseForPlainBase(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	if referencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=false for plain base Dockerfile")
	}
}

func TestReferencesDindBaseReturnsFalseForOtherImage(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	if referencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=false for non-base Dockerfile")
	}
}

func TestReferencesDindBaseIgnoresComments(t *testing.T) {
	dockerfile := []byte("# FROM opencode-msb/runner-base-dind:latest\nFROM debian:trixie-slim\n")
	if referencesDindBase(dockerfile) {
		t.Error("expected ReferencesDindBase=false for commented FROM")
	}
}

func TestReferencesBaseReturnsFalseForDindImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	if referencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for dind Dockerfile (no false positive)")
	}
}

func TestEnsureImageBuildsDindBaseWhenDockerfileReferencesDind(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	wantTags := []string{baseTag, dindBaseTag, "opencode-msb/runner-test-project:latest"}
	runEnsureImageTagTest(t, dockerfile, false, wantTags)
}

func TestEnsureImageDoesNotBuildDindForPlainBase(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	wantTags := []string{baseTag, "opencode-msb/runner-test-project:latest"}
	runEnsureImageTagTest(t, dockerfile, false, wantTags)
}

func TestEnsureImageDoesNotBuildDindOnForceWithoutReference(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	wantTags := []string{baseTag, "opencode-msb/runner-test-project:latest"}
	runEnsureImageTagTest(t, dockerfile, true, wantTags)
}

func runEnsureImageTagTest(t *testing.T, dockerfile []byte, force bool, wantTags []string) {
	t.Helper()
	m := &docker.MockDockerClient{}
	var builtTags []string
	m.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
		builtTags = append(builtTags, opts.Tags...)
		return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	docker.WithDockerMock(t, m)

	_, _, _, err := ensureImageWithClient(
		context.Background(),
		&MockMsbClient{},
		dockerfile,
		"test-project",
		force,
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", builtTags, wantTags)
	}
}

func TestEnsureImageLoadsIntoMSBWhenNotCached(t *testing.T) {
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
	msbClient := &MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error {
			return errors.New("image not in cache")
		},
	}
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	_, _, _, err := ensureImageWithClient(
		context.Background(),
		msbClient,
		dockerfile,
		"test-project",
		false,
		&termio.Mock{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Fatalf("expected 1 image load, got %d", len(msbClient.LoadedImages))
	}
	if !strings.HasPrefix(msbClient.LoadedImages[0], "opencode-msb/runner-test-project:") {
		t.Errorf("unexpected loaded image ref: %s", msbClient.LoadedImages[0])
	}
}
