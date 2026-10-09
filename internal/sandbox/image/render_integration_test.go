//go:build integration

package image

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// TestRenderDockerfileBuilds builds a real image for every Dockerfile
// composition case in RenderDockerfile, covering the matrix of:
//
//	{no project Dockerfile, managed baseRef, custom base} x {docker, no docker}
//
// plus the docker marker injection cases. These run real docker builds and are
// excluded from the main suite via the `integration` build tag; run with
// `make test-integration`. They require a working docker daemon and network
// access (base image pulls and the node/docker/agent installs baked into the
// rendered Dockerfile).
//
// They exist to give refactoring safety for the per-tool multistage composition
// in dockerfile.go RenderDockerfile: each case renders a Dockerfile and asserts
// the whole thing actually builds. The custom-base cases use Fedora (dnf) so
// they exercise a non-Debian distro; the docker cases install the engine's
// runtime prerequisites in the body, which the final-stage check must accept.
func TestRenderDockerfileBuilds(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}

	version, err := resolveAgentVersion(context.Background(), a, "")
	if err != nil {
		t.Fatalf("resolve agent version: %v", err)
	}

	cases := []struct {
		name              string
		projectDockerfile []byte
		docker            bool
	}{
		{"no-dockerfile", nil, false},
		{"no-dockerfile-docker", nil, true},
		{"managed-base", []byte("FROM agents-sandbox/runner-base:latest\nRUN echo managed\n"), false},
		{
			"managed-base-docker-implied",
			[]byte("FROM agents-sandbox/runner-base-docker:latest\nRUN echo managed\n"),
			false,
		},
		{"managed-base-dind-implied", []byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo managed\n"), false},
		{"custom-base-fedora", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar && dnf clean all\n"), false},
		{"custom-base-fedora-docker", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n"), true},
		{"custom-base-docker-marker", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n" +
				"# agents-sandbox:docker\n" +
				"RUN echo after-docker\n"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := renderBytes(t, a, tc.projectDockerfile, tc.docker)
			buildDockerfile(t, a, rendered, version, tc.docker)
		})
	}
}

// TestRenderDockerfileToolLayersStayCached is the regression guard for the
// per-tool composition: mutating only the user body must not invalidate the
// node, agent, or docker install layers. It builds once, then rebuilds with a
// changed body and asserts the tool-stage RUN steps are cache hits.
func TestRenderDockerfileToolLayersStayCached(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	version, err := resolveAgentVersion(context.Background(), a, "")
	if err != nil {
		t.Fatalf("resolve agent version: %v", err)
	}

	base := []byte(
		"FROM fedora:latest\n" +
			"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n" +
			"RUN echo body-v1\n")
	changed := []byte(
		"FROM fedora:latest\n" +
			"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n" +
			"RUN echo body-v2\n")

	tag := "agents-sandbox/it-cache"
	t.Cleanup(func() {
		_, _ = docker.Get().
			ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	})

	// Prime the cache with the first body.
	buildDockerfileTag(t, a, renderBytes(t, a, base, true), version, true, tag)

	// Rebuild with a changed body, capturing the streamed build output.
	var lines []string
	buildDockerfileTagOutput(t, a, renderBytes(t, a, changed, true), version, true, tag, &lines)

	for _, stage := range []string{nodeStageAlias, agentStageAlias, dockerStageAlias} {
		if !stageStepCached(lines, stage) {
			t.Errorf("expected stage %q RUN to be a cache hit after only the user body changed; output:\n%s",
				stage, strings.Join(lines, "\n"))
		}
	}
}

// stageInstallMarkers maps each tool stage to a substring unique to its install
// RUN, so the streamed build output can be scanned for that step. The classic
// builder (what buildImage uses) prints "Step N/M : <instruction>" followed by
// " ---> Using cache", with no stage label; BuildKit prints "#N [<stage> ...]"
// followed by "#N CACHED". Keying on the instruction text works for both.
var stageInstallMarkers = map[string]string{
	nodeStageAlias:   "nodejs.org/dist",
	agentStageAlias:  "opencode.ai/install",
	dockerStageAlias: "download.docker.com",
}

// stageStepCached reports whether the install RUN step of the given stage was a
// cache hit in a streamed build, handling both BuildKit (#N CACHED) and classic
// (---> Using cache) output.
func stageStepCached(lines []string, stage string) bool {
	marker, ok := stageInstallMarkers[stage]
	if !ok {
		return false
	}
	for i, line := range lines {
		if !strings.Contains(line, marker) {
			continue
		}
		if len(lines) > i+1 && (strings.Contains(lines[i+1], "CACHED") || strings.Contains(lines[i+1], "Using cache")) {
			return true
		}
	}
	return false
}

// buildDockerfile builds the rendered Dockerfile through the production
// buildImage helper against the real docker daemon, failing the test if the
// build does not succeed. The built image is removed on test cleanup.
func buildDockerfile(t *testing.T, a agent.Agent, dockerfile []byte, agentVersion string, dockerEnabled bool) {
	t.Helper()
	tag := "agents-sandbox/it-" + sanitizeTag(t.Name())
	buildDockerfileTag(t, a, dockerfile, agentVersion, dockerEnabled, tag)
	t.Cleanup(func() {
		_, _ = docker.Get().
			ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	})
}

func buildDockerfileTag(
	t *testing.T,
	a agent.Agent,
	dockerfile []byte,
	agentVersion string,
	dockerEnabled bool,
	tag string,
) {
	t.Helper()
	buildDockerfileTagOutput(t, a, dockerfile, agentVersion, dockerEnabled, tag, nil)
}

func buildDockerfileTagOutput(
	t *testing.T,
	a agent.Agent,
	dockerfile []byte,
	agentVersion string,
	dockerEnabled bool,
	tag string,
	lines *[]string,
) {
	t.Helper()

	ctx := context.Background()
	if _, err := docker.Get().Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}

	line := func(s string) {
		if lines != nil {
			*lines = append(*lines, s)
		}
	}
	if err := buildImage(
		ctx,
		a,
		dockerfile,
		tag,
		false,
		agentVersion,
		"",
		"",
		dockerEnabled,
		imageIdentity{},
		line,
	); err != nil {
		t.Fatalf("docker image build failed: %v", err)
	}
}

// TestRenderDockerfileLoginShellHasImagePath proves the profile.d merge fires
// in a real login shell: /etc/profile resets PATH, and the installed script
// re-adds the image's entries. Covers the managed base (its /opt/agents-sandbox
// is the entry the reset drops) and a project ENV PATH entry.
func TestRenderDockerfileLoginShellHasImagePath(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, _ := agent.Lookup("opencode")
	version, err := resolveAgentVersion(context.Background(), a, "")
	if err != nil {
		t.Fatalf("resolve agent version: %v", err)
	}
	project := []byte("FROM agents-sandbox/runner-base:latest\nENV PATH=\"/opt/my-tools:${PATH}\"\n")

	tag := "agents-sandbox/it-login-path"
	t.Cleanup(func() {
		_, _ = docker.Get().
			ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	})
	buildDockerfileTag(t, a, renderBytes(t, a, project, false), version, false, tag)

	out, err := exec.Command("docker", "run", "--rm", tag, "bash", "-lc", `printf '%s' "$PATH"`).Output()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	loginPath := string(out)
	for _, want := range []string{"/opt/agents-sandbox/bin", "/opt/my-tools"} {
		if !strings.Contains(loginPath, want) {
			t.Errorf("login PATH %q missing %q", loginPath, want)
		}
	}
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
