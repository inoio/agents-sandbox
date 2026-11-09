//go:build integration

package image

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// TestBuildImageBuildKitRealDocker builds a minimal Dockerfile through the
// BuildKit path against the real daemon and asserts the tagged image is
// inspectable with a digest. It skips cleanly on an engine that does not
// advertise BuildKit (Builder-Version "2"), so it only runs where the embedded
// builder is reachable over the /grpc + /session hijack this path uses.
//
// The Dockerfile is FROM scratch plus a COPY from the synthetic context, so the
// build needs no network: it exercises the filesync context transfer and the
// moby exporter without a base-image pull.
func TestBuildImageBuildKitRealDocker(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	ctx := context.Background()
	ping, err := docker.Get().Ping(ctx, client.PingOptions{})
	if err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}
	if ping.BuilderVersion != build.BuilderBuildKit {
		t.Skipf("engine does not advertise BuildKit (Builder-Version=%q)", ping.BuilderVersion)
	}

	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}

	tag := "agents-sandbox/it-buildkit"
	t.Cleanup(func() { removeTestImage(tag) })

	var lines []string
	buildErr := buildImageBuildKit(
		ctx,
		docker.Get(),
		a,
		testBuildRequest([]byte("FROM scratch\nCOPY Dockerfile /Dockerfile\n"), "", "", "", imageIdentity{}),
		tag,
		false,
		func(s string) { lines = append(lines, s) },
	)
	if buildErr != nil {
		t.Fatalf("BuildKit build failed: %v\noutput:\n%s", buildErr, lines)
	}

	inspect, err := docker.Get().ImageInspect(ctx, tag)
	if err != nil {
		t.Fatalf("inspect the built image: %v", err)
	}
	if inspect.ID == "" {
		t.Error("built image has no ID/digest")
	}
}
