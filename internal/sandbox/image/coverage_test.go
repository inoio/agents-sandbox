package image

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestEnsureImageSuccess(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return nil, errors.New("image not found")
		},
	})
	fakeBuilder(t, builder.BuildResult{
		ArchivePath: "/tmp/a", Digest: "sha256:abc123", Env: []string{"PATH=/usr/bin"},
	}, nil)

	info, err := EnsureImage(context.Background(), a, "test-project", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Env["PATH"] != "/usr/bin" {
		t.Errorf("info.Env = %v", info.Env)
	}
}

func TestEnsureImageReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	fakeBuilder(t, builder.BuildResult{}, errors.New("build boom"))
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	_, err := EnsureImage(
		context.Background(), a, "test-project", BuildOptions{Force: true}, &termio.Mock{},
	)
	if err == nil {
		t.Error("expected EnsureImage to return an error when the builder fails")
	}
}

func TestBuildSuccess(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	archivePath := newArchiveFile(t)
	fakeBuilder(t, builder.BuildResult{
		ArchivePath: archivePath, Digest: "sha256:abc123", Env: []string{"PATH=/usr/bin"},
	}, nil)
	msbClient := &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return errors.New("not cached") },
	}
	msb.WithMsbMock(t, msbClient)

	if err := Build(context.Background(), a, "test-project", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msbClient.LoadedImages) != 1 {
		t.Errorf("expected 1 image load, got %d", len(msbClient.LoadedImages))
	}
}

func TestBuildRemovesArchiveAfterLoad(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	archivePath := newArchiveFile(t)
	fakeBuilder(t, builder.BuildResult{ArchivePath: archivePath, Digest: "sha256:abc123"}, nil)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return errors.New("not cached") },
	})

	if err := Build(context.Background(), a, "test-project", BuildOptions{}, &termio.Mock{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("archive %s still present after Build; err = %v", archivePath, err)
	}
}

func TestBuildReturnsErrorWhenImageBuildFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	fakeBuilder(t, builder.BuildResult{}, errors.New("build boom"))
	msb.WithMsbMock(t, &msb.MockMsbClient{})

	if err := Build(context.Background(), a, "test-project", BuildOptions{Force: true}, &termio.Mock{}); err == nil {
		t.Error("expected Build to return an error when image build fails")
	}
}

func TestBuildReturnsErrorWhenLoadFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersion(t, "1.2.3")
	a, _ := agent.Lookup("opencode")
	fakeBuilder(t, builder.BuildResult{ArchivePath: newArchiveFile(t), Digest: "sha256:abc123"}, nil)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return errors.New("not cached") },
		ImageLoadFn: func(context.Context, string, io.Reader) error {
			return errors.New("load boom")
		},
	})

	if err := Build(context.Background(), a, "test-project", BuildOptions{}, &termio.Mock{}); err == nil {
		t.Error("expected Build to return an error when loading the image into microsandbox fails")
	}
}

func TestEnsureImageReturnsErrorWhenVersionResolveFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "", errors.New("resolve boom")
	})
	_, err := EnsureImageWithClient(
		context.Background(), a, []byte("FROM x\n"), "test-project", BuildOptions{}, &termio.Mock{},
	)
	if err == nil {
		t.Error("expected error when resolving agent version fails")
	}
}

func TestEnsureImageReturnsErrorWhenDeferredVersionResolveFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, _ := agent.Lookup("opencode")
	WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		if requested != "" {
			t.Errorf("resolver requested = %q, want empty version", requested)
		}
		return "", errors.New("resolve boom")
	})
	msb.WithMsbMock(t, &msb.MockMsbClient{
		ImageInspectFn: func(context.Context, string) (*msbSdk.ImageConfig, error) {
			return msbImageConfig("sha256:existing", map[string]string{dockerfileIDLabelKey: "stale"}, nil), nil
		},
	})
	_, err := EnsureImageWithClient(
		context.Background(), a, []byte("FROM x\n"), "test-project",
		BuildOptions{UserProvided: true}, &termio.Mock{},
	)
	if err == nil {
		t.Error("expected error when resolving the deferred agent version fails")
	}
}
