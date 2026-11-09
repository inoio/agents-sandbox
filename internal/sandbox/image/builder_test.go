package image

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
)

// TestSelectBuilder pins the builder selection against the engine's advertised
// Builder-Version: "2" (BuilderBuildKit) selects the BuildKit path, while
// BuilderV1 and an absent header — what Docker Engine and podman report — both
// select the classic path.
func TestSelectBuilder(t *testing.T) {
	cases := []struct {
		name string
		ping build.BuilderVersion
		want builderKind
	}{
		{"buildkit", build.BuilderBuildKit, builderBuildKit},
		{"classic advertises 1", build.BuilderV1, builderClassic},
		{"empty means classic", "", builderClassic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
				PingFn: func(_ context.Context, _ client.PingOptions) (client.PingResult, error) {
					return client.PingResult{BuilderVersion: tc.ping}, nil
				},
			})
			got, err := selectBuilder(context.Background(), docker.Get())
			if err != nil {
				t.Fatalf("selectBuilder: %v", err)
			}
			if got != tc.want {
				t.Errorf("selectBuilder = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSelectBuilderPropagatesPingError pins that a ping failure surfaces from
// selection unchanged, so a build on an unreachable engine fails loudly instead
// of silently assuming the classic builder.
func TestSelectBuilderPropagatesPingError(t *testing.T) {
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		PingFn: func(context.Context, client.PingOptions) (client.PingResult, error) {
			return client.PingResult{}, errors.New("cannot connect to Docker daemon")
		},
	})
	_, err := selectBuilder(context.Background(), docker.Get())
	if err == nil || !strings.Contains(err.Error(), "cannot connect to Docker daemon") {
		t.Errorf("selectBuilder error = %v, want the ping error to surface", err)
	}
}

// TestBuildImageNoSilentDowngradeFromBuildKit pins the no-silent-downgrade
// invariant: when the engine advertises BuildKit, a BuildKit failure surfaces
// and the classic builder is never reached. Falling back would silently build
// with a different engine than the one configured.
func TestBuildImageNoSilentDowngradeFromBuildKit(t *testing.T) {
	a := agentOpencode(t)
	stubBuildKitClient(t, &fakeBuildKitSolver{solveErr: errors.New("buildkit boom")})
	classicCalled := false
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		PingFn: func(_ context.Context, _ client.PingOptions) (client.PingResult, error) {
			return client.PingResult{BuilderVersion: build.BuilderBuildKit}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			classicCalled = true
			return client.ImageBuildResult{Body: io.NopCloser(bytes.NewReader(nil))}, nil
		},
	})
	err := buildImage(
		context.Background(), docker.Get(), a,
		testBuildRequest([]byte("FROM debian:trixie-slim\n"), "", "debian:trixie-slim", "", imageIdentity{}),
		"tag", false, func(string) {},
	)
	if err == nil {
		t.Fatal("buildImage error = nil, want the BuildKit path error")
	}
	if !strings.Contains(err.Error(), "buildkit boom") {
		t.Errorf("buildImage error = %v, want the BuildKit solve error to surface", err)
	}
	if classicCalled {
		t.Error("buildImage reached the classic builder on a BuildKit engine")
	}
}
