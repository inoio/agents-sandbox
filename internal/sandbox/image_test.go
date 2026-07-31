package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/moby/moby/client"
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

type failingDockerClient struct{}

func (f *failingDockerClient) ImageBuild(
	_ context.Context,
	_ ui.Reader,
	_ client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	return client.ImageBuildResult{}, errors.New("docker unavailable")
}

func (f *failingDockerClient) ImageInspect(
	_ context.Context,
	_ string,
	_ ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	return client.ImageInspectResult{}, errors.New("docker unavailable")
}

func (f *failingDockerClient) ImageSave(
	_ context.Context,
	_ []string,
	_ ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	return nil, errors.New("docker unavailable")
}

func (f *failingDockerClient) Close() error {
	return nil
}

// recordingDockerClient captures the ImageBuild options so tests can assert
// on the build args actually forwarded to Docker. The build body is empty,
// which scanBuildOutput treats as a successful (EOF-terminated) build.
type recordingDockerClient struct {
	buildArgs map[string]*string
}

func (r *recordingDockerClient) ImageBuild(
	_ context.Context,
	_ ui.Reader,
	opts client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	r.buildArgs = opts.BuildArgs
	return client.ImageBuildResult{Body: ui.NopCloser(bytes.NewReader(nil))}, nil
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

func (r *recordingDockerClient) Close() error {
	return nil
}

func TestUserBuildArgs(t *testing.T) {
	got := userBuildArgs(1001, 20)
	if v := got["USER_UID"]; v == nil || *v != "1001" {
		t.Errorf("USER_UID: want %q, got %v", "1001", v)
	}
	if v := got["USER_GID"]; v == nil || *v != "20" {
		t.Errorf("USER_GID: want %q, got %v", "20", v)
	}
}

func TestBuildDockerImageSetsHostUserBuildArgs(t *testing.T) {
	l := output.NewPrinter(ui.Discard, false)
	rc := &recordingDockerClient{}
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")

	if err := buildDockerImage(context.Background(), rc, dockerfile, "tag", "label", false, l); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantUID := strconv.Itoa(os.Getuid())
	wantGID := strconv.Itoa(os.Getgid())
	if v := rc.buildArgs["USER_UID"]; v == nil || *v != wantUID {
		t.Errorf("USER_UID: want %q, got %v", wantUID, v)
	}
	if v := rc.buildArgs["USER_GID"]; v == nil || *v != wantGID {
		t.Errorf("USER_GID: want %q, got %v", wantGID, v)
	}
}

func TestEnsureImageReturnsErrorWhenBuildFails(t *testing.T) {
	l := output.NewPrinter(ui.Discard, false)
	_, _, _, err := EnsureImage(
		context.Background(),
		&failingDockerClient{},
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
	content, err := ui.ReadAll(tr)
	if err != nil {
		t.Fatalf("unexpected error reading tar content: %v", err)
	}
	if !bytes.Equal(content, dockerfile) {
		t.Errorf("tar content does not match dockerfile")
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

type tagTrackingDockerClient struct {
	builtTags []string
}

func (t *tagTrackingDockerClient) ImageBuild(
	_ context.Context,
	_ ui.Reader,
	opts client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	t.builtTags = append(t.builtTags, opts.Tags...)
	return client.ImageBuildResult{Body: ui.NopCloser(bytes.NewReader(nil))}, nil
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

func (t *tagTrackingDockerClient) Close() error {
	return nil
}

func TestEnsureImageBuildsDindBaseWhenDockerfileReferencesDind(t *testing.T) {
	cli := &tagTrackingDockerClient{}
	dockerfile := []byte("FROM opencode-msb/runner-base-dind:latest\nRUN echo hi\n")
	_, _, _, err := EnsureImage(context.Background(), cli, dockerfile, "test-project", false, newTestio(t))
	if err == nil {
		t.Fatal("expected error from ImageInspect, got nil")
	}
	wantTags := []string{BaseTag, DindBaseTag, "opencode-msb/runner-test-project:latest"}
	if !reflect.DeepEqual(cli.builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", cli.builtTags, wantTags)
	}
}

func TestEnsureImageDoesNotBuildDindForPlainBase(t *testing.T) {
	cli := &tagTrackingDockerClient{}
	dockerfile := []byte("FROM opencode-msb/runner-base:latest\nRUN echo hi\n")
	_, _, _, err := EnsureImage(context.Background(), cli, dockerfile, "test-project", false, newTestio(t))
	if err == nil {
		t.Fatal("expected error from ImageInspect, got nil")
	}
	wantTags := []string{BaseTag, "opencode-msb/runner-test-project:latest"}
	if !reflect.DeepEqual(cli.builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", cli.builtTags, wantTags)
	}
}

func TestEnsureImageDoesNotBuildDindOnForceWithoutReference(t *testing.T) {
	cli := &tagTrackingDockerClient{}
	dockerfile := []byte("FROM debian:trixie-slim\nRUN echo hi\n")
	_, _, _, err := EnsureImage(context.Background(), cli, dockerfile, "test-project", true, newTestio(t))
	if err == nil {
		t.Fatal("expected error from ImageInspect, got nil")
	}
	wantTags := []string{BaseTag, "opencode-msb/runner-test-project:latest"}
	if !reflect.DeepEqual(cli.builtTags, wantTags) {
		t.Errorf("built tags:\n  got:  %v\n  want: %v", cli.builtTags, wantTags)
	}
}
