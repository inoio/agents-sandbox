package image

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPreviewImageNeverResolvesVersion guards the core preview invariant: the
// read-only path must never resolve an agent version over the network, even
// when no version is pinned.
func TestPreviewImageNeverResolvesVersion(t *testing.T) {
	WithMockAgentVersionResolver(t, func(context.Context, agent.Agent, string) (string, error) {
		t.Fatal("previewRunnerImage must not resolve the agent version")
		return "", nil
	})
	docker.WithNoopDockerMock(t)
	a := agentOpencode(t)

	p, err := previewRunnerImage(
		context.Background(), a, nil, "slug",
		BuildOptions{AgentVersion: testAgentVersion}, &termio.Mock{},
	)
	if err != nil {
		t.Fatalf("previewRunnerImage: %v", err)
	}
	if !p.NeedsBuild {
		t.Error("NeedsBuild = false, want true")
	}
	if want := runnerTag("slug", "opencode"); p.Tag != want {
		t.Errorf("Tag = %q, want %q", p.Tag, want)
	}
	if p.AgentVersion != testAgentVersion {
		t.Errorf("AgentVersion = %q, want %q", p.AgentVersion, testAgentVersion)
	}
	if p.ResolveVersionAtBuild {
		t.Error("ResolveVersionAtBuild = true, want false")
	}
}

// TestPreviewImageReportsReuseWhenDockerfileIDMatches pins the reuse branch: an
// existing image whose dockerfile-id label matches the rendered plan needs no
// build and yields no reasons.
func TestPreviewImageReportsReuseWhenDockerfileIDMatches(t *testing.T) {
	a := agentOpencode(t)
	rendered, err := RenderDockerfile(a, testAgentVersion, nil, dockerOff)
	if err != nil {
		t.Fatalf("RenderDockerfile: %v", err)
	}
	id := computeDockerfileID(rendered, testAgentVersion)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(
			_ context.Context, _ string, _ ...client.ImageInspectOption,
		) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				Config: &dockerspec.DockerOCIImageConfig{Labels: map[string]string{dockerfileIDLabelKey: id}},
			}, nil
		},
	})

	p, err := previewRunnerImage(
		context.Background(), a, nil, "slug",
		BuildOptions{AgentVersion: testAgentVersion}, &termio.Mock{},
	)
	if err != nil {
		t.Fatalf("previewRunnerImage: %v", err)
	}
	if p.NeedsBuild {
		t.Errorf("NeedsBuild = true, want false (reasons %v)", p.Reasons)
	}
	if len(p.Reasons) != 0 {
		t.Errorf("Reasons = %v, want empty", p.Reasons)
	}
}

// TestPreviewImageForcedBuild pins that a forced rebuild is reported without
// inspecting an image.
func TestPreviewImageForcedBuild(t *testing.T) {
	docker.WithNoopDockerMock(t)
	a := agentOpencode(t)

	p, err := previewRunnerImage(
		context.Background(), a, nil, "slug",
		BuildOptions{AgentVersion: testAgentVersion, Force: true}, &termio.Mock{},
	)
	if err != nil {
		t.Fatalf("previewRunnerImage: %v", err)
	}
	if !p.NeedsBuild {
		t.Error("NeedsBuild = false, want true")
	}
	if !slices.Contains(p.Reasons, "forced by --rebuild") {
		t.Errorf("Reasons = %v, want the forced reason", p.Reasons)
	}
}

// TestPreviewImageEmptyVersionReportsResolveAtBuild pins that an unpinned
// version is reported as resolved at build time, without resolving it now.
func TestPreviewImageEmptyVersionReportsResolveAtBuild(t *testing.T) {
	WithMockAgentVersionResolver(t, func(context.Context, agent.Agent, string) (string, error) {
		t.Fatal("previewRunnerImage must not resolve the agent version")
		return "", nil
	})
	docker.WithNoopDockerMock(t)
	a := agentOpencode(t)

	p, err := previewRunnerImage(context.Background(), a, nil, "slug", BuildOptions{}, &termio.Mock{})
	if err != nil {
		t.Fatalf("previewRunnerImage: %v", err)
	}
	if !p.ResolveVersionAtBuild {
		t.Error("ResolveVersionAtBuild = false, want true")
	}
	if p.AgentVersion != "" {
		t.Errorf("AgentVersion = %q, want empty", p.AgentVersion)
	}
}

