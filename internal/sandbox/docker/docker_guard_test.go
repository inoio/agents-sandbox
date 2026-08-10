package docker

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

func TestInstallFailFastGet_CausesPanicWithoutMock(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("fail-fast docker.Get should panic when no mock is installed, but did not")
		}
	}()
	InstallFailFastGet()
	c := Get()
	c.ImageBuild(context.Background(), io.NopCloser(nil), client.ImageBuildOptions{})
}

func TestInstallFailFastGet_OptInMockDoesNotPanic(t *testing.T) {
	WithNoopDockerMock(t)
	c := Get()
	c.ImageInspect(context.Background(), "ref")
	// no panic -> pass
}

func TestInstallFailFastGet_PanicMessageGuidesToMock(t *testing.T) {
	defer func() {
		msg := recover()
		if msg == nil {
			t.Fatal("expected panic")
		}
		if s, ok := msg.(string); !ok || !strings.Contains(s, "WithDockerMock") {
			t.Fatalf("panic message should mention WithDockerMock, got %v", msg)
		}
	}()
	InstallFailFastGet()
	Get().Ping(context.Background(), client.PingOptions{})
}
