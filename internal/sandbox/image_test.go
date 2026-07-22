package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
)

func TestReferencesBaseDetectsBaseImage(t *testing.T) {
	dockerfile := []byte("FROM opencode-msb/runner:base\nRUN echo hi\n")
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
	dockerfile := []byte("# FROM opencode-msb/runner:base\nFROM debian:trixie-slim\n")
	if ReferencesBase(dockerfile) {
		t.Error("expected ReferencesBase=false for commented FROM")
	}
}

func TestImageTag(t *testing.T) {
	got := ImageTag("sha256:abc123def456")
	expected := "opencode-msb/runner:sha256-abc123def456"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

type failingDockerClient struct{}

func (f *failingDockerClient) ImageBuild(ctx context.Context, buildContext io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error) {
	return client.ImageBuildResult{}, errors.New("docker unavailable")
}

func (f *failingDockerClient) ImageInspect(ctx context.Context, imageID string, inspectOpts ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	return client.ImageInspectResult{}, errors.New("docker unavailable")
}

func (f *failingDockerClient) ImageSave(ctx context.Context, imageIDs []string, saveOpts ...client.ImageSaveOption) (client.ImageSaveResult, error) {
	return nil, errors.New("docker unavailable")
}

func (f *failingDockerClient) Close() error {
	return nil
}

func TestEnsureImageReturnsErrorWhenBuildFails(t *testing.T) {
	l := log.New(io.Discard, false)
	_, _, err := EnsureImage(context.Background(), &failingDockerClient{}, EmbeddedDockerfile, true, l)
	if err == nil {
		t.Error("expected error when Docker build fails")
	}
}

func TestDockerfileTarContainsDockerfile(t *testing.T) {
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	tarBuf := dockerfileTar(dockerfile)

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