// TestRunnerDockerfileIDMatchesTheRenderedPlan pins the exported identity
// helper that higher-layer preview tests seed an image label with: it must equal
// computeDockerfileID over the rendered project Dockerfile, so a seeded image
// matches the plan the preview computes.
func TestRunnerDockerfileIDMatchesTheRenderedPlan(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)

	id, err := RunnerDockerfileID(a, testAgentVersion, dockerOff)
	if err != nil {
		t.Fatalf("RunnerDockerfileID: %v", err)
	}
	rendered, err := RenderProjectDockerfile(a, testAgentVersion, dockerOff)
	if err != nil {
		t.Fatalf("RenderProjectDockerfile: %v", err)
	}
	if want := computeDockerfileID(rendered, testAgentVersion); id != want {
		t.Errorf("RunnerDockerfileID = %q, want %q", id, want)
	}
}

// TestRunnerDockerfileIDSurfacesRenderError pins that a project Dockerfile that
// cannot render propagates the error instead of returning a bogus id.
func TestRunnerDockerfileIDSurfacesRenderError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a := agentOpencode(t)
	dockerfilePath := configpaths.Get().ProjectDockerfile()
	if err := os.MkdirAll(filepath.Dir(dockerfilePath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(dockerfilePath, []byte("FROM ubuntu:24.04 AS "+baseStageAlias+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := RunnerDockerfileID(a, testAgentVersion, dockerOff); err == nil {
		t.Fatal("expected a render error for a reserved stage alias")
	}
}

// TestPreviewImageReadsProjectDockerfile exercises the exported entry: it warns
// ignored project config, reads the project Dockerfile, and delegates to the
// read-only plan path.
func TestPreviewImageReadsProjectDockerfile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	WithMockAgentVersionResolver(t, func(context.Context, agent.Agent, string) (string, error) {
		t.Fatal("PreviewImage must not resolve the agent version")
		return "", nil
	})
	docker.WithNoopDockerMock(t)
	a := agentOpencode(t)

	p, err := PreviewImage(
		context.Background(), a, "slug", BuildOptions{AgentVersion: testAgentVersion}, &termio.Mock{},
	)
	if err != nil {
		t.Fatalf("PreviewImage: %v", err)
	}
	if want := runnerTag("slug", "opencode"); p.Tag != want {
		t.Errorf("Tag = %q, want %q", p.Tag, want)
	}
}

// TestPreviewImageSurfacesRenderError pins that a plan that cannot render (a
// reserved stage alias) fails the preview instead of reporting a bogus plan.
func TestPreviewImageSurfacesRenderError(t *testing.T) {
	docker.WithNoopDockerMock(t)
	a := agentOpencode(t)
	project := []byte("FROM ubuntu:24.04 AS " + baseStageAlias + "\nRUN echo hi\n")

	_, err := previewRunnerImage(
		context.Background(), a, project, "slug",
		BuildOptions{AgentVersion: testAgentVersion}, &termio.Mock{},
	)
	if err == nil {
		t.Fatal("expected a render error for a reserved stage alias")
	}
}

// TestPreviewImageWarnsOnLegacyDockerfile pins parity with EnsureImage: the
// preview surfaces the same project-Dockerfile deprecation warnings.
func TestPreviewImageWarnsOnLegacyDockerfile(t *testing.T) {
	docker.WithNoopDockerMock(t)
	a := agentOpencode(t)
	project := []byte("FROM agents-sandbox/runner-base-dind\n# agents-sandbox:dind\nRUN echo hi\n")
	ui := &termio.Mock{}

	if _, err := previewRunnerImage(
		context.Background(), a, project, "slug",
		BuildOptions{AgentVersion: testAgentVersion}, ui,
	); err != nil {
		t.Fatalf("previewRunnerImage: %v", err)
	}

	joined := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{"runner-base-dind", "# agents-sandbox:dind"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a deprecation warning mentioning %q, got %v", want, ui.WarnCalls)
		}
	}
}
