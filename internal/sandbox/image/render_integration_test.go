//go:build integration

package image

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// TestRenderDockerfileBuilds builds a real image for every Dockerfile
// composition case in RenderDockerfile, covering the matrix of:
//
//	{no project Dockerfile, managed baseRef, custom base} x {dind, no dind}
//
// These run real docker builds and are excluded from the main suite via the
// `integration` build tag; run with `make test-integration`. They require a
// working docker daemon and network access (base image pulls and the
// node/docker/agent installs baked into the rendered Dockerfile).
//
// They exist to give refactoring safety for the snippet ordering in
// dockerfile.go RenderDockerfile: each case renders a Dockerfile and asserts the
// whole thing actually builds. In particular, the dind cases validate that the
// tool blocks appear in an order that succeeds when the base provides the
// documented prerequisites (see docs: runner image custom base). The custom-base
// cases use Fedora (dnf) so they exercise a non-Debian distro; they install the
// prerequisites the tool's appended blocks require.
func TestRenderDockerfileBuilds(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}

	// A real agent version is baked into the image via the OPENCODE_VERSION
	// build arg; resolve it the same way production does so the install succeeds.
	version, err := resolveAgentVersion(context.Background(), a, "")
	if err != nil {
		t.Fatalf("resolve agent version: %v", err)
	}

	cases := []struct {
		name              string
		projectDockerfile []byte
		dind              bool
	}{
		{"no-dockerfile", nil, false},
		{"no-dockerfile-dind", nil, true},
		{"managed-base", []byte("FROM agents-sandbox/runner-base:latest\nRUN echo managed\n"), false},
		{"managed-base-dind-implied", []byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo managed\n"), false},
		{"custom-base-fedora", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar && dnf clean all\n"), false},
		{"custom-base-fedora-dind", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := RenderDockerfile(a, tc.projectDockerfile, tc.dind)
			buildDockerfile(t, a, rendered, version, tc.dind)
		})
	}
}

// buildDockerfile builds the rendered Dockerfile through the production
// buildImage helper against the real docker daemon, failing the test if the
// build does not succeed. The built image is removed on test cleanup.
func buildDockerfile(t *testing.T, a agent.Agent, dockerfile []byte, agentVersion string, dind bool) {
	t.Helper()

	ctx := context.Background()
	if _, err := docker.Get().Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}

	tag := "agents-sandbox/it-" + sanitizeTag(t.Name())
	if err := buildImage(ctx, a, dockerfile, tag, false, agentVersion, "", "", dind, func(string) {}); err != nil {
		t.Fatalf("docker image build failed: %v", err)
	}

	t.Cleanup(func() {
		_, _ = docker.Get().ImageRemove(ctx, tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	})
}

// sanitizeTag renders a test name into a docker-tag-safe lowercase string.
func sanitizeTag(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
